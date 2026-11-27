package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultRequestTimeout bounds every JSON-RPC request, even when the
	// caller passes a context without a deadline.
	DefaultRequestTimeout = 10 * time.Second
	// defaultShutdownTimeout bounds how long Shutdown/Close wait for the
	// server process to exit before killing it.
	defaultShutdownTimeout = 2 * time.Second
	// maxHeaderBytes and maxMessageBytes keep a broken or hostile server from
	// exhausting memory while framing messages.
	maxHeaderBytes  = 64 << 10
	maxMessageBytes = 32 << 20
)

var (
	// ErrClientClosed is returned once the client has been shut down.
	ErrClientClosed = errors.New("lsp: client closed")
	// ErrServerClosed is returned when the server's stdout reaches EOF.
	ErrServerClosed = errors.New("lsp: server closed the connection")
	// ErrAlreadyInitialized is returned by a second Initialize call.
	ErrAlreadyInitialized = errors.New("lsp: client already initialized")
)

// Options configures a stdio language-server process.
type Options struct {
	// Command is the server executable (resolved through PATH when relative).
	Command string
	// Args are passed verbatim to the server.
	Args []string
	// Env adds or overrides environment variables for the child process.
	Env map[string]string
	// RequestTimeout bounds each JSON-RPC request. Zero uses
	// DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Stderr receives the server's stderr. Nil discards it.
	Stderr io.Writer
	// OnDiagnostics, when set, is called from the client's read loop for every
	// textDocument/publishDiagnostics notification. It must return quickly and
	// must not call back into the client.
	OnDiagnostics func(PublishDiagnosticsParams)
}

// Client is a minimal stdio JSON-RPC LSP client. It owns one server process
// and one read-loop goroutine; call Shutdown or Close to release both.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	requestTimeout time.Duration
	onDiagnostics  func(PublishDiagnosticsParams)

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse
	termErr   error

	diagMu      sync.RWMutex
	diagnostics map[string][]Diagnostic

	loopDone chan struct{}

	initMu      sync.Mutex
	initialized bool

	closed     atomic.Bool
	killed     atomic.Bool
	finishOnce sync.Once
	finishErr  error

	closeOnce    sync.Once
	shutdownOnce sync.Once
	shutdownErr  error
}

// Start launches the language server and begins reading its JSON-RPC stream.
// Initialize must be called before document methods.
func Start(opts Options) (*Client, error) {
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		return nil, fmt.Errorf("lsp: command is required")
	}
	timeout := opts.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	cmd := exec.Command(command, opts.Args...)
	cmd.Env = mergedEnv(opts.Env)
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = io.Discard
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: open stdin for %q: %w", command, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("lsp: open stdout for %q: %w", command, err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("lsp: start %q: %w", command, err)
	}

	c := &Client{
		cmd:            cmd,
		stdin:          stdin,
		stdout:         stdout,
		requestTimeout: timeout,
		onDiagnostics:  opts.OnDiagnostics,
		pending:        make(map[int64]chan rpcResponse),
		diagnostics:    make(map[string][]Diagnostic),
		loopDone:       make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func mergedEnv(overrides map[string]string) []string {
	env := os.Environ()
	if len(overrides) == 0 {
		return env
	}
	// Append overrides last: on Unix the last duplicate value wins, and on
	// Windows exec normalises duplicate keys case-insensitively.
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

// Done is closed when the read loop has stopped (usually after the server
// process exits or the client is closed).
func (c *Client) Done() <-chan struct{} {
	if c == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.loopDone
}

// Initialized reports whether Initialize completed successfully.
func (c *Client) Initialized() bool {
	if c == nil {
		return false
	}
	c.initMu.Lock()
	defer c.initMu.Unlock()
	return c.initialized
}

// Initialize performs the LSP handshake: an initialize request followed by the
// initialized notification. rootPath is the project root; it is resolved to a
// file:// URI.
func (c *Client) Initialize(ctx context.Context, rootPath string) (*InitializeResult, error) {
	if c == nil {
		return nil, ErrClientClosed
	}
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.initialized {
		return nil, ErrAlreadyInitialized
	}

	params := InitializeParams{
		ProcessID: os.Getpid(),
		ClientInfo: &ClientInfo{
			Name: "GoWork",
		},
		Capabilities: ClientCapabilities{
			General: &GeneralClientCapabilities{PositionEncodings: []string{"utf-16"}},
			TextDocument: &TextDocumentClientCapabilities{
				Synchronization: &TextDocumentSyncClientCapabilities{
					DynamicRegistration: false,
				},
				PublishDiagnostics: &PublishDiagnosticsClientCapabilities{
					RelatedInformation: true,
					VersionSupport:     true,
				},
				DocumentSymbol: &DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: true,
				},
				Definition: &DefinitionClientCapabilities{
					LinkSupport: true,
				},
			},
		},
	}

	if rootPath != "" {
		abs, err := filepath.Abs(rootPath)
		if err != nil {
			return nil, fmt.Errorf("lsp: resolve root %q: %w", rootPath, err)
		}
		params.RootPath = abs
		params.RootURI = PathToURI(abs)
		params.WorkspaceFolders = []WorkspaceFolder{{
			URI:  params.RootURI,
			Name: filepath.Base(abs),
		}}
	}

	raw, err := c.request(ctx, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("lsp: initialize: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("lsp: initialize returned an empty result")
	}

	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("lsp: decode initialize result: %w", err)
	}
	if err := c.notify("initialized", struct{}{}); err != nil {
		return nil, fmt.Errorf("lsp: initialized notification: %w", err)
	}
	c.initialized = true
	return &result, nil
}

// DidOpen tells the server that a document was opened with the given LSP
// languageId and full text.
func (c *Client) DidOpen(ctx context.Context, path, languageID, text string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	params := DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        documentURI(path),
			LanguageID: languageID,
			Version:    1,
			Text:       text,
		},
	}
	if err := c.notify("textDocument/didOpen", params); err != nil {
		return fmt.Errorf("lsp: didOpen %s: %w", path, err)
	}
	return nil
}

// DocumentSymbols requests textDocument/documentSymbol for path. Servers may
// answer with hierarchical DocumentSymbols or flat SymbolInformation entries;
// both decode into DocumentSymbol, the latter with Location set instead of
// Range.
func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
	raw, err := c.request(ctx, "textDocument/documentSymbol", DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: documentURI(path)},
	})
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var symbols []DocumentSymbol
	if err := json.Unmarshal(trimmed, &symbols); err != nil {
		return nil, fmt.Errorf("lsp: decode document symbols: %w", err)
	}
	return symbols, nil
}

// Definition requests textDocument/definition at a zero-based line/character
// position and normalises Location, Location[], and LocationLink[] results to
// a slice of Locations.
func (c *Client) Definition(ctx context.Context, path string, line, character int) ([]Location, error) {
	if line < 0 || character < 0 {
		return nil, fmt.Errorf("lsp: definition needs a non-negative position, got %d:%d", line, character)
	}
	raw, err := c.request(ctx, "textDocument/definition", DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: documentURI(path)},
		Position:     Position{Line: line, Character: character},
	})
	if err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

func decodeLocations(raw json.RawMessage) ([]Location, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	switch trimmed[0] {
	case '[':
		var locations []Location
		if err := json.Unmarshal(trimmed, &locations); err != nil {
			return nil, fmt.Errorf("lsp: decode definition locations: %w", err)
		}
		if len(locations) == 0 || locations[0].URI != "" {
			return locations, nil
		}
		var links []LocationLink
		if err := json.Unmarshal(trimmed, &links); err != nil {
			return nil, fmt.Errorf("lsp: decode definition links: %w", err)
		}
		return linksToLocations(links), nil
	case '{':
		var location Location
		if err := json.Unmarshal(trimmed, &location); err != nil {
			return nil, fmt.Errorf("lsp: decode definition location: %w", err)
		}
		if location.URI != "" {
			return []Location{location}, nil
		}
		var link LocationLink
		if err := json.Unmarshal(trimmed, &link); err != nil {
			return nil, fmt.Errorf("lsp: decode definition link: %w", err)
		}
		if link.TargetURI == "" {
			return nil, fmt.Errorf("lsp: definition returned an unrecognised location")
		}
		return linksToLocations([]LocationLink{link}), nil
	default:
		return nil, fmt.Errorf("lsp: definition returned unexpected JSON %s", trimmed)
	}
}

func linksToLocations(links []LocationLink) []Location {
	out := make([]Location, 0, len(links))
	for _, link := range links {
		if link.TargetURI == "" {
			continue
		}
		rng := link.TargetRange
		if link.TargetSelectionRange != nil {
			rng = *link.TargetSelectionRange
		}
		out = append(out, Location{URI: link.TargetURI, Range: rng})
	}
	return out
}

// Diagnostics returns the last diagnostics published for path (or a file://
// URI), in a defensive copy. A server with no published diagnostics returns
// nil.
func (c *Client) Diagnostics(pathOrURI string) []Diagnostic {
	if c == nil {
		return nil
	}
	key := canonicalURI(pathOrURI)
	c.diagMu.RLock()
	defer c.diagMu.RUnlock()
	return cloneDiagnostics(c.diagnostics[key])
}

// Shutdown performs the LSP shutdown request followed by the exit
// notification, then waits briefly for the process before killing it. It is
// idempotent; Close afterwards is safe.
func (c *Client) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.shutdownOnce.Do(func() {
		c.shutdownErr = c.gracefulShutdown(ctx)
	})
	return c.shutdownErr
}

func (c *Client) gracefulShutdown(ctx context.Context) error {
	if c.closed.Load() {
		return nil
	}
	var rpcErr error
	if err := c.terminalError(); err == nil {
		if _, err := c.request(ctx, "shutdown", nil); err != nil {
			rpcErr = fmt.Errorf("lsp: shutdown: %w", err)
		}
	}

	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	if !c.waitLoop(defaultShutdownTimeout) {
		c.killed.Store(true)
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		if !c.waitLoop(defaultShutdownTimeout) {
			_ = c.stdout.Close()
			c.waitLoop(500 * time.Millisecond)
		}
	}
	_ = c.finishClient()
	return rpcErr
}

// Close force-stops the server if it is still running and reaps the process.
// It is safe to call multiple times and after Shutdown.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.closed.Load() {
			return
		}
		c.killed.Store(true)
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		_ = c.stdin.Close()
		if !c.waitLoop(defaultShutdownTimeout) {
			_ = c.stdout.Close()
			c.waitLoop(500 * time.Millisecond)
		}
		_ = c.finishClient()
	})
	return c.finishClient()
}

func (c *Client) finishClient() error {
	c.finishOnce.Do(func() {
		_ = c.stdout.Close()
		c.failAll(ErrClientClosed)
		c.closed.Store(true)
		err := c.cmd.Wait()
		if err != nil && c.killed.Load() {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				err = nil
			}
		}
		c.finishErr = err
	})
	return c.finishErr
}

func (c *Client) waitLoop(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.loopDone:
		return true
	case <-timer.C:
		return false
	}
}

func (c *Client) readLoop() {
	defer close(c.loopDone)
	defer func() {
		if r := recover(); r != nil {
			c.failAll(fmt.Errorf("lsp: read loop panic: %v", r))
		}
	}()

	reader := bufio.NewReaderSize(c.stdout, 64*1024)
	for {
		body, err := readMessage(reader)
		if err != nil {
			switch {
			case errors.Is(err, io.EOF), errors.Is(err, os.ErrClosed):
				c.failAll(ErrServerClosed)
			default:
				c.failAll(fmt.Errorf("lsp: read: %w", err))
			}
			return
		}
		c.handleMessage(body)
	}
}

func (c *Client) handleMessage(body []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return
	}

	if msg.Method == "textDocument/publishDiagnostics" {
		c.captureDiagnostics(msg.Params)
		return
	}

	if len(msg.ID) > 0 && msg.Method != "" {
		// Server-to-client request (registration, progress, configuration).
		// Replying null satisfies the protocol without implementing every
		// optional capability.
		_ = c.send(rpcOutgoingResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  nil,
		})
		return
	}

	if len(msg.ID) > 0 {
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			return
		}
		resp := rpcResponse{
			result: msg.Result,
			rpcErr: msg.Error,
		}
		c.pendingMu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.pendingMu.Unlock()
		if ch != nil {
			select {
			case ch <- resp:
			default:
			}
		}
	}
}

func (c *Client) captureDiagnostics(raw json.RawMessage) {
	var params PublishDiagnosticsParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	key := canonicalURI(params.URI)
	c.diagMu.Lock()
	c.diagnostics[key] = cloneDiagnostics(params.Diagnostics)
	c.diagMu.Unlock()

	if c.onDiagnostics != nil {
		func() {
			defer func() { _ = recover() }()
			c.onDiagnostics(params)
		}()
	}
}

func cloneDiagnostics(in []Diagnostic) []Diagnostic {
	if len(in) == 0 {
		return nil
	}
	out := make([]Diagnostic, len(in))
	copy(out, in)
	for i := range out {
		if len(out[i].Code) > 0 {
			out[i].Code = append(json.RawMessage(nil), out[i].Code...)
		}
		if len(out[i].Tags) > 0 {
			out[i].Tags = append([]int(nil), out[i].Tags...)
		}
	}
	return out
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil {
		return nil, ErrClientClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.terminalError(); err != nil {
		return nil, err
	}

	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)

	c.pendingMu.Lock()
	if c.termErr != nil {
		err := c.termErr
		c.pendingMu.Unlock()
		return nil, err
	}
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.send(rpcOutgoingRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		c.dropPending(id)
		return nil, fmt.Errorf("lsp: send %s: %w", method, err)
	}

	rctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	select {
	case resp := <-ch:
		if resp.err != nil {
			return nil, resp.err
		}
		if resp.rpcErr != nil {
			return nil, resp.rpcErr
		}
		return resp.result, nil
	case <-rctx.Done():
		c.dropPending(id)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("lsp: %s timed out after %s", method, c.requestTimeout)
	case <-c.loopDone:
		c.dropPending(id)
		if err := c.terminalError(); err != nil {
			return nil, err
		}
		return nil, ErrServerClosed
	}
}

func (c *Client) notify(method string, params any) error {
	if c == nil {
		return ErrClientClosed
	}
	if err := c.terminalError(); err != nil {
		return err
	}
	if err := c.send(rpcOutgoingNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}); err != nil {
		return fmt.Errorf("lsp: send %s: %w", method, err)
	}
	return nil
}

func (c *Client) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("lsp: marshal message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed.Load() {
		return ErrClientClosed
	}
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

func (c *Client) dropPending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) terminalError() error {
	if c.closed.Load() {
		return ErrClientClosed
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return c.termErr
}

func (c *Client) failAll(err error) {
	if err == nil {
		err = ErrClientClosed
	}
	c.pendingMu.Lock()
	if c.termErr == nil {
		c.termErr = err
	}
	channels := make([]chan rpcResponse, 0, len(c.pending))
	for id, ch := range c.pending {
		channels = append(channels, ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()

	for _, ch := range channels {
		select {
		case ch <- rpcResponse{err: err}:
		default:
		}
	}
}

// readMessage reads one Content-Length framed JSON-RPC message.
func readMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	headerBytes := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		headerBytes += len(line)
		if headerBytes > maxHeaderBytes {
			return nil, fmt.Errorf("lsp: message headers exceed %d bytes", maxHeaderBytes)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("lsp: invalid Content-Length %q", value)
		}
		if n > maxMessageBytes {
			return nil, fmt.Errorf("lsp: message length %d exceeds %d", n, maxMessageBytes)
		}
		contentLength = n
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("lsp: message missing Content-Length")
	}
	if contentLength == 0 {
		return nil, fmt.Errorf("lsp: message has an empty body")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// PathToURI converts an absolute or relative filesystem path to a file:// URI.
func PathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

func documentURI(pathOrURI string) string {
	if strings.HasPrefix(pathOrURI, "file://") {
		return pathOrURI
	}
	return PathToURI(pathOrURI)
}

func canonicalURI(pathOrURI string) string {
	if !strings.HasPrefix(pathOrURI, "file://") {
		return PathToURI(pathOrURI)
	}
	u, err := url.Parse(pathOrURI)
	if err != nil {
		return pathOrURI
	}
	path := u.Path
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
		path = path[1:]
	}
	return PathToURI(path)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
