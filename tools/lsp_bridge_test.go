package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GoWork/internal/lsp"
	"GoWork/internal/lsp/lspfake"
)

// TestMain doubles as the fake language-server entry point for bridge tests.
func TestMain(m *testing.M) {
	if lspfake.MaybeRun() {
		return
	}
	os.Exit(m.Run())
}

type lspTestSetup struct {
	bridge *LSPBridge
	root   string
	args   DispatchArgs
}

func newLSPTestSetup(t *testing.T, languages []string, extraEnv map[string]string) *lspTestSetup {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {\n\thelper()\n}\n\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	env := map[string]string{lspfake.Env: "1"}
	for key, value := range extraEnv {
		env[key] = value
	}
	cfg := lsp.NewConfig()
	if len(languages) > 0 {
		if err := cfg.Add(lsp.ServerConfig{
			Name:        "fake-lsp",
			Command:     os.Args[0],
			Env:         env,
			LanguageIDs: languages,
			Enabled:     true,
		}); err != nil {
			t.Fatalf("configure fake server: %v", err)
		}
	}
	bridge := NewLSPBridge(cfg)
	t.Cleanup(func() {
		if err := bridge.Close(); err != nil {
			t.Errorf("bridge Close: %v", err)
		}
	})
	return &lspTestSetup{
		bridge: bridge,
		root:   root,
		args:   DispatchArgs{RootPath: &root},
	}
}

func runLSPTool(t *testing.T, tool AgentTool, args DispatchArgs, input any) ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Run(context.Background(), args, raw)
	if err != nil {
		t.Fatalf("%s Run returned transport error: %v", tool.Name(), err)
	}
	return result
}

func TestLSPBridgeToolsSuccess(t *testing.T) {
	setup := newLSPTestSetup(t, []string{"go"}, nil)

	diagnostics := runLSPTool(t, NewLSPDiagnosticsTool(setup.bridge), setup.args, map[string]any{"path": "main.go"})
	if diagnostics.IsError {
		t.Fatalf("lsp_diagnostics failed: %s", diagnostics.Content)
	}
	if !strings.Contains(diagnostics.Content, "diagnostics for main.go: 1") {
		t.Fatalf("diagnostics output = %q", diagnostics.Content)
	}
	if !strings.Contains(diagnostics.Content, "2:3 [error] fake diagnostic (fake)") {
		t.Fatalf("diagnostics output lacks the 1-based position/message: %q", diagnostics.Content)
	}

	symbols := runLSPTool(t, NewLSPSymbolsTool(setup.bridge), setup.args, map[string]any{"path": "main.go"})
	if symbols.IsError {
		t.Fatalf("lsp_symbols failed: %s", symbols.Content)
	}
	if !strings.Contains(symbols.Content, "main (function) line 1:1") {
		t.Fatalf("symbols output = %q", symbols.Content)
	}
	if !strings.Contains(symbols.Content, "helper (method) line 2:1") {
		t.Fatalf("symbols output lost the child symbol: %q", symbols.Content)
	}

	filtered := runLSPTool(t, NewLSPSymbolsTool(setup.bridge), setup.args, map[string]any{"path": "main.go", "query": "HELP"})
	if filtered.IsError || !strings.Contains(filtered.Content, "helper") || strings.Contains(filtered.Content, "main (function)") {
		t.Fatalf("filtered symbols = %+v", filtered)
	}

	definition := runLSPTool(t, NewLSPDefinitionTool(setup.bridge), setup.args, map[string]any{
		"path": "main.go", "line": 1, "character": 2,
	})
	if definition.IsError {
		t.Fatalf("lsp_definition failed: %s", definition.Content)
	}
	if !strings.Contains(definition.Content, "definition for main.go at 1:2: 1") {
		t.Fatalf("definition output = %q", definition.Content)
	}
	if !strings.Contains(definition.Content, "- main.go:2:3") {
		t.Fatalf("definition location was not rendered 1-based/relative: %q", definition.Content)
	}

	// All three tools share one client for the same server/language/root.
	setup.bridge.mu.Lock()
	clientCount := len(setup.bridge.clients)
	setup.bridge.mu.Unlock()
	if clientCount != 1 {
		t.Fatalf("cached clients = %d, want 1 reused client", clientCount)
	}

	missing := runLSPTool(t, NewLSPDiagnosticsTool(setup.bridge), setup.args, map[string]any{"path": "does-not-exist.go"})
	if !missing.IsError || !strings.Contains(missing.Content, "read does-not-exist.go") {
		t.Fatalf("missing file result = %+v", missing)
	}
}

func TestLSPBridgeNoServerForLanguage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := lsp.NewConfig()
	if err := cfg.Add(lsp.ServerConfig{
		Name:        "disabled-lsp",
		Command:     "definitely-not-started",
		LanguageIDs: []string{"go"},
		Enabled:     false,
	}); err != nil {
		t.Fatal(err)
	}
	bridge := NewLSPBridge(cfg)
	t.Cleanup(func() { _ = bridge.Close() })
	args := DispatchArgs{RootPath: &root}

	result := runLSPTool(t, NewLSPDiagnosticsTool(bridge), args, map[string]any{"path": "main.go"})
	if !result.IsError || !strings.Contains(result.Content, `no enabled language server configured for language "go"`) {
		t.Fatalf("no-server result = %+v", result)
	}

	result = runLSPTool(t, NewLSPSymbolsTool(bridge), args, map[string]any{"path": "notes.xyz"})
	if !result.IsError || !strings.Contains(result.Content, "no language ID mapping") {
		t.Fatalf("unknown-extension result = %+v", result)
	}

	result = runLSPTool(t, NewLSPDefinitionTool(bridge), args, map[string]any{"path": "../escape.go", "line": 1, "character": 1})
	if !result.IsError || !strings.Contains(result.Content, "must be relative") {
		t.Fatalf("path traversal result = %+v", result)
	}

	result = runLSPTool(t, NewLSPDefinitionTool(bridge), args, map[string]any{"path": "main.go", "line": 0, "character": 1})
	if !result.IsError || !strings.Contains(result.Content, "1-based") {
		t.Fatalf("bad position result = %+v", result)
	}
}

func TestLSPBridgeProtocolError(t *testing.T) {
	setup := newLSPTestSetup(t, []string{"go"}, map[string]string{
		lspfake.ErrorMethodEnv: "textDocument/documentSymbol",
	})

	result := runLSPTool(t, NewLSPSymbolsTool(setup.bridge), setup.args, map[string]any{"path": "main.go"})
	if !result.IsError {
		t.Fatalf("protocol error was returned as success: %+v", result)
	}
	if !strings.Contains(result.Content, "fake protocol error") {
		t.Fatalf("protocol error text = %q", result.Content)
	}
	if !strings.Contains(result.Content, "RPC error") {
		t.Fatalf("RPC error was not described clearly: %q", result.Content)
	}
}

func TestLSPBridgeCancellation(t *testing.T) {
	setup := newLSPTestSetup(t, []string{"go"}, map[string]string{
		lspfake.SleepMethodEnv: "textDocument/documentSymbol",
	})

	ctx, cancel := context.WithCancel(context.Background())
	tool := NewLSPSymbolsTool(setup.bridge)
	raw := json.RawMessage(`{"path":"main.go"}`)
	done := make(chan struct {
		result ToolResult
		err    error
	}, 1)

	go func() {
		result, err := tool.Run(ctx, setup.args, raw)
		done <- struct {
			result ToolResult
			err    error
		}{result, err}
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("cancellation leaked a transport error: %v", got.err)
		}
		if !got.result.IsError || !strings.Contains(got.result.Content, "canceled") {
			t.Fatalf("cancellation result = %+v", got.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tool did not return after context cancellation")
	}
}

func TestLSPBridgeCancellationDuringStartup(t *testing.T) {
	setup := newLSPTestSetup(t, []string{"go"}, map[string]string{
		lspfake.SleepMethodEnv: "initialize",
	})

	ctx, cancel := context.WithCancel(context.Background())
	tool := NewLSPDiagnosticsTool(setup.bridge)
	raw := json.RawMessage(`{"path":"main.go"}`)
	done := make(chan struct {
		result ToolResult
		err    error
	}, 1)

	go func() {
		result, err := tool.Run(ctx, setup.args, raw)
		done <- struct {
			result ToolResult
			err    error
		}{result, err}
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("startup cancellation leaked a transport error: %v", got.err)
		}
		if !got.result.IsError || !strings.Contains(got.result.Content, "canceled") {
			t.Fatalf("startup cancellation result = %+v", got.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tool did not return after startup cancellation")
	}

	setup.bridge.mu.Lock()
	cached := len(setup.bridge.clients)
	setup.bridge.mu.Unlock()
	if cached != 0 {
		t.Fatalf("failed startup left %d cached clients", cached)
	}
}

func TestLSPBridgeShutdownLifecycle(t *testing.T) {
	setup := newLSPTestSetup(t, []string{"go"}, nil)
	if result := runLSPTool(t, NewLSPDiagnosticsTool(setup.bridge), setup.args, map[string]any{"path": "main.go"}); result.IsError {
		t.Fatalf("setup diagnostics failed: %s", result.Content)
	}

	setup.bridge.mu.Lock()
	var done <-chan struct{}
	for _, entry := range setup.bridge.clients {
		done = entry.client.Done()
	}
	setup.bridge.mu.Unlock()
	if done == nil {
		t.Fatal("no language server client was cached")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := setup.bridge.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("language server process did not stop after Shutdown")
	}
	setup.bridge.mu.Lock()
	remaining := len(setup.bridge.clients)
	setup.bridge.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("cached clients after Shutdown = %d, want 0", remaining)
	}

	// Shutdown is idempotent, and a closed bridge refuses new work.
	if err := setup.bridge.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	result := runLSPTool(t, NewLSPDiagnosticsTool(setup.bridge), setup.args, map[string]any{"path": "main.go"})
	if !result.IsError || !strings.Contains(result.Content, "closed") {
		t.Fatalf("tool after Shutdown = %+v", result)
	}
}
