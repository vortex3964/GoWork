package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxHTTPJSONBody  = 32 << 20
	maxHTTPErrorBody = 4 << 10
)

var errStopSSE = errors.New("mcp: sse response complete")

// httpBase contains the state shared by the Streamable HTTP and legacy
// HTTP+SSE transports.
type httpBase struct {
	cfg    ServerConfig
	client *http.Client
	rt     *http.Transport

	messages chan []byte
	done     chan struct{}
	lifetime context.Context
	cancel   context.CancelFunc

	closed atomic.Bool

	errMu   sync.Mutex
	err     error
	errOnce sync.Once

	sessionMu       sync.RWMutex
	sessionID       string
	protocolVersion string
}

func newHTTPBase(cfg ServerConfig) *httpBase {
	lifetime, cancel := context.WithCancel(context.Background())
	client := &http.Client{}
	var rt *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		rt = base.Clone()
	} else {
		rt = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	client.Transport = rt
	return &httpBase{
		cfg:      cfg,
		client:   client,
		rt:       rt,
		messages: make(chan []byte, 64),
		done:     make(chan struct{}),
		lifetime: lifetime,
		cancel:   cancel,
	}
}

func (b *httpBase) Messages() <-chan []byte { return b.messages }
func (b *httpBase) Done() <-chan struct{}   { return b.done }

func (b *httpBase) Err() error {
	b.errMu.Lock()
	defer b.errMu.Unlock()
	return b.err
}

func (b *httpBase) setErr(err error) {
	b.errOnce.Do(func() {
		b.errMu.Lock()
		b.err = err
		b.errMu.Unlock()
		close(b.done)
	})
}

func (b *httpBase) SetProtocolVersion(version string) {
	b.sessionMu.Lock()
	b.protocolVersion = version
	b.sessionMu.Unlock()
}

func (b *httpBase) setSessionID(id string) {
	if id == "" {
		return
	}
	b.sessionMu.Lock()
	b.sessionID = id
	b.sessionMu.Unlock()
}

func (b *httpBase) headers() (sessionID, protocolVersion string) {
	b.sessionMu.RLock()
	defer b.sessionMu.RUnlock()
	return b.sessionID, b.protocolVersion
}

// applyHeaders adds configured headers plus the MCP session and negotiated
// protocol headers. The initialize request must not carry session or version
// headers because neither is known yet.
func (b *httpBase) applyHeaders(req *http.Request, initialize bool, accept string) {
	for key, value := range b.cfg.Headers {
		req.Header.Set(key, value)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if initialize {
		return
	}
	sessionID, protocolVersion := b.headers()
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
}

func (b *httpBase) captureSession(resp *http.Response) {
	b.setSessionID(strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")))
}

func (b *httpBase) ensureOpen() error {
	if b.closed.Load() {
		return ErrClosed
	}
	select {
	case <-b.done:
		if err := b.Err(); err != nil {
			return err
		}
		return ErrClosed
	default:
		return nil
	}
}

func (b *httpBase) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	reqCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.lifetime, cancel)
	return reqCtx, func() {
		stop()
		cancel()
	}
}

func (b *httpBase) deliver(ctx context.Context, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	message := append([]byte(nil), payload...)
	select {
	case b.messages <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		if err := b.Err(); err != nil {
			return err
		}
		return ErrClosed
	}
}

func (b *httpBase) closeBase() {
	b.closed.Store(true)
	b.cancel()
	if b.rt != nil {
		b.rt.CloseIdleConnections()
	}
	b.setErr(nil)
}

func (b *httpBase) statusError(resp *http.Response) error {
	body, _ := readLimited(resp.Body, maxHTTPErrorBody)
	return &httpStatusError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       strings.TrimSpace(string(body)),
	}
}

// streamableHTTPTransport implements the Streamable HTTP transport: every
// JSON-RPC message is POSTed to a single endpoint, and responses may arrive as
// either JSON or an SSE stream.
type streamableHTTPTransport struct {
	*httpBase
}

func newStreamableHTTPTransport(cfg ServerConfig) *streamableHTTPTransport {
	return &streamableHTTPTransport{httpBase: newHTTPBase(cfg)}
}

func (t *streamableHTTPTransport) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.cfg.URL == "" {
		return errors.New("streamable http transport requires a url")
	}
	return t.ensureOpen()
}

func (t *streamableHTTPTransport) Send(ctx context.Context, payload []byte) error {
	kind, expectedID, err := classifyRPCMessage(payload)
	if err != nil {
		return err
	}
	if err := t.ensureOpen(); err != nil {
		return err
	}

	initialize := isInitializeMessage(payload)
	reqCtx, cancel := t.requestContext(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build mcp request: %w", err)
	}
	t.applyHeaders(req, initialize, "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t.closed.Load() {
			return ErrClosed
		}
		return fmt.Errorf("post mcp request: %w", err)
	}
	defer resp.Body.Close()
	t.captureSession(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return t.statusError(resp)
	}

	// Notifications and client responses are acknowledged only; there is no
	// JSON-RPC response to wait for.
	if kind != rpcKindRequest {
		return nil
	}

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return errors.New("server accepted request without returning a response")
	}

	contentType := mediaType(resp.Header.Get("Content-Type"))
	switch {
	case contentType == "text/event-stream":
		return t.consumeSSE(reqCtx, resp.Body, expectedID)
	case contentType == "application/json" || contentType == "":
		body, err := readLimited(resp.Body, maxHTTPJSONBody)
		if err != nil {
			return fmt.Errorf("read mcp response: %w", err)
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return errors.New("mcp server returned an empty response body")
		}
		return t.deliver(reqCtx, body)
	default:
		body, _ := readLimited(resp.Body, maxHTTPErrorBody)
		return fmt.Errorf("unexpected mcp response content type %q: %s", contentType, truncate(strings.TrimSpace(string(body)), 256))
	}
}

func (t *streamableHTTPTransport) consumeSSE(ctx context.Context, body io.Reader, expectedID string) error {
	err := readSSE(body, func(event sseEvent) error {
		data := bytes.TrimSpace(event.Data)
		if len(data) == 0 {
			return nil
		}
		kind, id, err := classifyRPCMessage(data)
		if err != nil {
			return err
		}
		if err := t.deliver(ctx, data); err != nil {
			return err
		}
		if kind == rpcKindResponse && id == expectedID {
			return errStopSSE
		}
		return nil
	})
	if errors.Is(err, errStopSSE) {
		return nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t.closed.Load() {
			return ErrClosed
		}
		return fmt.Errorf("read mcp sse response: %w", err)
	}
	return errors.New("mcp sse stream ended before the response arrived")
}

func (t *streamableHTTPTransport) Close() error {
	t.closeBase()
	return nil
}

// sseHTTPTransport implements the legacy HTTP+SSE transport from protocol
// revision 2024-11-05. A long-lived GET stream announces a POST endpoint; all
// responses arrive on that stream.
type sseHTTPTransport struct {
	*httpBase

	endpointMu sync.RWMutex
	endpoint   string

	bodyMu    sync.Mutex
	body      io.ReadCloser
	getCancel context.CancelFunc

	readerDone chan struct{}
	started    atomic.Bool
	closeOnce  sync.Once
	closeErr   error
}

func newSSEHTTPTransport(cfg ServerConfig) *sseHTTPTransport {
	return &sseHTTPTransport{
		httpBase:   newHTTPBase(cfg),
		readerDone: make(chan struct{}),
	}
}

func (t *sseHTTPTransport) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.cfg.URL == "" {
		return errors.New("sse http transport requires a url")
	}
	if err := t.ensureOpen(); err != nil {
		return err
	}

	timeout := t.cfg.StartupTimeout.Duration()
	if timeout <= 0 {
		timeout = DefaultStartupTimeout
	}

	// The startup deadline must cover both the GET response headers and the
	// first endpoint event. Cancelling this child context also aborts a GET
	// that is stuck before headers; the context stays alive after Start so it
	// can continue to govern the streaming body.
	getCtx, getCancel := context.WithCancel(t.lifetime)
	timer := time.AfterFunc(timeout, getCancel)
	started := false
	defer func() {
		timer.Stop()
		if !started {
			getCancel()
		}
	}()

	req, err := http.NewRequestWithContext(getCtx, http.MethodGet, t.cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("build sse request: %w", err)
	}
	t.applyHeaders(req, true, "text/event-stream")

	resp, err := t.client.Do(req)
	if err != nil {
		if t.closed.Load() {
			return ErrClosed
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return fmt.Errorf("%w: timed out after %s connecting to the mcp sse endpoint: %v", ErrTimeout, timeout, ctxErr)
			}
			return ctxErr
		}
		if getCtx.Err() != nil {
			return fmt.Errorf("%w: timed out after %s connecting to the mcp sse endpoint", ErrTimeout, timeout)
		}
		return fmt.Errorf("connect to mcp sse endpoint: %w", err)
	}
	t.captureSession(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := t.statusError(resp)
		_ = resp.Body.Close()
		return err
	}
	if ct := mediaType(resp.Header.Get("Content-Type")); ct != "text/event-stream" {
		_ = resp.Body.Close()
		return fmt.Errorf("mcp sse endpoint returned content type %q", ct)
	}

	t.bodyMu.Lock()
	t.body = resp.Body
	t.getCancel = getCancel
	t.bodyMu.Unlock()
	t.started.Store(true)

	endpointCh := make(chan string, 1)
	readErr := make(chan error, 1)
	go t.readLoop(resp.Body, endpointCh, readErr)

	timedOut := func(err error) error {
		if err == nil {
			return fmt.Errorf("%w: timed out after %s waiting for the mcp sse endpoint event", ErrTimeout, timeout)
		}
		return fmt.Errorf("%w: timed out after %s waiting for the mcp sse endpoint event: %v", ErrTimeout, timeout, err)
	}

	select {
	case endpoint := <-endpointCh:
		if !timer.Stop() {
			return timedOut(errors.New("startup deadline elapsed"))
		}
		started = true
		t.setEndpoint(endpoint)
		return nil
	case err := <-readErr:
		if getCtx.Err() != nil && !t.closed.Load() {
			return timedOut(err)
		}
		return fmt.Errorf("read mcp sse stream: %w", err)
	case <-t.done:
		if getCtx.Err() != nil && !t.closed.Load() {
			return timedOut(t.Err())
		}
		if err := t.Err(); err != nil {
			return err
		}
		return ErrClosed
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return timedOut(ctx.Err())
		}
		return ctx.Err()
	case <-getCtx.Done():
		if t.closed.Load() {
			return ErrClosed
		}
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return timedOut(err)
			}
			return err
		}
		return timedOut(nil)
	}
}

func (t *sseHTTPTransport) readLoop(body io.Reader, endpointCh chan<- string, readErr chan<- error) {
	defer close(t.readerDone)

	err := readSSE(body, func(event sseEvent) error {
		data := bytes.TrimSpace(event.Data)
		if len(data) == 0 {
			return nil
		}
		if event.Event == "endpoint" || (event.Event == "" && data[0] != '{') {
			if endpointCh == nil {
				// A second endpoint event is redundant; ignoring it keeps the
				// read loop from blocking on a channel Start no longer reads.
				return nil
			}
			endpoint, err := t.resolveEndpoint(string(data))
			if err != nil {
				return err
			}
			select {
			case endpointCh <- endpoint:
				endpointCh = nil
			case <-t.done:
				return ErrClosed
			case <-t.lifetime.Done():
				return t.lifetime.Err()
			}
			return nil
		}
		return t.deliver(t.lifetime, data)
	})

	if t.closed.Load() {
		return
	}
	if err == nil {
		err = errors.New("stream closed by server")
	}
	t.setErr(fmt.Errorf("mcp sse stream ended: %w", err))
	select {
	case readErr <- err:
	default:
	}
}

func (t *sseHTTPTransport) resolveEndpoint(data string) (string, error) {
	base, err := url.Parse(t.cfg.URL)
	if err != nil {
		return "", fmt.Errorf("parse server url: %w", err)
	}
	ref, err := url.Parse(strings.TrimSpace(data))
	if err != nil {
		return "", fmt.Errorf("parse sse endpoint %q: %w", data, err)
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("sse endpoint has unsupported scheme %q", resolved.Scheme)
	}
	if resolved.Host == "" {
		return "", errors.New("sse endpoint has no host")
	}
	return resolved.String(), nil
}

func (t *sseHTTPTransport) setEndpoint(endpoint string) {
	t.endpointMu.Lock()
	t.endpoint = endpoint
	t.endpointMu.Unlock()
}

func (t *sseHTTPTransport) getEndpoint() string {
	t.endpointMu.RLock()
	defer t.endpointMu.RUnlock()
	return t.endpoint
}

func (t *sseHTTPTransport) Send(ctx context.Context, payload []byte) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}
	endpoint := t.getEndpoint()
	if endpoint == "" {
		return errors.New("mcp sse endpoint is not ready")
	}

	reqCtx, cancel := t.requestContext(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build mcp post: %w", err)
	}
	t.applyHeaders(req, isInitializeMessage(payload), "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t.closed.Load() {
			return ErrClosed
		}
		return fmt.Errorf("post mcp message: %w", err)
	}
	defer resp.Body.Close()
	t.captureSession(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return t.statusError(resp)
	}
	// Responses are delivered over the long-lived SSE stream, not this body.
	return nil
}

func (t *sseHTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		t.closed.Store(true)
		t.cancel()
		// Closing done releases a read loop blocked delivering a message to a
		// client that has already stopped reading.
		t.setErr(nil)

		t.bodyMu.Lock()
		body := t.body
		bodyCancel := t.getCancel
		t.bodyMu.Unlock()
		if bodyCancel != nil {
			bodyCancel()
		}
		if body != nil {
			_ = body.Close()
		}

		if t.started.Load() {
			select {
			case <-t.readerDone:
			case <-time.After(time.Second):
				t.closeErr = errors.New("timed out waiting for mcp sse reader to stop")
			}
		}
		if t.rt != nil {
			t.rt.CloseIdleConnections()
		}
	})
	return t.closeErr
}

func isInitializeMessage(payload []byte) bool {
	var msg rpcMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return false
	}
	return msg.Method == "initialize"
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	limited := io.LimitReader(r, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("body exceeds %d bytes", max)
	}
	return data, nil
}
