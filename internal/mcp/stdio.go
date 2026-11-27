package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxStdioMessage = 32 << 20

// stdioTransport runs an MCP server as a child process and exchanges
// newline-delimited JSON on its stdin/stdout. Server stderr is captured in a
// bounded tail for diagnostics instead of being mixed into the protocol.
type stdioTransport struct {
	cfg ServerConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer

	messages   chan []byte
	done       chan struct{}
	readerDone chan struct{}
	waitDone   chan struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
	closed    atomic.Bool
	started   atomic.Bool
	closeErr  error

	errMu   sync.Mutex
	err     error
	errOnce sync.Once
}

func newStdioTransport(cfg ServerConfig) *stdioTransport {
	return &stdioTransport{
		cfg:        cfg,
		stderr:     newTailBuffer(4096),
		messages:   make(chan []byte, 64),
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
		waitDone:   make(chan struct{}),
	}
}

func (t *stdioTransport) Start(ctx context.Context) error {
	if t.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.cfg.Command == "" {
		return errors.New("stdio transport requires a command")
	}

	cmd := exec.Command(t.cfg.Command, t.cfg.Args...)
	cmd.Env = mergeEnv(os.Environ(), t.cfg.Env)
	cmd.Stderr = t.stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start server %q: %w", t.cfg.Command, err)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.started.Store(true)

	go t.readLoop()
	go t.waitLoop()
	return nil
}

func (t *stdioTransport) readLoop() {
	defer close(t.readerDone)
	defer close(t.messages)

	scanner := bufio.NewScanner(t.stdout)
	scanner.Buffer(make([]byte, 64*1024), maxStdioMessage)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		message := append([]byte(nil), line...)
		select {
		case t.messages <- message:
		case <-t.done:
			return
		}
	}

	if err := scanner.Err(); err != nil && !t.closed.Load() {
		t.setErr(fmt.Errorf("read server stdout: %w", err))
		return
	}
	if t.closed.Load() {
		return
	}

	// stdout closed before shutdown was requested. Give cmd.Wait a moment to
	// report the exit status, then surface a clear connection error.
	select {
	case <-t.waitDone:
	case <-time.After(100 * time.Millisecond):
	}
	if t.closed.Load() {
		return
	}
	if t.Err() == nil {
		t.setErr(errors.New("server process closed stdout unexpectedly"))
	}
}

func (t *stdioTransport) waitLoop() {
	err := t.cmd.Wait()
	if !t.closed.Load() {
		if err != nil {
			exitErr := fmt.Errorf("server process %q exited: %w", t.cfg.Command, err)
			if stderr := strings.TrimSpace(t.stderr.String()); stderr != "" {
				exitErr = fmt.Errorf("%w (stderr: %s)", exitErr, truncate(stderr, 2048))
			}
			t.setErr(exitErr)
		} else {
			t.setErr(errors.New("server process exited unexpectedly"))
		}
	}
	close(t.waitDone)
}

func (t *stdioTransport) Send(ctx context.Context, message []byte) error {
	if t.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.closed.Load() {
		return ErrClosed
	}
	if t.stdin == nil {
		return ErrNotStarted
	}

	// A child that stops reading stdin must not block the caller forever.
	// Pipes support write deadlines; when the platform does not, Close still
	// unblocks the write by closing stdin.
	if deadline, ok := ctx.Deadline(); ok {
		if setter, ok := t.stdin.(interface{ SetWriteDeadline(time.Time) error }); ok {
			if err := setter.SetWriteDeadline(deadline); err == nil {
				defer func() { _ = setter.SetWriteDeadline(time.Time{}) }()
			}
		}
	}

	frame := make([]byte, 0, len(message)+1)
	frame = append(frame, message...)
	frame = append(frame, '\n')
	if err := writeFull(t.stdin, frame); err != nil {
		if t.closed.Load() {
			return ErrClosed
		}
		var wrapped error
		if errors.Is(err, os.ErrDeadlineExceeded) {
			wrapped = fmt.Errorf("%w: write server stdin: %v", ErrTimeout, err)
		} else {
			wrapped = fmt.Errorf("write server stdin: %w", err)
		}
		t.setErr(wrapped)
		return wrapped
	}
	return nil
}

func (t *stdioTransport) Messages() <-chan []byte { return t.messages }
func (t *stdioTransport) Done() <-chan struct{}   { return t.done }

func (t *stdioTransport) Err() error {
	t.errMu.Lock()
	defer t.errMu.Unlock()
	return t.err
}

func (t *stdioTransport) setErr(err error) {
	t.errOnce.Do(func() {
		t.errMu.Lock()
		t.err = err
		t.errMu.Unlock()
		close(t.done)
	})
}

func (t *stdioTransport) SetProtocolVersion(string) {}

func (t *stdioTransport) Diagnostics() string {
	return strings.TrimSpace(t.stderr.String())
}

func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		// Closing done releases the read loop if it is blocked delivering a
		// message to a client that has already stopped reading.
		t.setErr(nil)

		if t.stdin != nil {
			_ = t.stdin.Close()
		}

		timeout := t.cfg.ShutdownTimeout.Duration()
		if timeout <= 0 {
			timeout = DefaultShutdownTimeout
		}

		if t.cmd != nil && t.cmd.Process != nil {
			// Ask politely first; servers commonly exit as soon as stdin closes.
			_ = t.cmd.Process.Signal(os.Interrupt)
			select {
			case <-t.waitDone:
			case <-time.After(timeout):
				if err := t.cmd.Process.Kill(); err != nil {
					t.closeErr = fmt.Errorf("kill server process: %w", err)
				}
				select {
				case <-t.waitDone:
				case <-time.After(time.Second):
					if t.closeErr == nil {
						t.closeErr = errors.New("timed out waiting for server process to exit")
					}
				}
			}
		}

		if t.stdout != nil {
			_ = t.stdout.Close()
		}
		if t.started.Load() {
			select {
			case <-t.readerDone:
			case <-time.After(time.Second):
				if t.closeErr == nil {
					t.closeErr = errors.New("timed out waiting for server output reader to stop")
				}
			}
		}
	})
	return t.closeErr
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func mergeEnv(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	for key, value := range overrides {
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// tailBuffer keeps the last max bytes written to it. It is used only for
// diagnostics, so writes never fail.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return len(p), nil
	}
	if overflow := len(b.buf) + len(p) - b.max; overflow > 0 {
		b.buf = append(b.buf[:0], b.buf[overflow:]...)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
