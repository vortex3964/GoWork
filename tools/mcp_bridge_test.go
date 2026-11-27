package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"GoWork/internal/mcp"
)

type nilMCPResultCaller struct{}

func (nilMCPResultCaller) CallTool(context.Context, string, any) (*mcp.CallResult, error) {
	return nil, nil
}

type fakeMCPCaller struct {
	mu      sync.Mutex
	calls   int
	name    string
	args    any
	result  *mcp.CallResult
	err     error
	started chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (f *fakeMCPCaller) CallTool(ctx context.Context, name string, arguments any) (*mcp.CallResult, error) {
	f.mu.Lock()
	f.calls++
	f.name = name
	f.args = arguments
	started := f.started
	block := f.block
	f.mu.Unlock()

	if started != nil {
		f.once.Do(func() { close(started) })
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &mcp.CallResult{Text: "ok"}, nil
}

func (f *fakeMCPCaller) snapshot() (calls int, name string, args any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.name, f.args
}

func TestMCPBridgeSuccess(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
	fake := &fakeMCPCaller{result: &mcp.CallResult{
		Content: []mcp.ContentBlock{{Type: "text", Text: "hello from mcp"}},
		Text:    "hello from mcp",
	}}
	bridge, err := NewMCPBridge(fake, "files", mcp.Tool{
		Name:        "echo",
		Description: "Echo text back",
		InputSchema: schema,
	})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}

	if bridge.Name() != "mcp_files_echo" {
		t.Errorf("Name() = %q", bridge.Name())
	}
	if bridge.MCPName() != "echo" {
		t.Errorf("MCPName() = %q", bridge.MCPName())
	}
	if bridge.Server() != "files" {
		t.Errorf("Server() = %q", bridge.Server())
	}
	if bridge.Description() != "Echo text back" {
		t.Errorf("Description() = %q", bridge.Description())
	}
	if bridge.Kind() != KindAllowed {
		t.Errorf("Kind() = %v, want KindAllowed", bridge.Kind())
	}
	if got := string(bridge.InputSchema()); got != string(schema) {
		t.Errorf("InputSchema() = %s", got)
	}

	// The schema returned to providers must not alias the bridge's copy.
	returned := bridge.InputSchema()
	returned[0] = 'x'
	if got := string(bridge.InputSchema()); got != string(schema) {
		t.Errorf("InputSchema was mutated through its return value: %s", got)
	}

	result, err := bridge.Run(context.Background(), DispatchArgs{}, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Content != "hello from mcp" || result.IsError {
		t.Errorf("Run result = %+v", result)
	}

	calls, name, args := fake.snapshot()
	if calls != 1 || name != "echo" {
		t.Errorf("client call = count %d name %q", calls, name)
	}
	rawArgs, ok := args.(json.RawMessage)
	if !ok || string(rawArgs) != `{"text":"hi"}` {
		t.Errorf("arguments = %#v, want raw {\"text\":\"hi\"}", args)
	}
}

func TestMCPBridgeContentFallbackAndToolError(t *testing.T) {
	fake := &fakeMCPCaller{result: &mcp.CallResult{
		Content: []mcp.ContentBlock{
			{Type: "text", Text: "first"},
			{Type: "image", MIMEType: "image/png"},
			{Type: "resource", Resource: &mcp.ResourceContents{URI: "file:///x", Text: "file body"}},
		},
		IsError: true,
	}}
	bridge, err := NewMCPBridge(fake, "files", mcp.Tool{Name: "mixed"})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}
	result, err := bridge.Run(context.Background(), DispatchArgs{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true")
	}
	want := "first\n[image: image/png]\nfile body"
	if result.Content != want {
		t.Errorf("Content = %q, want %q", result.Content, want)
	}

	// Structured content is used when the server omitted readable blocks.
	fake.result = &mcp.CallResult{StructuredContent: json.RawMessage(`{"answer":42}`)}
	result, err = bridge.Run(context.Background(), DispatchArgs{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run structured: %v", err)
	}
	if result.Content != `{"answer":42}` {
		t.Errorf("structured Content = %q", result.Content)
	}
}

func TestMCPBridgeTransportErrorAndNilResult(t *testing.T) {
	sentinel := errors.New("connection lost")
	bridge, err := NewMCPBridge(&fakeMCPCaller{err: sentinel}, "files", mcp.Tool{Name: "echo"})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}
	if _, err := bridge.Run(context.Background(), DispatchArgs{}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want sentinel", err)
	}

	bridge, err = NewMCPBridge(nilMCPResultCaller{}, "files", mcp.Tool{Name: "echo"})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}
	if _, err := bridge.Run(context.Background(), DispatchArgs{}, nil); err == nil || !strings.Contains(err.Error(), "no result") {
		t.Fatalf("Run nil result error = %v", err)
	}
}

func TestMCPBridgeCancellation(t *testing.T) {
	fake := &fakeMCPCaller{}
	bridge, err := NewMCPBridge(fake, "files", mcp.Tool{Name: "echo"})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bridge.Run(ctx, DispatchArgs{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Run error = %v, want context.Canceled", err)
	}
	if calls, _, _ := fake.snapshot(); calls != 0 {
		t.Errorf("pre-canceled Run called the client %d times", calls)
	}

	started := make(chan struct{})
	block := make(chan struct{})
	fake.started = started
	fake.block = block
	ctx, cancel = context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, err := bridge.Run(ctx, DispatchArgs{}, nil)
		resultCh <- err
	}()
	<-started
	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight Run error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestMCPBridgeInvalidArguments(t *testing.T) {
	fake := &fakeMCPCaller{}
	bridge, err := NewMCPBridge(fake, "files", mcp.Tool{Name: "echo"})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}

	for _, input := range []json.RawMessage{
		json.RawMessage(`[]`),
		json.RawMessage(`"text"`),
		json.RawMessage(`{`),
	} {
		result, err := bridge.Run(context.Background(), DispatchArgs{}, input)
		if err != nil {
			t.Fatalf("Run(%s) error = %v, want tool result", input, err)
		}
		if !result.IsError || !strings.Contains(result.Content, "arguments must be a JSON object") {
			t.Errorf("Run(%s) = %+v, want argument error", input, result)
		}
	}
	if calls, _, _ := fake.snapshot(); calls != 0 {
		t.Errorf("invalid arguments called the client %d times", calls)
	}
}

func TestNewMCPBridgeValidationAndDefaults(t *testing.T) {
	fake := &fakeMCPCaller{}
	if _, err := NewMCPBridge(nil, "files", mcp.Tool{Name: "echo"}); err == nil {
		t.Fatal("NewMCPBridge accepted a nil client")
	}
	if _, err := NewMCPBridge(fake, "files", mcp.Tool{}); err == nil {
		t.Fatal("NewMCPBridge accepted an empty tool name")
	}
	if _, err := NewMCPBridge(fake, "files", mcp.Tool{Name: "bad", InputSchema: json.RawMessage(`{`)}); err == nil {
		t.Fatal("NewMCPBridge accepted invalid schema JSON")
	}
	if _, err := NewMCPBridge(fake, "files", mcp.Tool{Name: "bad", InputSchema: json.RawMessage(`[]`)}); err == nil {
		t.Fatal("NewMCPBridge accepted a non-object schema")
	}

	bridge, err := NewMCPBridge(fake, "files", mcp.Tool{Name: "  noargs  "})
	if err != nil {
		t.Fatalf("NewMCPBridge: %v", err)
	}
	if got := string(bridge.InputSchema()); got != `{"type":"object"}` {
		t.Errorf("default InputSchema = %s", got)
	}
	if !strings.Contains(bridge.Description(), `MCP tool "noargs" from server "files"`) {
		t.Errorf("fallback Description = %q", bridge.Description())
	}
	if bridge.MCPName() != "noargs" {
		t.Errorf("MCPName = %q, want trimmed", bridge.MCPName())
	}
}

func TestMCPToolsConversion(t *testing.T) {
	fake := &fakeMCPCaller{}
	definitions := []mcp.Tool{
		{
			Name:        "echo",
			Description: "Echo text",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		{
			Name:        "read.file",
			Description: "Read a file",
		},
	}

	agentTools, providerDefs, err := MCPTools("my server", fake, definitions)
	if err != nil {
		t.Fatalf("MCPTools: %v", err)
	}
	if len(agentTools) != 2 || len(providerDefs) != 2 {
		t.Fatalf("got %d agent tools and %d provider defs", len(agentTools), len(providerDefs))
	}
	wantNames := []string{"mcp_my_server_echo", "mcp_my_server_read_file"}
	for i, want := range wantNames {
		if got := agentTools[i].Name(); got != want {
			t.Errorf("agentTools[%d].Name() = %q, want %q", i, got, want)
		}
		if got := providerDefs[i].Name; got != want {
			t.Errorf("providerDefs[%d].Name = %q, want %q", i, got, want)
		}
		if got := string(providerDefs[i].InputSchema); got != string(agentTools[i].InputSchema()) {
			t.Errorf("providerDefs[%d] schema = %s", i, got)
		}
		if providerDefs[i].Description != agentTools[i].Description() {
			t.Errorf("providerDefs[%d] description mismatch", i)
		}
	}
	if providerDefs[1].Description != "Read a file" {
		t.Errorf("providerDefs[1].Description = %q", providerDefs[1].Description)
	}
	if got := string(providerDefs[1].InputSchema); got != `{"type":"object"}` {
		t.Errorf("default provider schema = %s", got)
	}

	bridge, ok := agentTools[0].(*MCPBridge)
	if !ok {
		t.Fatalf("agentTools[0] type = %T", agentTools[0])
	}
	if bridge.MCPName() != "echo" {
		t.Errorf("bridge.MCPName() = %q", bridge.MCPName())
	}
}

func TestMCPToolsRejectsSanitizedCollisions(t *testing.T) {
	_, _, err := MCPTools("server", &fakeMCPCaller{}, []mcp.Tool{
		{Name: "a.b"},
		{Name: "a/b"},
	})
	if err == nil || !strings.Contains(err.Error(), "both map") {
		t.Fatalf("MCPTools error = %v, want collision", err)
	}
}

func TestMCPToolName(t *testing.T) {
	tests := []struct {
		server string
		tool   string
		want   string
	}{
		{"files", "read_file", "mcp_files_read_file"},
		{"my server", "read.file", "mcp_my_server_read_file"},
		{"", "echo", "mcp_echo"},
		{"files", "", "mcp_files"},
		{"", "", "mcp_tool"},
		{"x/y", "z:q", "mcp_x_y_z_q"},
	}
	for _, tt := range tests {
		if got := MCPToolName(tt.server, tt.tool); got != tt.want {
			t.Errorf("MCPToolName(%q, %q) = %q, want %q", tt.server, tt.tool, got, tt.want)
		}
	}

	longTool := strings.Repeat("a", 100)
	name := MCPToolName("server", longTool)
	if len(name) > mcpToolNameLimit {
		t.Errorf("long name length = %d, want <= %d: %s", len(name), mcpToolNameLimit, name)
	}
	if name != MCPToolName("server", longTool) {
		t.Error("MCPToolName is not deterministic")
	}
	if name == MCPToolName("other", longTool) {
		t.Error("different servers produced the same truncated name")
	}
}
