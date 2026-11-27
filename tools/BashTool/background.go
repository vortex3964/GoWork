// DESC: background job manager for the bash tool. Commands that outlive their
// sync wait (or were asked to run_in_background) finish here as detached
// processes, so the caller isn't blocked and cancellation of the original
// turn doesn't kill them.
package bashtool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// maxBufferedOutput is the hard cap on bytes retained per output stream
	// while a command runs. It equals MaxOutputLength so an already-capped
	// buffer never needs a second round of truncation at result time.
	maxBufferedOutput = MaxOutputLength

	// truncationMarkerReserve is the space set aside inside the cap for the
	// "... [N chars truncated] ..." marker, so the assembled string also
	// stays at or below maxBufferedOutput.
	truncationMarkerReserve = 64
)

// truncationMarker matches the wording used by truncateOutput.
func truncationMarker(dropped int64) string {
	return fmt.Sprintf("\n\n... [%d chars truncated] ...\n\n", dropped)
}

// lockedBuffer is a strings buffer that is safe to read while os/exec's copy
// goroutines are still feeding it. It retains at most maxBufferedOutput bytes:
// once a stream outgrows that, only its head and tail are kept around a
// truncation marker, so a chatty process can't grow memory without bound.
type lockedBuffer struct {
	mu sync.Mutex

	// data holds every byte while total <= maxBufferedOutput.
	data []byte
	// Once truncated, head holds the first bytes and tail the most recent
	// ones; data is released. tailCap is len(tail)'s fixed capacity.
	head      []byte
	tail      []byte
	tailCap   int
	total     int64
	truncated bool
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	if !l.truncated {
		if l.total+int64(n) <= int64(maxBufferedOutput) {
			l.data = append(l.data, p...)
			l.total += int64(n)
			return n, nil
		}
		l.truncateLocked(p)
		return n, nil
	}

	l.total += int64(n)
	l.appendTailLocked(p)
	return n, nil
}

// truncateLocked switches the buffer to head+marker+tail mode. It keeps the
// first and last (maxBufferedOutput - truncationMarkerReserve)/2 bytes of
// data+p and drops the middle, so the final string fits inside the cap no
// matter how much was written.
func (l *lockedBuffer) truncateLocked(p []byte) {
	total := l.total + int64(len(p))

	budget := maxBufferedOutput - truncationMarkerReserve
	if budget < 0 {
		budget = 0
	}
	headLen := budget / 2
	tailLen := budget - headLen
	l.tailCap = tailLen

	// Head: the first headLen bytes of data followed by p.
	l.head = make([]byte, 0, headLen)
	if headLen <= len(l.data) {
		l.head = append(l.head, l.data[:headLen]...)
	} else {
		l.head = append(l.head, l.data...)
		need := headLen - len(l.data)
		if need > len(p) {
			need = len(p)
		}
		l.head = append(l.head, p[:need]...)
	}

	// Tail: the last tailLen bytes of data followed by p.
	l.tail = make([]byte, 0, tailLen)
	if tailLen <= len(p) {
		l.tail = append(l.tail, p[len(p)-tailLen:]...)
	} else {
		need := tailLen - len(p)
		if need > len(l.data) {
			need = len(l.data)
		}
		l.tail = append(l.tail, l.data[len(l.data)-need:]...)
		l.tail = append(l.tail, p...)
	}

	l.total = total
	l.data = nil // release the unbounded prefix
	l.truncated = true
}

// appendTailLocked appends p to the rolling tail, keeping only the most recent
// tailCap bytes. p is copied so callers can reuse their slice.
func (l *lockedBuffer) appendTailLocked(p []byte) {
	if l.tailCap <= 0 {
		return
	}
	if len(p) >= l.tailCap {
		l.tail = append(l.tail[:0], p[len(p)-l.tailCap:]...)
		return
	}

	overflow := len(l.tail) + len(p) - l.tailCap
	if overflow > 0 {
		copy(l.tail, l.tail[overflow:])
		l.tail = l.tail[:len(l.tail)-overflow]
	}
	l.tail = append(l.tail, p...)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.truncated {
		return string(l.data)
	}

	dropped := l.total - int64(len(l.head)+len(l.tail))
	var b strings.Builder
	b.Grow(len(l.head) + len(l.tail) + truncationMarkerReserve)
	b.Write(l.head)
	b.WriteString(truncationMarker(dropped))
	b.Write(l.tail)
	return b.String()
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
