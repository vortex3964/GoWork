package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func testStdioConfig(scenario string) ServerConfig {
	env := map[string]string{"GO_WANT_MCP_HELPER": "1"}
	if scenario != "" {
		env["MCP_HELPER_SCENARIO"] = scenario
	}
	return ServerConfig{
		Name:            "fake",
		Command:         os.Args[0],
		Args:            []string{"-test.run=^TestStdioHelperProcess$"},
		Env:             env,
		StartupTimeout:  Duration(3 * time.Second),
		RequestTimeout:  Duration(3 * time.Second),
		ShutdownTimeout: Duration(2 * time.Second),
	}
}

func TestClientStdioLifecycle(t *testing.T) {
	client, err := NewClient(testStdioConfig(""))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Name() != "fake" {
		t.Errorf("Name() = %q", client.Name())
	}
	if got := client.Transport(); got != TransportStdio {
		t.Errorf("Transport() = %q, want stdio", got)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !client.Healthy() {
		t.Error("Healthy() = false after Start")
	}

	stdio, ok := client.transport.(*stdioTransport)
	if !ok {
		t.Fatalf("transport type = %T", client.transport)
	}

	if got := client.ProtocolVersion(); got != ProtocolVersion20250618 {
		t.Errorf("ProtocolVersion() = %q, want %q", got, ProtocolVersion20250618)
	}
	info := client.ServerInfo()
	if info.Name != "fake-server" || info.Version != "1.2.3" {
		t.Errorf("ServerInfo() = %+v", info)
	}
	caps := client.Capabilities()
	if caps.Tools == nil || !caps.Tools.ListChanged {
		t.Errorf("Capabilities().Tools = %+v", caps.Tools)
	}
	if got := client.Instructions(); got != "Use the echo tool." {
		t.Errorf("Instructions() = %q", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("ListTools returned %d tools: %+v", len(tools), tools)
	}
	wantNames := []string{"echo", "fail", "image", "resource"}
	for i, want := range wantNames {
		if tools[i].Name != want {
			t.Errorf("tools[%d].Name = %q, want %q", i, tools[i].Name, want)
		}
	}

	echo := tools[0]
	if echo.Title != "Echo" {
		t.Errorf("echo.Title = %q", echo.Title)
	}
	if echo.Description != "Echo text back" {
		t.Errorf("echo.Description = %q, want trimmed", echo.Description)
	}
	if !strings.Contains(string(echo.InputSchema), `"text"`) {
		t.Errorf("echo.InputSchema = %s", echo.InputSchema)
	}

	image := tools[2]
	if string(image.InputSchema) != `{"type":"object"}` {
		t.Errorf("image.InputSchema = %s, want default object schema", image.InputSchema)
	}

	result, err := client.CallTool(ctx, "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("CallTool echo: %v", err)
	}
	if result.Text != "echo: hello" || result.IsError {
		t.Errorf("echo result = %+v", result)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Errorf("echo content = %+v", result.Content)
	}

	fail, err := client.CallTool(ctx, "fail", nil)
	if err != nil {
		t.Fatalf("CallTool fail: %v", err)
	}
	if !fail.IsError || fail.Text != "tool execution failed" {
		t.Errorf("fail result = %+v", fail)
	}

	imageResult, err := client.CallTool(ctx, "image", nil)
	if err != nil {
		t.Fatalf("CallTool image: %v", err)
	}
	if imageResult.Text != "[image: image/png]\ncaption" {
		t.Errorf("image text = %q", imageResult.Text)
	}
	if len(imageResult.Content) != 2 || imageResult.Content[0].Data != "aGVsbG8=" {
		t.Errorf("image content = %+v", imageResult.Content)
	}

	resourceResult, err := client.CallTool(ctx, "resource", nil)
	if err != nil {
		t.Fatalf("CallTool resource: %v", err)
	}
	if resourceResult.Text != "file body" {
		t.Errorf("resource text = %q", resourceResult.Text)
	}

	structured, err := client.CallTool(ctx, "structured", nil)
	if err != nil {
		t.Fatalf("CallTool structured: %v", err)
	}
	if structured.Text != `{"answer":42}` {
		t.Errorf("structured text = %q", structured.Text)
	}

	_, err = client.CallTool(ctx, "missing", nil)
	if err == nil {
		t.Fatal("CallTool missing succeeded")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("CallTool missing error = %v, want RPCError %d", err, CodeInvalidParams)
	}

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := client.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if client.Healthy() {
		t.Error("Healthy() = true after Close")
	}

	select {
	case <-stdio.waitDone:
	default:
		t.Error("server process was not reaped")
	}
	select {
	case <-stdio.readerDone:
	default:
		t.Error("stdio output reader did not stop")
	}
	if stdio.cmd.ProcessState == nil {
		t.Error("process state is nil after Close; server process was not reaped")
	}

	if _, err := client.ListTools(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("ListTools after Close = %v, want ErrClosed", err)
	}
	if _, err := client.CallTool(ctx, "echo", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("CallTool after Close = %v, want ErrClosed", err)
	}
	if err := client.Ping(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Ping after Close = %v, want ErrClosed", err)
	}
	if err := client.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("Start after Close = %v, want ErrClosed", err)
	}
}

func TestClientStdioStartErrors(t *testing.T) {
	tests := []struct {
		name      string
		scenario  string
		startup   time.Duration
		want      string
		checkIs   error
		checkRPC  int
		checkDiag string
	}{
		{
			name:     "initialize rpc error",
			scenario: "init-error",
			want:     "initialize rejected",
			checkRPC: -32000,
		},
		{
			name:     "unsupported protocol version",
			scenario: "unsupported-version",
			want:     "unsupported protocol version",
			checkIs:  ErrUnsupportedProtocolVersion,
		},
		{
			name:     "startup timeout",
			scenario: "silent-init",
			startup:  250 * time.Millisecond,
			want:     "request timed out",
			checkIs:  ErrTimeout,
		},
		{
			name:      "server exits during initialize",
			scenario:  "crash-start",
			want:      "missing credentials",
			checkDiag: "missing credentials",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testStdioConfig(tt.scenario)
			if tt.startup > 0 {
				cfg.StartupTimeout = Duration(tt.startup)
			}
			cfg.ShutdownTimeout = Duration(time.Second)

			client, err := NewClient(cfg)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer func() {
				if err := client.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			}()

			err = client.Start(context.Background())
			if err == nil {
				t.Fatal("Start succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Start error = %q, want substring %q", err, tt.want)
			}
			if tt.checkIs != nil && !errors.Is(err, tt.checkIs) {
				t.Errorf("Start error = %v, want errors.Is(%v)", err, tt.checkIs)
			}
			if tt.checkRPC != 0 {
				var rpcErr *RPCError
				if !errors.As(err, &rpcErr) || rpcErr.Code != tt.checkRPC {
					t.Errorf("Start error = %v, want RPCError code %d", err, tt.checkRPC)
				}
			}
			if tt.checkDiag != "" && !strings.Contains(err.Error(), tt.checkDiag) {
				t.Errorf("Start error = %q, want diagnostics %q", err, tt.checkDiag)
			}

			if err := client.Ping(context.Background()); !errors.Is(err, ErrNotStarted) {
				t.Errorf("Ping after failed Start = %v, want ErrNotStarted", err)
			}
		})
	}
}

func TestClientStdioUnknownCommand(t *testing.T) {
	cfg := testStdioConfig("")
	cfg.Command = "/nonexistent/definitely-not-a-real-mcp-server"
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	err = client.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start server") {
		t.Fatalf("Start error = %v, want start server failure", err)
	}
}

func TestClientStdioProcessExit(t *testing.T) {
	client, err := NewClient(testStdioConfig(""))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.CallTool(ctx, "exit", nil); err == nil {
		t.Fatal("CallTool exit succeeded, want connection error")
	}
	if err := client.Ping(ctx); err == nil {
		t.Fatal("Ping succeeded after server exit")
	}
	if client.Healthy() {
		t.Error("Healthy() = true after server exit")
	}
}

func TestClientNotStarted(t *testing.T) {
	client, err := NewClient(testStdioConfig(""))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()
	if _, err := client.ListTools(ctx); !errors.Is(err, ErrNotStarted) {
		t.Errorf("ListTools = %v, want ErrNotStarted", err)
	}
	if _, err := client.CallTool(ctx, "echo", nil); !errors.Is(err, ErrNotStarted) {
		t.Errorf("CallTool = %v, want ErrNotStarted", err)
	}
	if err := client.Ping(ctx); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Ping = %v, want ErrNotStarted", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Start(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Start after Close = %v, want ErrClosed", err)
	}
}

func TestClientStartTwice(t *testing.T) {
	client, err := NewClient(testStdioConfig(""))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := client.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("second Start = %v, want ErrAlreadyStarted", err)
	}
}

func TestNewClientDefaultsAndValidation(t *testing.T) {
	client, err := NewClient(ServerConfig{Command: "echo"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Name() != "mcp" {
		t.Errorf("Name() = %q, want mcp", client.Name())
	}
	if client.cfg.StartupTimeout.Duration() != DefaultStartupTimeout {
		t.Errorf("StartupTimeout = %s", client.cfg.StartupTimeout.Duration())
	}
	if client.cfg.RequestTimeout.Duration() != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %s", client.cfg.RequestTimeout.Duration())
	}
	if client.cfg.ShutdownTimeout.Duration() != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s", client.cfg.ShutdownTimeout.Duration())
	}
	_ = client.Close()

	if _, err := NewClient(ServerConfig{Name: "bad", Command: "a", URL: "https://example.com"}); err == nil {
		t.Fatal("NewClient accepted command and url together")
	}
	if _, err := NewClient(ServerConfig{Name: "bad"}); err == nil {
		t.Fatal("NewClient accepted an empty server")
	}
	if _, err := NewClient(ServerConfig{Name: "bad", URL: "ftp://example.com"}); err == nil {
		t.Fatal("NewClient accepted a non-http url")
	}
}

func TestNormalizeTool(t *testing.T) {
	tests := []struct {
		name    string
		tool    Tool
		wantErr bool
		check   func(t *testing.T, tool Tool)
	}{
		{
			name: "default schema",
			tool: Tool{Name: "  noargs  ", Description: "  desc  "},
			check: func(t *testing.T, tool Tool) {
				if tool.Name != "noargs" || tool.Description != "desc" {
					t.Errorf("tool = %+v", tool)
				}
				if string(tool.InputSchema) != `{"type":"object"}` {
					t.Errorf("InputSchema = %s", tool.InputSchema)
				}
			},
		},
		{
			name: "null schema",
			tool: Tool{Name: "null", InputSchema: json.RawMessage("null")},
			check: func(t *testing.T, tool Tool) {
				if string(tool.InputSchema) != `{"type":"object"}` {
					t.Errorf("InputSchema = %s", tool.InputSchema)
				}
			},
		},
		{
			name:    "missing name",
			tool:    Tool{},
			wantErr: true,
		},
		{
			name:    "invalid schema",
			tool:    Tool{Name: "bad", InputSchema: json.RawMessage(`{`)},
			wantErr: true,
		},
		{
			name:    "array schema",
			tool:    Tool{Name: "bad", InputSchema: json.RawMessage(`[]`)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeTool(tt.tool)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeTool succeeded: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeTool: %v", err)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
