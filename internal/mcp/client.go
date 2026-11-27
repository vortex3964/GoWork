package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// ClientName is sent as clientInfo.name during initialization.
	ClientName = "GoWork"

	maxToolPages = 100
)

// ClientVersion is sent as clientInfo.version. The application may override it
// at startup to match its release version.
var ClientVersion = "dev"

// Client is one connection to an MCP server. It is safe for concurrent calls
// once Start has returned; Start and Close themselves must not be called
// concurrently with each other.
type Client struct {
	cfg  ServerConfig
	name string

	mu        sync.Mutex
	started   bool
	starting  bool
	closed    bool
	nextID    int64
	pending   map[string]chan rpcResult
	transport transport

	readerStop chan struct{}
	readerDone chan struct{}
	connErr    error

	closeOnce sync.Once
	closeCh   chan struct{}
	closeErr  error

	protocolVersion string
	serverInfo      ServerInfo
	capabilities    ServerCapabilities
	instructions    string
}

// NewClient validates cfg and returns a client for it. The connection is not
// opened until Start is called.
func NewClient(cfg ServerConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = "mcp"
	}
	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: %w", cfg.Name, err)
	}
	if len(transportKinds(normalized)) == 0 {
		return nil, fmt.Errorf("mcp: server %q: no usable transport", cfg.Name)
	}
	return &Client{
		cfg:     normalized,
		name:    normalized.Name,
		pending: make(map[string]chan rpcResult),
		closeCh: make(chan struct{}),
	}, nil
}

// Name returns the configured server name.
func (c *Client) Name() string { return c.name }

// Transport returns the effective transport kind.
func (c *Client) Transport() TransportKind { return c.cfg.TransportKind() }

// Start launches or connects to the server and completes the initialize
// handshake. The supplied context bounds connection establishment; it does not
// govern the lifetime of the connection.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return ErrClosed
	case c.started:
		c.mu.Unlock()
		return ErrAlreadyStarted
	case c.starting:
		c.mu.Unlock()
		return errors.New("mcp: client start already in progress")
	}
	c.starting = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.starting = false
		c.mu.Unlock()
	}()

	kinds := transportKinds(c.cfg)
	var firstErr error

	for i, kind := range kinds {
		t, err := newTransport(kind, c.cfg)
		if err != nil {
			return fmt.Errorf("mcp %q: start: %w", c.name, err)
		}

		c.startReader(t)

		attemptCtx := ctx
		cancel := func() {}
		if timeout := c.startupTimeout(); timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		startErr := t.Start(attemptCtx)
		if startErr == nil {
			startErr = c.initialize(attemptCtx)
		}
		cancel()

		if startErr == nil {
			c.mu.Lock()
			c.started = true
			c.mu.Unlock()
			return nil
		}
		if errors.Is(startErr, context.DeadlineExceeded) {
			startErr = fmt.Errorf("%w: start after %s", ErrTimeout, c.startupTimeout())
		}

		diagnostics := transportDiagnostics(t)
		c.shutdownAttempt()

		if firstErr == nil && len(kinds) > 1 && i < len(kinds)-1 && isLegacyFallbackError(startErr) {
			firstErr = startErr
			continue
		}
		if firstErr != nil {
			msg := fmt.Errorf("mcp %q: start: streamable http failed: %v; sse fallback failed: %w", c.name, firstErr, startErr)
			if diagnostics != "" {
				return fmt.Errorf("%w (stderr: %s)", msg, truncate(diagnostics, 2048))
			}
			return msg
		}
		if diagnostics != "" {
			return fmt.Errorf("mcp %q: start: %w (stderr: %s)", c.name, startErr, truncate(diagnostics, 2048))
		}
		return fmt.Errorf("mcp %q: start: %w", c.name, startErr)
	}

	if firstErr != nil {
		return fmt.Errorf("mcp %q: start: %w", c.name, firstErr)
	}
	return fmt.Errorf("mcp %q: start: no transport available", c.name)
}

// Ping checks that the connection is still healthy. It is safe to call while
// other requests are in flight.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.ready(); err != nil {
		return err
	}
	_, err := c.call(ctx, "ping", nil, c.requestTimeout())
	return err
}

// Health is an alias for Ping, provided for callers that model health checks
// separately from protocol-level pings.
func (c *Client) Health(ctx context.Context) error { return c.Ping(ctx) }

// Healthy reports the last known connection state without doing I/O. Use Ping
// for an active check.
func (c *Client) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started && !c.closed && c.connErr == nil
}

// ListTools discovers all tools advertised by the server, following pagination
// cursors. Definitions are normalized to name, description, and input schema.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}

	var tools []Tool
	seenNames := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	cursor := ""

	for page := 0; ; page++ {
		if page >= maxToolPages {
			return nil, fmt.Errorf("mcp %q: tools/list exceeded %d pages", c.name, maxToolPages)
		}

		var params any
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}

		raw, err := c.call(ctx, "tools/list", params, c.requestTimeout())
		if err != nil {
			return nil, err
		}
		var result listToolsResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("mcp %q: decode tools/list: %w", c.name, err)
		}

		for _, tool := range result.Tools {
			normalized, err := normalizeTool(tool)
			if err != nil {
				return nil, fmt.Errorf("mcp %q: tools/list: %w", c.name, err)
			}
			if _, duplicate := seenNames[normalized.Name]; duplicate {
				return nil, fmt.Errorf("mcp %q: server listed duplicate tool %q", c.name, normalized.Name)
			}
			seenNames[normalized.Name] = struct{}{}
			tools = append(tools, normalized)
		}

		if result.NextCursor == "" {
			break
		}
		if _, duplicate := seenCursors[result.NextCursor]; duplicate {
			return nil, fmt.Errorf("mcp %q: tools/list repeated pagination cursor %q", c.name, result.NextCursor)
		}
		seenCursors[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}

	if tools == nil {
		tools = []Tool{}
	}
	return tools, nil
}

// CallTool invokes a tool and normalizes its content blocks to text. Tool
// execution failures are returned in CallResult.IsError with a nil error;
// transport and protocol failures are returned as errors.
func (c *Client) CallTool(ctx context.Context, name string, arguments any) (*CallResult, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("mcp: tool name must not be empty")
	}

	params := struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments,omitempty"`
	}{
		Name:      name,
		Arguments: arguments,
	}

	raw, err := c.call(ctx, "tools/call", params, c.requestTimeout())
	if err != nil {
		return nil, err
	}
	var result callToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp %q: decode tools/call for %q: %w", c.name, name, err)
	}

	text := normalizeContent(result.Content)
	structured := bytes.TrimSpace(result.StructuredContent)
	if text == "" && len(structured) > 0 && !bytes.Equal(structured, []byte("null")) {
		text = string(structured)
	}

	return &CallResult{
		Content:           result.Content,
		Text:              text,
		IsError:           result.IsError,
		StructuredContent: result.StructuredContent,
	}, nil
}

// ProtocolVersion returns the revision negotiated during initialize.
func (c *Client) ProtocolVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.protocolVersion
}

// ServerInfo returns the identity reported by the server.
func (c *Client) ServerInfo() ServerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverInfo
}

// Capabilities returns the server capabilities captured at initialize time.
func (c *Client) Capabilities() ServerCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capabilities
}

// Instructions returns the optional usage instructions supplied by the server.
func (c *Client) Instructions() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructions
}

// Close shuts the connection down and releases the server process, if any. It
// is idempotent and safe to call after a failed Start.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		t := c.transport
		stop := c.readerStop
		done := c.readerDone
		c.readerStop = nil
		c.readerDone = nil
		c.mu.Unlock()

		close(c.closeCh)
		if stop != nil {
			close(stop)
		}

		if t != nil {
			if err := t.Close(); err != nil {
				c.closeErr = err
			}
		}
		if done != nil {
			timeout := c.shutdownTimeout()
			select {
			case <-done:
			case <-time.After(timeout):
				if c.closeErr == nil {
					c.closeErr = errors.New("mcp: timed out waiting for client reader to stop")
				}
			}
		}

		c.failPending(ErrClosed)

		c.mu.Lock()
		c.started = false
		c.transport = nil
		c.mu.Unlock()
	})
	return c.closeErr
}

func (c *Client) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: PreferredProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      ClientInfo{Name: ClientName, Version: ClientVersion},
	}
	raw, err := c.call(ctx, "initialize", params, 0)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	if result.ProtocolVersion == "" {
		return errors.New("initialize result is missing protocolVersion")
	}
	if !isSupportedProtocolVersion(result.ProtocolVersion) {
		return fmt.Errorf("%w: server chose %q, supported: %s", ErrUnsupportedProtocolVersion, result.ProtocolVersion, strings.Join(supportedProtocolVersions, ", "))
	}

	c.mu.Lock()
	t := c.transport
	c.mu.Unlock()
	if t != nil {
		t.SetProtocolVersion(result.ProtocolVersion)
	}

	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}

	c.mu.Lock()
	c.protocolVersion = result.ProtocolVersion
	c.serverInfo = result.ServerInfo
	c.capabilities = result.Capabilities
	c.instructions = result.Instructions
	c.mu.Unlock()
	return nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	payload, err := json.Marshal(rpcNotification{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}

	c.mu.Lock()
	t := c.transport
	closed := c.closed
	c.mu.Unlock()
	if closed || t == nil {
		return ErrClosed
	}
	if err := t.Send(ctx, payload); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if c.transport == nil {
		c.mu.Unlock()
		return nil, ErrNotStarted
	}
	if c.connErr != nil {
		err := c.connErr
		c.mu.Unlock()
		return nil, err
	}

	c.nextID++
	id := c.nextID
	key := fmt.Sprintf("n:%d", id)
	ch := make(chan rpcResult, 1)
	c.pending[key] = ch
	t := c.transport
	closeCh := c.closeCh
	readerDone := c.readerDone
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	payload, err := json.Marshal(rpcRequest{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp %q: encode %s: %w", c.name, method, err)
	}

	if err := t.Send(ctx, payload); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, c.contextError(method, ctxErr, timeout)
		}
		if errors.Is(err, ErrClosed) || c.isClosed() {
			return nil, ErrClosed
		}
		return nil, fmt.Errorf("mcp %q: %s: %w", c.name, method, err)
	}

	select {
	case result := <-ch:
		if result.err != nil {
			return nil, result.err
		}
		if result.msg == nil {
			return nil, fmt.Errorf("mcp %q: %s: empty response", c.name, method)
		}
		if result.msg.Error != nil {
			return nil, fmt.Errorf("mcp %q: %s: %w", c.name, method, result.msg.Error)
		}
		return result.msg.Result, nil

	case <-ctx.Done():
		return nil, c.contextError(method, ctx.Err(), timeout)

	case <-closeCh:
		return nil, c.closedError()

	case <-readerDone:
		return nil, c.connectionError()
	}
}

func (c *Client) contextError(method string, err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) {
		if timeout > 0 {
			return fmt.Errorf("%w: %s after %s", ErrTimeout, method, timeout)
		}
		return fmt.Errorf("%w: %s", ErrTimeout, method)
	}
	return fmt.Errorf("mcp %q: %s: %w", c.name, method, err)
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Client) ready() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if !c.started {
		return ErrNotStarted
	}
	if c.connErr != nil {
		return c.connErr
	}
	return nil
}

func (c *Client) closedError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connErr != nil {
		return c.connErr
	}
	return ErrClosed
}

func (c *Client) connectionError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connErr != nil {
		return c.connErr
	}
	return fmt.Errorf("mcp %q: connection closed", c.name)
}

func (c *Client) startReader(t transport) {
	stop := make(chan struct{})
	done := make(chan struct{})

	c.mu.Lock()
	c.transport = t
	c.connErr = nil
	c.readerStop = stop
	c.readerDone = done
	c.mu.Unlock()

	go c.readLoop(t, stop, done)
}

func (c *Client) readLoop(t transport, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	for {
		select {
		case <-stop:
			return
		case <-c.closeCh:
			return
		case raw, ok := <-t.Messages():
			if !ok {
				c.readerFailed(connectionClosedError(t))
				return
			}
			if err := c.handleMessage(raw); err != nil {
				c.readerFailed(fmt.Errorf("mcp %q: protocol error: %w", c.name, err))
				go func() { _ = t.Close() }()
				return
			}
		case <-t.Done():
			c.readerFailed(connectionClosedError(t))
			return
		}
	}
}

func connectionClosedError(t transport) error {
	if err := t.Err(); err != nil {
		return err
	}
	return errors.New("mcp: connection closed unexpectedly")
}

func (c *Client) readerFailed(err error) {
	if err == nil {
		err = errors.New("mcp: connection closed unexpectedly")
	}
	c.mu.Lock()
	if c.connErr == nil {
		c.connErr = err
	}
	c.mu.Unlock()
	c.failPending(err)
}

func (c *Client) handleMessage(raw []byte) error {
	msg, err := decodeRPCMessage(raw)
	if err != nil {
		return err
	}

	if msg.Method != "" {
		if _, isRequest := idKey(msg.ID); !isRequest {
			// Server notification (for example tools/list_changed). This
			// foundation does not subscribe to any, so ignoring is correct.
			return nil
		}
		return c.handleServerRequest(msg.ID, msg.Method)
	}

	key, ok := idKey(msg.ID)
	if !ok {
		return errors.New("response is missing an id")
	}

	c.mu.Lock()
	ch, found := c.pending[key]
	if found {
		delete(c.pending, key)
	}
	c.mu.Unlock()
	if !found {
		// Late response for a timed-out request; drop it.
		return nil
	}
	ch <- rpcResult{msg: msg}
	return nil
}

func (c *Client) handleServerRequest(id json.RawMessage, method string) error {
	var result any
	var rpcErr *RPCError
	switch method {
	case "ping":
		result = map[string]any{}
	default:
		rpcErr = &RPCError{
			Code:    CodeMethodNotFound,
			Message: "method not found: " + method,
		}
	}

	payload, err := json.Marshal(rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
		Error:   rpcErr,
	})
	if err != nil {
		return fmt.Errorf("encode response to %s: %w", method, err)
	}

	c.mu.Lock()
	t := c.transport
	closed := c.closed
	c.mu.Unlock()
	if closed || t == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.requestTimeout())
	defer cancel()
	if err := t.Send(ctx, payload); err != nil {
		if errors.Is(err, ErrClosed) || errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("respond to server request %s: %w", method, err)
	}
	return nil
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan rpcResult)
	c.mu.Unlock()

	for _, ch := range pending {
		select {
		case ch <- rpcResult{err: err}:
		default:
		}
	}
}

func (c *Client) shutdownAttempt() {
	c.mu.Lock()
	stop := c.readerStop
	done := c.readerDone
	t := c.transport
	c.readerStop = nil
	c.readerDone = nil
	c.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if t != nil {
		_ = t.Close()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(c.shutdownTimeout()):
		}
	}

	c.mu.Lock()
	c.transport = nil
	c.connErr = nil
	c.mu.Unlock()
	c.failPending(ErrClosed)
}

func (c *Client) startupTimeout() time.Duration {
	if d := c.cfg.StartupTimeout.Duration(); d > 0 {
		return d
	}
	return DefaultStartupTimeout
}

func (c *Client) requestTimeout() time.Duration {
	if d := c.cfg.RequestTimeout.Duration(); d > 0 {
		return d
	}
	return DefaultRequestTimeout
}

func (c *Client) shutdownTimeout() time.Duration {
	if d := c.cfg.ShutdownTimeout.Duration(); d > 0 {
		return d
	}
	return DefaultShutdownTimeout
}

func transportKinds(cfg ServerConfig) []TransportKind {
	switch cfg.TransportKind() {
	case TransportStdio:
		return []TransportKind{TransportStdio}
	case TransportHTTP:
		return []TransportKind{TransportHTTP}
	case TransportSSE:
		return []TransportKind{TransportSSE}
	case TransportAuto:
		if cfg.Command != "" {
			return []TransportKind{TransportStdio}
		}
		return []TransportKind{TransportHTTP, TransportSSE}
	default:
		return nil
	}
}

func newTransport(kind TransportKind, cfg ServerConfig) (transport, error) {
	switch kind {
	case TransportStdio:
		return newStdioTransport(cfg), nil
	case TransportHTTP:
		return newStreamableHTTPTransport(cfg), nil
	case TransportSSE:
		return newSSEHTTPTransport(cfg), nil
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q", kind)
	}
}

func transportDiagnostics(t transport) string {
	if d, ok := t.(interface{ Diagnostics() string }); ok {
		return d.Diagnostics()
	}
	return ""
}

func normalizeTool(tool Tool) (Tool, error) {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return Tool{}, errors.New("tool is missing a name")
	}

	schema := bytes.TrimSpace(tool.InputSchema)
	if len(schema) == 0 || bytes.Equal(schema, []byte("null")) {
		schema = []byte(`{"type":"object"}`)
	}
	if !json.Valid(schema) {
		return Tool{}, fmt.Errorf("tool %q has invalid inputSchema JSON", name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(schema, &object); err != nil || object == nil {
		return Tool{}, fmt.Errorf("tool %q inputSchema must be a JSON object", name)
	}

	return Tool{
		Name:        name,
		Title:       strings.TrimSpace(tool.Title),
		Description: strings.TrimSpace(tool.Description),
		InputSchema: append(json.RawMessage(nil), schema...),
	}, nil
}
