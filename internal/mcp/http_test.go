package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testHTTPConfig(url string) ServerConfig {
	return ServerConfig{
		Name:            "http-fake",
		Type:            string(TransportHTTP),
		URL:             url,
		Headers:         map[string]string{"X-Test-Token": "secret"},
		AllowPrivate:    true,
		StartupTimeout:  Duration(3 * time.Second),
		RequestTimeout:  Duration(3 * time.Second),
		ShutdownTimeout: Duration(2 * time.Second),
	}
}

func TestClientHTTPStreamable(t *testing.T) {
	var mu sync.Mutex
	var initializeSession, requestSession, requestProtocol string
	var listedCursor string
	subsequentRequests := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("X-Test-Token"); got != "secret" {
			t.Errorf("X-Test-Token = %q, want secret", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		msg, err := decodeRPCMessage(body)
		if err != nil {
			t.Errorf("decode body %s: %v", body, err)
			return
		}

		switch msg.Method {
		case "initialize":
			mu.Lock()
			initializeSession = r.Header.Get("Mcp-Session-Id")
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", "session-123")
			writeRPCResult(t, w, msg.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion20250618,
				Capabilities: ServerCapabilities{
					Tools: &ToolsCapability{ListChanged: true},
				},
				ServerInfo:   ServerInfo{Name: "http-fake", Version: "2.0.0"},
				Instructions: "HTTP instructions",
			})

		case "notifications/initialized":
			mu.Lock()
			requestSession = r.Header.Get("Mcp-Session-Id")
			requestProtocol = r.Header.Get("MCP-Protocol-Version")
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)

		case "tools/list":
			mu.Lock()
			subsequentRequests++
			mu.Unlock()
			var params struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			mu.Lock()
			listedCursor = params.Cursor
			mu.Unlock()

			if params.Cursor == "" {
				writeRPCResult(t, w, msg.ID, map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "Echo",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"text": map[string]any{"type": "string"}},
						},
					}},
					"nextCursor": "page-2",
				})
				return
			}
			writeRPCResult(t, w, msg.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "ping-tool",
					"description": "Pings",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})

		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			switch params.Name {
			case "echo":
				writeRPCResult(t, w, msg.ID, map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": fmt.Sprintf("http echo: %v", params.Arguments["text"]),
					}},
				})
			case "fail":
				writeRPCResult(t, w, msg.ID, map[string]any{
					"content": []map[string]any{{"type": "text", "text": "http failure"}},
					"isError": true,
				})
			default:
				writeRPCError(t, w, msg.ID, CodeInvalidParams, "unknown tool")
			}

		case "ping":
			writeRPCResult(t, w, msg.ID, map[string]any{})

		default:
			writeRPCError(t, w, msg.ID, CodeMethodNotFound, "unexpected method "+msg.Method)
		}
	}))
	defer srv.Close()

	client, err := NewClient(testHTTPConfig(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if got := client.ProtocolVersion(); got != ProtocolVersion20250618 {
		t.Errorf("ProtocolVersion = %q", got)
	}
	if got := client.ServerInfo().Name; got != "http-fake" {
		t.Errorf("ServerInfo.Name = %q", got)
	}
	if got := client.Instructions(); got != "HTTP instructions" {
		t.Errorf("Instructions = %q", got)
	}
	if caps := client.Capabilities(); caps.Tools == nil || !caps.Tools.ListChanged {
		t.Errorf("Capabilities.Tools = %+v", caps.Tools)
	}

	mu.Lock()
	if initializeSession != "" {
		t.Errorf("initialize sent Mcp-Session-Id %q, want empty", initializeSession)
	}
	if requestSession != "session-123" {
		t.Errorf("initialized notification session = %q", requestSession)
	}
	if requestProtocol != ProtocolVersion20250618 {
		t.Errorf("initialized notification protocol = %q", requestProtocol)
	}
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "ping-tool" {
		t.Fatalf("tools = %+v", tools)
	}

	mu.Lock()
	if listedCursor != "page-2" {
		t.Errorf("second tools/list cursor = %q, want page-2", listedCursor)
	}
	if subsequentRequests != 2 {
		t.Errorf("tools/list requests = %d, want 2", subsequentRequests)
	}
	mu.Unlock()

	result, err := client.CallTool(ctx, "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Text != "http echo: hi" || result.IsError {
		t.Errorf("CallTool result = %+v", result)
	}

	fail, err := client.CallTool(ctx, "fail", nil)
	if err != nil {
		t.Fatalf("CallTool fail: %v", err)
	}
	if !fail.IsError || fail.Text != "http failure" {
		t.Errorf("fail result = %+v", fail)
	}

	_, err = client.CallTool(ctx, "missing", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("CallTool missing error = %v, want RPCError", err)
	}

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClientHTTPStreamableSSEResponse(t *testing.T) {
	var pingOnce sync.Once
	pingResponded := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		msg, err := decodeRPCMessage(body)
		if err != nil {
			t.Errorf("decode body %s: %v", body, err)
			return
		}

		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sse-session")
			writeRPCResult(t, w, msg.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion20250618,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      ServerInfo{Name: "sse-fake", Version: "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Error("response writer does not support flushing")
				return
			}

			// Priming event, then a server-initiated ping that the client must
			// answer on a concurrent POST before the tool result arrives.
			fmt.Fprint(w, "event: message\ndata:\n\n")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":900,\"method\":\"ping\"}\n\n")
			flusher.Flush()

			select {
			case <-pingResponded:
			case <-time.After(3 * time.Second):
				t.Error("client did not respond to server ping")
				return
			case <-r.Context().Done():
				return
			}

			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"sse result\"}]}}\n\n", msg.ID)
			flusher.Flush()

		case "":
			if string(msg.ID) == "900" {
				pingOnce.Do(func() { close(pingResponded) })
				w.WriteHeader(http.StatusAccepted)
				return
			}
			t.Errorf("unexpected response id %s", msg.ID)
			w.WriteHeader(http.StatusAccepted)
		default:
			writeRPCError(t, w, msg.ID, CodeMethodNotFound, "unexpected method")
		}
	}))
	defer srv.Close()

	client, err := NewClient(testHTTPConfig(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.CallTool(ctx, "echo", map[string]any{"text": "x"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Text != "sse result" {
		t.Errorf("CallTool text = %q", result.Text)
	}

	select {
	case <-pingResponded:
	default:
		t.Error("server did not receive the ping response")
	}
}

func TestClientHTTPLegacySSEAutoFallback(t *testing.T) {
	responses := make(chan []byte, 16)
	var getCount atomic.Int32
	var mu sync.Mutex
	var protocolHeader string
	seenProtocol := false

	mux := http.NewServeMux()
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		msg, err := decodeRPCMessage(body)
		if err != nil {
			t.Errorf("decode body %s: %v", body, err)
			return
		}
		if msg.Method != "initialize" && msg.Method != "" {
			mu.Lock()
			protocolHeader = r.Header.Get("MCP-Protocol-Version")
			seenProtocol = true
			mu.Unlock()
		}

		switch msg.Method {
		case "initialize":
			payload, _ := json.Marshal(rpcResponse{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Result: InitializeResult{
					ProtocolVersion: ProtocolVersion20250618,
					Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
					ServerInfo:      ServerInfo{Name: "legacy-fake", Version: "0.9"},
				},
			})
			responses <- payload
		case "notifications/initialized":
		case "tools/list":
			payload, _ := json.Marshal(rpcResponse{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Result: map[string]any{
					"tools": []map[string]any{{
						"name":        "legacy-tool",
						"description": "Legacy",
						"inputSchema": map[string]any{"type": "object"},
					}},
				},
			})
			responses <- payload
		case "tools/call":
			payload, _ := json.Marshal(rpcResponse{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Result: map[string]any{
					"content": []map[string]any{{"type": "text", "text": "legacy result"}},
				},
			})
			responses <- payload
		case "ping":
			payload, _ := json.Marshal(rpcResponse{JSONRPC: jsonRPCVersion, ID: msg.ID, Result: map[string]any{}})
			responses <- payload
		default:
			payload, _ := json.Marshal(rpcResponse{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Error:   &RPCError{Code: CodeMethodNotFound, Message: "unexpected"},
			})
			responses <- payload
		}
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			// The legacy server has no Streamable HTTP endpoint; this is what
			// triggers the client's automatic fallback.
			w.Header().Set("Allow", "GET")
			http.Error(w, "use the SSE transport", http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		getCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer does not support flushing")
			return
		}
		fmt.Fprint(w, "event: endpoint\ndata: /messages?session=abc\n\n")
		flusher.Flush()

		for {
			select {
			case payload := <-responses:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := testHTTPConfig(srv.URL)
	// Leave Type empty so a failed Streamable HTTP initialize falls back to
	// the legacy SSE transport.
	cfg.Type = ""
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := getCount.Load(); got != 1 {
		t.Errorf("SSE GET count = %d, want 1", got)
	}
	if got := client.ServerInfo().Name; got != "legacy-fake" {
		t.Errorf("ServerInfo.Name = %q", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "legacy-tool" {
		t.Fatalf("tools = %+v", tools)
	}

	result, err := client.CallTool(ctx, "legacy-tool", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Text != "legacy result" {
		t.Errorf("CallTool text = %q", result.Text)
	}

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	mu.Lock()
	if !seenProtocol || protocolHeader != ProtocolVersion20250618 {
		t.Errorf("protocol header = %q (seen=%v), want %q", protocolHeader, seenProtocol, ProtocolVersion20250618)
	}
	mu.Unlock()
}

func TestClientHTTPLegacySSEEndpointTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Never announce an endpoint; the client must time out and close.
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := testHTTPConfig(srv.URL)
	cfg.Type = ""
	cfg.StartupTimeout = Duration(250 * time.Millisecond)
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.Start(context.Background())
	if err == nil {
		t.Fatal("Start succeeded, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Start error = %q, want timeout", err)
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("Start error = %v, want errors.Is(ErrTimeout)", err)
	}
}

func TestClientHTTPInitializeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server exploded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, err := NewClient(testHTTPConfig(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.Start(context.Background())
	if err == nil {
		t.Fatal("Start succeeded, want error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "server exploded") {
		t.Fatalf("Start error = %q, want HTTP 500 and body", err)
	}
}

func TestClientHTTPTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		msg, err := decodeRPCMessage(body)
		if err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		switch msg.Method {
		case "initialize":
			writeRPCResult(t, w, msg.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion20250618,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      ServerInfo{Name: "slow", Version: "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			time.Sleep(500 * time.Millisecond)
			writeRPCResult(t, w, msg.ID, map[string]any{"tools": []any{}})
		case "ping":
			writeRPCResult(t, w, msg.ID, map[string]any{})
		default:
			writeRPCError(t, w, msg.ID, CodeMethodNotFound, "unexpected")
		}
	}))
	defer srv.Close()

	cfg := testHTTPConfig(srv.URL)
	cfg.RequestTimeout = Duration(100 * time.Millisecond)
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.ListTools(ctx)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ListTools error = %v, want ErrTimeout", err)
	}

	// A timed-out request must not poison the connection.
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping after timeout: %v", err)
	}
}

func TestClientHTTPUnexpectedContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		msg, _ := decodeRPCMessage(body)
		switch msg.Method {
		case "initialize":
			writeRPCResult(t, w, msg.ID, InitializeResult{
				ProtocolVersion: ProtocolVersion20250618,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
				ServerInfo:      ServerInfo{Name: "html", Version: "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>not json</html>")
		}
	}))
	defer srv.Close()

	client, err := NewClient(testHTTPConfig(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err = client.ListTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "content type") {
		t.Fatalf("ListTools error = %v, want content type error", err)
	}
}

func writeRPCResult(t *testing.T, w http.ResponseWriter, id json.RawMessage, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result})
}

func writeRPCError(t *testing.T, w http.ResponseWriter, id json.RawMessage, code int, message string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	})
}
