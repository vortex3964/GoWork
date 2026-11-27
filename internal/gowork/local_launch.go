package gowork

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"GoWork/providers"
)

const (
	defaultLocalPollInterval  = 250 * time.Millisecond
	defaultLocalStopGrace     = 5 * time.Second
	defaultLocalHealthTimeout = 2 * time.Second
	localProbeTimeout         = 1500 * time.Millisecond
	localStopCommandTimeout   = 15 * time.Second
)

// localServerStatus is the picker-facing state of one local server. It never
// carries command arguments or environment values, only a short label.
type localServerStatus struct {
	Provider  string
	Installed bool
	Running   bool
	// Managed means GoWork started the server (or its service) and is
	// responsible for stopping it. An external server is reported but never
	// killed.
	Managed bool
	// Busy is set to "starting…"/"stopping…" during a transition.
	Busy   string
	Detail string
}

func (s localServerStatus) Label() string {
	switch {
	case s.Busy != "":
		return s.Busy
	case s.Running && s.Managed:
		return "running · managed by GoWork"
	case s.Running:
		return "running · external (not started by GoWork)"
	case s.Detail != "":
		return s.Detail
	case s.Installed:
		return "not running · press s to start"
	default:
		return "not installed"
	}
}

// localServerProcess is one managed child. done is closed after cmd.Wait, so
// every waiter (startup, stop, app exit) can finish without polling or leaking
// a goroutine.
type localServerProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	log  *localServerLog

	mu      sync.Mutex
	exited  bool
	waitErr error
	service bool // ServiceCLI handed off to a daemon and is considered up
	stopped bool // service stop command already ran
}

func (p *localServerProcess) wait() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.exited = true
	p.waitErr = err
	p.mu.Unlock()
	close(p.done)
}

func (p *localServerProcess) result() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited, p.waitErr
}

func (p *localServerProcess) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.exited || p.service
}

func (p *localServerProcess) exitOK() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited && p.waitErr == nil
}

func (p *localServerProcess) markServiceActive() {
	p.mu.Lock()
	p.service = true
	p.mu.Unlock()
}

// markServiceStopped reports whether this is the first caller to stop the
// detached service, so a concurrent Stop/StopAll cannot run the CLI twice.
func (p *localServerProcess) markServiceStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return false
	}
	p.stopped = true
	return true
}

// terminate signals the whole process tree, escalating to a forced kill if the
// child ignores the graceful signal. It returns once Wait observes the exit or
// the bounded escalation window expires.
func (p *localServerProcess) terminate(grace time.Duration) {
	if exited, _ := p.result(); exited {
		return
	}

	_ = signalProcessTree(p.cmd, false)
	if grace <= 0 {
		grace = defaultLocalStopGrace
	}
	graceTimer := time.NewTimer(grace)
	defer graceTimer.Stop()
	select {
	case <-p.done:
		return
	case <-graceTimer.C:
	}

	_ = signalProcessTree(p.cmd, true)
	forceTimer := time.NewTimer(2 * time.Second)
	defer forceTimer.Stop()
	select {
	case <-p.done:
	case <-forceTimer.C:
	}
}

// localServerManager owns every local server GoWork starts. It is created once
// per app run and its Start/Stop calls are safe to use from tea.Cmd goroutines.
type localServerManager struct {
	mu       sync.Mutex
	servers  map[string]*localServerProcess
	busy     map[string]string
	external map[string]bool
	specs    map[string]providers.LocalServerSpec

	newCommand func(string, ...string) *exec.Cmd
	client     *http.Client

	startTimeout         time.Duration
	pollInterval         time.Duration
	stopGrace            time.Duration
	healthRequestTimeout time.Duration
	probeTimeout         time.Duration

	// shutdownCh is closed by StopAll. In-flight starts abort their child
	// instead of being orphaned when the TUI exits.
	shutdownCh   chan struct{}
	shuttingDown bool
	starts       sync.WaitGroup
}

func newLocalServerManager() *localServerManager {
	m := &localServerManager{
		servers:              make(map[string]*localServerProcess),
		busy:                 make(map[string]string),
		external:             make(map[string]bool),
		specs:                make(map[string]providers.LocalServerSpec),
		newCommand:           exec.Command,
		client:               &http.Client{},
		pollInterval:         defaultLocalPollInterval,
		stopGrace:            defaultLocalStopGrace,
		healthRequestTimeout: defaultLocalHealthTimeout,
		probeTimeout:         localProbeTimeout,
		shutdownCh:           make(chan struct{}),
	}
	for _, spec := range []providers.LocalServerSpec{
		providers.LlamaCppLaunchSpec(),
		providers.LMStudioLaunchSpec(),
	} {
		m.specs[providers.CanonicalLocalProviderName(spec.Provider)] = spec
	}
	return m
}

func (m *localServerManager) lookup(provider string) (string, providers.LocalServerSpec, bool) {
	key := providers.CanonicalLocalProviderName(provider)
	spec, ok := m.specs[key]
	return key, spec, ok
}

// Status returns the cached state without touching the network. It is safe to
// call from the UI thread.
func (m *localServerManager) Status(provider string) localServerStatus {
	key, spec, ok := m.lookup(provider)
	if !ok {
		return localServerStatus{Provider: provider, Detail: "start/stop is not supported"}
	}

	m.mu.Lock()
	busy := m.busy[key]
	proc := m.servers[key]
	external := m.external[key]
	m.mu.Unlock()

	_, _, resolveErr := spec.ResolveCommand()
	installed := resolveErr == nil
	st := localServerStatus{
		Provider:  spec.Provider,
		Installed: installed || external || proc != nil,
	}
	switch {
	case busy != "":
		st.Busy = busy
	case proc != nil && proc.isRunning():
		st.Running = true
		st.Managed = true
	case external:
		st.Running = true
	}

	if !st.Installed && !st.Running && st.Busy == "" {
		// Guidance only: configured keys/values are never echoed.
		st.Detail = fmt.Sprintf("not installed · set %s to the executable", spec.CommandEnv)
	}
	return st
}

// StatusLabel returns the display label for a launchable local provider, and
// false for providers without launch controls (e.g. Ollama).
func (m *localServerManager) StatusLabel(provider string) (string, bool) {
	if _, _, ok := m.lookup(provider); !ok {
		return "", false
	}
	return m.Status(provider).Label(), true
}

// Probe refreshes the external-running cache with one bounded health check.
// The picker calls it from a tea.Cmd when it opens; it never launches anything.
func (m *localServerManager) Probe(ctx context.Context, provider string) localServerStatus {
	key, spec, ok := m.lookup(provider)
	if !ok {
		return localServerStatus{Provider: provider, Detail: "start/stop is not supported"}
	}

	m.mu.Lock()
	busy := m.busy[key]
	proc := m.servers[key]
	m.mu.Unlock()
	if busy != "" || (proc != nil && proc.isRunning()) {
		return m.Status(provider)
	}

	external := m.probeExisting(ctx, spec)
	m.mu.Lock()
	if external {
		m.external[key] = true
	} else {
		delete(m.external, key)
	}
	m.mu.Unlock()
	return m.Status(provider)
}

// Start launches the provider's server if it is not already running. A server
// already answering on the health endpoint is reported as external and is left
// untouched. Readiness is bounded by the spec timeout (or the manager's test
// override).
func (m *localServerManager) Start(ctx context.Context, provider string) (localServerStatus, error) {
	key, spec, ok := m.lookup(provider)
	if !ok {
		return localServerStatus{Provider: provider, Detail: "start/stop is not supported"},
			fmt.Errorf("local server launch is not supported for %s", provider)
	}

	command, args, err := spec.ResolveCommand()
	if err != nil {
		st := localServerStatus{Provider: spec.Provider, Detail: "not installed"}
		return st, err
	}

	m.mu.Lock()
	if m.shuttingDown {
		m.mu.Unlock()
		return localServerStatus{Provider: spec.Provider, Installed: true},
			fmt.Errorf("cannot start %s: app is shutting down", spec.DisplayName)
	}
	if state := m.busy[key]; state != "" {
		m.mu.Unlock()
		return m.Status(provider), fmt.Errorf("%s is already %s", spec.DisplayName, state)
	}
	if proc := m.servers[key]; proc != nil {
		if proc.isRunning() {
			m.mu.Unlock()
			return m.Status(provider), nil
		}
		// A previous run exited on its own; drop the stale bookkeeping.
		delete(m.servers, key)
		proc.log.remove()
	}
	m.busy[key] = "starting…"
	m.starts.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, key)
		m.mu.Unlock()
		m.starts.Done()
	}()

	// Never spawn on top of a server the user already has running.
	if m.probeExisting(ctx, spec) {
		m.mu.Lock()
		m.external[key] = true
		m.mu.Unlock()
		return localServerStatus{Provider: spec.Provider, Installed: true, Running: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return localServerStatus{Provider: spec.Provider, Installed: true}, err
	}
	m.mu.Lock()
	shuttingDown := m.shuttingDown
	m.mu.Unlock()
	if shuttingDown {
		return localServerStatus{Provider: spec.Provider, Installed: true},
			fmt.Errorf("cannot start %s: app is shutting down", spec.DisplayName)
	}

	// The child gets its own process group/session so terminal signals aimed
	// at the TUI do not reach it, and its output never paints the alt screen.
	// A real file (rather than an io.Writer pipe) also means a service CLI
	// that daemonizes cannot keep cmd.Wait blocked on an inherited pipe.
	cmd := m.newCommand(command, args...)
	configureDetachedProcess(cmd)
	serverLog, logFile, err := newLocalServerLog()
	if err != nil {
		return localServerStatus{Provider: spec.Provider, Installed: true, Detail: "start failed"},
			fmt.Errorf("prepare %s log: %w", spec.DisplayName, err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		serverLog.remove()
		return localServerStatus{Provider: spec.Provider, Installed: true, Detail: "start failed"},
			fmt.Errorf("start %s: %w", spec.DisplayName, err)
	}
	// The child has its own duplicated descriptor; keeping the parent handle
	// open would only delay temp-file cleanup on Windows.
	_ = logFile.Close()

	proc := &localServerProcess{
		cmd:  cmd,
		done: make(chan struct{}),
		log:  serverLog,
	}
	m.mu.Lock()
	m.servers[key] = proc
	delete(m.external, key)
	m.mu.Unlock()
	go proc.wait()

	return m.waitForReady(ctx, key, spec, proc)
}

func (m *localServerManager) waitForReady(ctx context.Context, key string, spec providers.LocalServerSpec, proc *localServerProcess) (localServerStatus, error) {
	timeout := spec.StartTimeout
	if m.startTimeout > 0 {
		timeout = m.startTimeout
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	doneCh := proc.done
	check := func() (ready bool, exited bool) {
		probeCtx, cancel := context.WithTimeout(ctx, m.healthRequestTimeout)
		ready = m.endpointReady(probeCtx, spec)
		cancel()
		exited, waitErr := proc.result()
		if ready && exited && !(spec.ServiceCLI && waitErr == nil) {
			return false, true
		}
		return ready, exited
	}

	if ready, exited := check(); ready {
		return m.markStarted(key, spec, proc), nil
	} else if exited && !(spec.ServiceCLI && proc.exitOK()) {
		return m.failStart(key, spec, proc, fmt.Sprintf("%s exited before becoming ready", spec.DisplayName))
	}

	for {
		select {
		case <-ctx.Done():
			m.abortStart(key, spec, proc)
			return localServerStatus{Provider: spec.Provider, Installed: true, Detail: "start canceled"},
				fmt.Errorf("starting %s: %w", spec.DisplayName, ctx.Err())

		case <-m.shutdownCh:
			m.abortStart(key, spec, proc)
			return localServerStatus{Provider: spec.Provider, Installed: true, Detail: "start canceled"},
				fmt.Errorf("starting %s: app is shutting down", spec.DisplayName)

		case <-deadline.C:
			suffix := logSuffix(proc)
			m.abortStart(key, spec, proc)
			return localServerStatus{Provider: spec.Provider, Installed: true, Detail: "start timed out"},
				fmt.Errorf("timed out after %s waiting for %s to become ready%s", timeout, spec.DisplayName, suffix)

		case <-doneCh:
			_, waitErr := proc.result()
			if spec.ServiceCLI && waitErr == nil {
				// `lms server start` normally exits after handing the server
				// to its daemon. Keep polling the endpoint until the deadline.
				doneCh = nil
				continue
			}
			return m.failStart(key, spec, proc, fmt.Sprintf("%s exited before becoming ready", spec.DisplayName))

		case <-ticker.C:
			ready, exited := check()
			if ready {
				return m.markStarted(key, spec, proc), nil
			}
			if exited {
				if spec.ServiceCLI && proc.exitOK() {
					doneCh = nil
					continue
				}
				return m.failStart(key, spec, proc, fmt.Sprintf("%s exited before becoming ready", spec.DisplayName))
			}
		}
	}
}

func (m *localServerManager) markStarted(key string, spec providers.LocalServerSpec, proc *localServerProcess) localServerStatus {
	if exited, waitErr := proc.result(); exited && spec.ServiceCLI && waitErr == nil {
		proc.markServiceActive()
	}
	return localServerStatus{
		Provider:  spec.Provider,
		Installed: true,
		Running:   true,
		Managed:   true,
	}
}

func (m *localServerManager) failStart(key string, spec providers.LocalServerSpec, proc *localServerProcess, message string) (localServerStatus, error) {
	suffix := logSuffix(proc)
	m.abortStart(key, spec, proc)
	st := localServerStatus{Provider: spec.Provider, Installed: true, Detail: "start failed"}
	return st, fmt.Errorf("%s%s", message, suffix)
}

// abortStart removes a half-started server and makes sure its process (or a
// service it launched) cannot survive a failed startup.
func (m *localServerManager) abortStart(key string, spec providers.LocalServerSpec, proc *localServerProcess) {
	if proc != nil {
		exited, waitErr := proc.result()
		switch {
		case !exited:
			proc.terminate(m.stopGrace)
		case spec.ServiceCLI && waitErr == nil:
			if proc.markServiceStopped() {
				m.runStopCommand(spec)
			}
		}
	}
	m.mu.Lock()
	if proc != nil && m.servers[key] == proc {
		delete(m.servers, key)
	}
	m.mu.Unlock()
	if proc != nil {
		proc.log.remove()
	}
}

// Stop terminates a server GoWork started. It refuses to kill an external
// server; that state is reported instead.
func (m *localServerManager) Stop(ctx context.Context, provider string) (localServerStatus, error) {
	key, spec, ok := m.lookup(provider)
	if !ok {
		return localServerStatus{Provider: provider, Detail: "start/stop is not supported"},
			fmt.Errorf("local server launch is not supported for %s", provider)
	}

	m.mu.Lock()
	if state := m.busy[key]; state != "" {
		m.mu.Unlock()
		return m.Status(provider), fmt.Errorf("%s is already %s", spec.DisplayName, state)
	}
	proc := m.servers[key]
	if proc == nil {
		m.mu.Unlock()
		external := m.probeExisting(ctx, spec)
		m.mu.Lock()
		if external {
			m.external[key] = true
		} else {
			delete(m.external, key)
		}
		m.mu.Unlock()
		if external {
			return localServerStatus{Provider: spec.Provider, Installed: true, Running: true}, nil
		}
		return localServerStatus{Provider: spec.Provider, Installed: true}, nil
	}
	m.busy[key] = "stopping…"
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, key)
		m.mu.Unlock()
	}()

	m.stopProcess(spec, proc)

	m.mu.Lock()
	if m.servers[key] == proc {
		delete(m.servers, key)
	}
	delete(m.external, key)
	m.mu.Unlock()
	proc.log.remove()

	return localServerStatus{Provider: spec.Provider, Installed: true}, nil
}

// stopProcess terminates the direct child tree, or asks the service CLI to
// shut down a daemon it started and then exited.
func (m *localServerManager) stopProcess(spec providers.LocalServerSpec, proc *localServerProcess) {
	exited, waitErr := proc.result()
	if !exited {
		proc.terminate(m.stopGrace)
		return
	}
	if spec.ServiceCLI && waitErr == nil && proc.markServiceStopped() {
		m.runStopCommand(spec)
	}
}

// StopAll is the app-exit cleanup: it aborts in-flight starts, then stops every
// server GoWork launched. It is idempotent and waits for its goroutines.
func (m *localServerManager) StopAll() {
	m.mu.Lock()
	if !m.shuttingDown {
		m.shuttingDown = true
		close(m.shutdownCh)
	}
	m.mu.Unlock()
	m.starts.Wait()

	m.mu.Lock()
	keys := make([]string, 0, len(m.servers))
	for key := range m.servers {
		keys = append(keys, key)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			m.mu.Lock()
			spec, known := m.specs[key]
			proc := m.servers[key]
			m.mu.Unlock()
			if !known || proc == nil {
				return
			}
			m.stopProcess(spec, proc)
			m.mu.Lock()
			if m.servers[key] == proc {
				delete(m.servers, key)
			}
			m.mu.Unlock()
			proc.log.remove()
		}(key)
	}
	wg.Wait()
}

func (m *localServerManager) probeExisting(ctx context.Context, spec providers.LocalServerSpec) bool {
	timeout := m.probeTimeout
	if timeout <= 0 {
		timeout = localProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.endpointReady(probeCtx, spec)
}

// endpointReady reports whether any of the spec's health/model endpoints
// answers 200. The request body is discarded; only readiness matters.
func (m *localServerManager) endpointReady(ctx context.Context, spec providers.LocalServerSpec) bool {
	for _, endpoint := range spec.HealthURLs() {
		reqCtx, cancel := context.WithTimeout(ctx, m.healthRequestTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := m.client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		ready := resp.StatusCode == http.StatusOK
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		if ready {
			return true
		}
	}
	return false
}

// runStopCommand asks a service CLI (e.g. `lms server stop`) to shut down the
// daemon it launched. Failures are best-effort: app exit must not block.
func (m *localServerManager) runStopCommand(spec providers.LocalServerSpec) {
	if len(spec.StopArgs) == 0 {
		return
	}
	command, _, err := spec.ResolveCommand()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), localStopCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, spec.StopArgs...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}

// logSuffix returns a short, single-line tail of the child's output for a
// startup error. Arguments and environment variables are never included.
func logSuffix(proc *localServerProcess) string {
	if proc == nil || proc.log == nil {
		return ""
	}
	tail := strings.TrimSpace(proc.log.Tail(600))
	if tail == "" {
		return ""
	}
	lines := strings.Split(tail, "\n")
	line := strings.TrimSpace(strings.TrimSuffix(lines[len(lines)-1], "\r"))
	if len(line) > 300 {
		line = line[:300] + "…"
	}
	if line == "" {
		return ""
	}
	return ": " + line
}

// localServerLog is a temp file used for the child's stdout/stderr. Keeping it
// a real file avoids os/exec copy goroutines that a daemonizing CLI could keep
// blocked, and still lets startup errors show the last lines of output.
type localServerLog struct {
	path string
}

func newLocalServerLog() (*localServerLog, *os.File, error) {
	f, err := os.CreateTemp("", "gowork-local-server-*.log")
	if err != nil {
		return nil, nil, err
	}
	return &localServerLog{path: f.Name()}, f, nil
}

func (l *localServerLog) remove() {
	if l == nil || l.path == "" {
		return
	}
	_ = os.Remove(l.path)
}

func (l *localServerLog) Tail(max int) string {
	if l == nil || l.path == "" {
		return ""
	}
	f, err := os.Open(l.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	if max <= 0 || int64(max) > size {
		max = int(size)
	}
	if max <= 0 {
		return ""
	}
	buf := make([]byte, max)
	if _, err := f.ReadAt(buf, size-int64(max)); err != nil && err != io.EOF {
		return ""
	}
	return string(buf)
}
