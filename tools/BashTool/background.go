// DESC: background job manager for the bash tool. Commands that outlive their
// sync wait (or were asked to run_in_background) finish here as detached
// processes, so the caller isn't blocked and cancellation of the original
// turn doesn't kill them.
package bashtool

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// lockedBuffer is a strings buffer that is safe to read while os/exec's copy
// goroutines are still feeding it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// job is one running (or finished) background command.
type job struct {
	ID         string
	Command    string
	WorkingDir string
	StartedAt  time.Time

	cmd    *exec.Cmd
	stdout *lockedBuffer
	stderr *lockedBuffer
	doneCh chan struct{}

	mu   sync.Mutex
	done bool
	err  error
}

// Done returns a channel that closes when the command has finished running.
func (j *job) Done() <-chan struct{} { return j.doneCh }

// Result blocks until the job finishes, then returns its full output.
func (j *job) Result() (stdout, stderr string, err error) {
	<-j.doneCh
	return j.stdout.String(), j.stderr.String(), j.err
}

// Snapshot returns the output collected so far and whether the job is still
// running, without blocking.
func (j *job) Snapshot() (stdout, stderr string, running bool) {
	stdout, stderr = j.stdout.String(), j.stderr.String()
	j.mu.Lock()
	defer j.mu.Unlock()
	return stdout, stderr, !j.done
}

// Kill terminates the process if it's still running.
func (j *job) Kill() {
	if j.cmd != nil && j.cmd.Process != nil {
		_ = j.cmd.Process.Kill()
	}
}

func (j *job) finish(err error) {
	j.mu.Lock()
	j.done = true
	j.err = err
	j.mu.Unlock()
	close(j.doneCh)
}

// jobManager tracks all background jobs by ID. It's a package-level singleton
// so any future tool (job_output, job_kill) can reach the same jobs.
type jobManager struct {
	mu   sync.RWMutex
	jobs map[string]*job
}

var bg = &jobManager{jobs: make(map[string]*job)}

var jobSeq atomic.Uint64

func newJobID() string {
	n := jobSeq.Add(1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), n)
}

// start launches command in wd as a detached background process and records
// it in the manager. The context this runs under never cancels the child once
// it's been moved to the background.
func (m *jobManager) start(ctx context.Context, wd, command string) (*job, error) {
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = wd

	out := &lockedBuffer{}
	errOut := &lockedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = errOut

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	j := &job{
		ID:         newJobID(),
		Command:    command,
		WorkingDir: wd,
		StartedAt:  time.Now(),
		cmd:        cmd,
		stdout:     out,
		stderr:     errOut,
		doneCh:     make(chan struct{}),
	}
	m.add(j)
	go func() { j.finish(cmd.Wait()) }()
	return j, nil
}

func (m *jobManager) add(j *job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

func (m *jobManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)
}

func (m *jobManager) get(id string) (*job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Cleanup drops finished jobs from the map so long sessions don't leak them.
func (m *jobManager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		select {
		case <-j.doneCh:
			delete(m.jobs, id)
		default:
		}
	}
}

// KillAll kills every tracked job and empties the map. Used by tests and as a
// safety net on shutdown; ~always safe because killing a finished job is a no-op.
func (m *jobManager) KillAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		j.Kill()
		delete(m.jobs, id)
	}
}
