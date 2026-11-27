package gowork

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	toolstab "GoWork/Tui/Components/ToolsTab"
	"GoWork/internal/lsp"
	"GoWork/internal/mcp"
	"GoWork/tools"
)

type fakeMCPClient struct {
	started  bool
	closed   bool
	startErr error
	listErr  error
	tools    []mcp.Tool
	calls    int
}

func (f *fakeMCPClient) Start(context.Context) error {
	f.started = true
	return f.startErr
}

func (f *fakeMCPClient) ListTools(context.Context) ([]mcp.Tool, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.tools, nil
}

func (f *fakeMCPClient) CallTool(context.Context, string, any) (*mcp.CallResult, error) {
	f.calls++
	return &mcp.CallResult{Text: "ok"}, nil
}

func (f *fakeMCPClient) Close() error {
	f.closed = true
	return nil
}

func fakeTools() []mcp.Tool {
	return []mcp.Tool{{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: []byte(`{"type":"object"}`),
	}}
}

func writeMCPConfig(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write MCP config: %v", err)
	}
}

func TestToolRuntimeLoadsConfiguredMCP(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	writeMCPConfig(t, rt.mcpPath, `{"mcpServers":{
		"fake":{"command":"fake-server"},
		"off":{"command":"off-server","disabled":true}
	}}`)

	var clients []*fakeMCPClient
	rt.newMCPClient = func(cfg mcp.ServerConfig) (mcpClient, error) {
		client := &fakeMCPClient{tools: fakeTools()}
		clients = append(clients, client)
		return client, nil
	}
	rt.Load()

	if len(clients) != 1 || !clients[0].started {
		t.Fatalf("clients = %+v, want one started client", clients)
	}
	if _, ok := rt.mcpClients["off"]; ok {
		t.Fatal("disabled server was connected")
	}
	name := tools.MCPToolName("fake", "echo")
	if _, ok := rt.mcpTools["fake"]; !ok {
		t.Fatalf("MCP tools missing for fake server: %+v", rt.mcpTools)
	}
	var found bool
	for _, tool := range rt.Tools() {
		if tool.Name() == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("Tools() missing %q", name)
	}
	found = false
	for _, def := range rt.ToolDefs() {
		if def.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("ToolDefs() missing %q", name)
	}

	servers := rt.MCPServers()
	if len(servers) != 2 {
		t.Fatalf("MCPServers = %+v", servers)
	}
	if !servers[0].Connected || servers[0].Tools != 1 || servers[0].Error != "" {
		t.Fatalf("fake status = %+v", servers[0])
	}
	if servers[1].Enabled || servers[1].Connected {
		t.Fatalf("disabled status = %+v", servers[1])
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !clients[0].closed {
		t.Fatal("MCP client was not closed")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestToolRuntimeAddToggleRemoveMCP(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	rt.newMCPClient = func(cfg mcp.ServerConfig) (mcpClient, error) {
		return &fakeMCPClient{tools: fakeTools()}, nil
	}
	rt.Load()

	msg := toolstab.AddMCPMsg{Name: "files", Command: "npx", Args: []string{"-y", "pkg"}}
	if err := rt.AddMCP(msg); err != nil {
		t.Fatalf("AddMCP: %v", err)
	}
	cfg, err := mcp.LoadConfig(rt.mcpPath)
	if err != nil {
		t.Fatalf("load saved MCP config: %v", err)
	}
	server, ok := cfg.Server("files")
	if !ok || server.Command != "npx" || strings.Join(server.Args, " ") != "-y pkg" {
		t.Fatalf("saved server = %+v, %v", server, ok)
	}
	if len(rt.mcpTools["files"]) != 1 {
		t.Fatalf("registered tools = %+v", rt.mcpTools["files"])
	}

	client := rt.mcpClients["files"].(*fakeMCPClient)
	if err := rt.SetMCPEnabled("files", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !client.closed {
		t.Fatal("disabling did not close the client")
	}
	if len(rt.mcpTools["files"]) != 0 {
		t.Fatal("disabled server still exposes tools")
	}
	cfg, _ = mcp.LoadConfig(rt.mcpPath)
	if server, _ := cfg.Server("files"); server.IsEnabled() {
		t.Fatal("disabled state was not persisted")
	}

	if err := rt.SetMCPEnabled("files", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if rt.mcpClients["files"] == nil || len(rt.mcpTools["files"]) != 1 {
		t.Fatal("re-enabled server did not reconnect")
	}

	if err := rt.RemoveMCP("files"); err != nil {
		t.Fatalf("RemoveMCP: %v", err)
	}
	cfg, _ = mcp.LoadConfig(rt.mcpPath)
	if _, ok := cfg.Server("files"); ok {
		t.Fatal("removed server stayed in the config")
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("tools after remove = %+v", rt.Tools())
	}
}

func TestToolRuntimeRecordsMCPFailures(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	writeMCPConfig(t, rt.mcpPath, `{"mcpServers":{"bad":{"command":"missing"}}}`)
	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) {
		return nil, errors.New("boom")
	}
	rt.Load()

	servers := rt.MCPServers()
	if len(servers) != 1 || servers[0].Error == "" || servers[0].Connected {
		t.Fatalf("failure status = %+v", servers)
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("failed server exposed tools: %+v", rt.Tools())
	}

	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) {
		return &fakeMCPClient{listErr: errors.New("list failed")}, nil
	}
	if err := rt.SetMCPEnabled("bad", true); err == nil {
		t.Fatal("re-enabling a failing server returned nil error")
	}
	servers = rt.MCPServers()
	if servers[0].Error == "" {
		t.Fatalf("list failure not recorded: %+v", servers)
	}
	_ = rt.Close()
}

func TestToolRuntimeInvalidMCPConfigIsReported(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	writeMCPConfig(t, rt.mcpPath, `{"mcpServers":`)
	rt.Load()
	if mcpErr, _ := rt.Errors(); mcpErr == "" {
		t.Fatal("invalid MCP config produced no error")
	}
	if len(rt.MCPServers()) != 0 {
		t.Fatalf("invalid config produced servers: %+v", rt.MCPServers())
	}

	// Adding a valid server rewrites the broken file successfully.
	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) {
		return &fakeMCPClient{tools: fakeTools()}, nil
	}
	if err := rt.AddMCP(toolstab.AddMCPMsg{Name: "files", Command: "npx"}); err != nil {
		t.Fatalf("AddMCP over broken config: %v", err)
	}
	if mcpErr, _ := rt.Errors(); mcpErr != "" {
		t.Fatalf("MCP error not cleared after save: %q", mcpErr)
	}
	_ = rt.Close()
}

func TestToolRuntimeLSPLifecycle(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	rt.Load()
	if rt.lspBridge != nil {
		t.Fatal("empty LSP config created a bridge")
	}

	if err := rt.AddLSP(toolstab.AddLSPMsg{Name: "gopls", Command: "gopls", Languages: []string{"go"}}); err != nil {
		t.Fatalf("AddLSP: %v", err)
	}
	data, err := os.ReadFile(rt.lspPath)
	if err != nil {
		t.Fatalf("read LSP config: %v", err)
	}
	cfg, err := lsp.ParseConfig(data)
	if err != nil {
		t.Fatalf("parse saved LSP config: %v", err)
	}
	if server, ok := cfg.Get("gopls"); !ok || !server.Enabled || len(server.LanguageIDs) != 1 {
		t.Fatalf("saved LSP server = %+v, %v", server, ok)
	}
	if rt.lspBridge == nil {
		t.Fatal("enabled LSP server did not create a bridge")
	}
	toolNames := map[string]bool{}
	for _, tool := range rt.Tools() {
		toolNames[tool.Name()] = true
	}
	for _, want := range []string{"lsp_diagnostics", "lsp_symbols", "lsp_definition"} {
		if !toolNames[want] {
			t.Fatalf("LSP tools missing %q: %+v", want, toolNames)
		}
	}

	if err := rt.SetLSPEnabled("gopls", false); err != nil {
		t.Fatalf("disable LSP: %v", err)
	}
	if rt.lspBridge != nil {
		t.Fatal("disabled LSP server kept its bridge")
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("disabled LSP server exposed tools: %+v", rt.Tools())
	}

	if err := rt.Configure("gopls"); err != nil {
		t.Fatalf("Configure existing server: %v", err)
	}
	if rt.lspBridge == nil {
		t.Fatal("Configure did not re-enable the server")
	}

	if err := rt.RemoveLSP("gopls"); err != nil {
		t.Fatalf("RemoveLSP: %v", err)
	}
	data, _ = os.ReadFile(rt.lspPath)
	cfg, err = lsp.ParseConfig(data)
	if err != nil {
		t.Fatalf("parse config after remove: %v", err)
	}
	if cfg.Len() != 0 {
		t.Fatalf("removed LSP server stayed configured: %+v", cfg.List())
	}

	if err := rt.AddLSP(toolstab.AddLSPMsg{Name: "bad", Command: "x"}); err == nil {
		t.Fatal("LSP server without language IDs was accepted")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestToolRuntimeMissingLSPCommandIsReported(t *testing.T) {
	rt := newToolRuntime(t.TempDir(), t.TempDir())
	rt.Load()
	if err := rt.AddLSP(toolstab.AddLSPMsg{
		Name:      "missing",
		Command:   "definitely-not-a-real-lsp-binary-xyz",
		Languages: []string{"go"},
	}); err != nil {
		t.Fatalf("AddLSP: %v", err)
	}
	servers := rt.LSPServers()
	if len(servers) != 1 || !strings.Contains(servers[0].Error, "not found") {
		t.Fatalf("missing command not reported: %+v", servers)
	}
	if len(rt.Tools()) == 0 {
		t.Fatal("an unavailable language server should still register its lazy tools")
	}
	if err := rt.SetLSPEnabled("missing", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if servers = rt.LSPServers(); servers[0].Error != "" {
		t.Fatalf("disabled server kept its error: %+v", servers)
	}
	_ = rt.Close()
}

func TestToolRuntimeInvalidLSPConfigIsReported(t *testing.T) {
	rt := newToolRuntime(t.TempDir(), t.TempDir())
	if err := os.WriteFile(rt.lspPath, []byte(`{"servers":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.Load()
	if _, lspErr := rt.Errors(); lspErr == "" {
		t.Fatal("invalid LSP config produced no error")
	}
	if len(rt.LSPServers()) != 0 {
		t.Fatalf("invalid LSP config produced servers: %+v", rt.LSPServers())
	}
}

func TestRebuildToolRegistryAddsAndRemovesRuntimeTools(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) {
		return &fakeMCPClient{tools: fakeTools()}, nil
	}
	rt.Load()
	if err := rt.AddMCP(toolstab.AddMCPMsg{Name: "files", Command: "npx"}); err != nil {
		t.Fatalf("AddMCP: %v", err)
	}
	if err := rt.AddLSP(toolstab.AddLSPMsg{Name: "gopls", Command: "gopls", Languages: []string{"go"}}); err != nil {
		t.Fatalf("AddLSP: %v", err)
	}

	think := false
	m := model{
		root:        t.TempDir(),
		toolRuntime: rt,
		baseTools:   []tools.AgentTool{},
		AiThink:     &think,
		planMode:    true,
	}
	if err := m.rebuildToolRegistry(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if m.tool_dispatcher == nil {
		t.Fatal("rebuild left no dispatcher")
	}
	if !m.tool_dispatcher.PlanMode() {
		t.Fatal("plan mode did not carry over to the rebuilt dispatcher")
	}
	for _, tool := range rt.Tools() {
		if _, ok := m.tool_dispatcher.Lookup(tool.Name()); !ok {
			t.Fatalf("dispatcher missing runtime tool %q", tool.Name())
		}
	}

	if err := rt.SetMCPEnabled("files", false); err != nil {
		t.Fatalf("disable MCP: %v", err)
	}
	if err := rt.SetLSPEnabled("gopls", false); err != nil {
		t.Fatalf("disable LSP: %v", err)
	}
	if err := m.rebuildToolRegistry(); err != nil {
		t.Fatalf("rebuild after disable: %v", err)
	}
	if len(rt.Tools()) != 0 {
		t.Fatalf("runtime tools after disable = %+v", rt.Tools())
	}
	_ = rt.Close()
}

func TestFlushSessionClosesToolClients(t *testing.T) {
	rt := newToolRuntime(t.TempDir(), t.TempDir())
	client := &fakeMCPClient{tools: fakeTools()}
	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) { return client, nil }
	rt.Load()
	if err := rt.AddMCP(toolstab.AddMCPMsg{Name: "files", Command: "npx"}); err != nil {
		t.Fatalf("AddMCP: %v", err)
	}

	m := model{toolRuntime: rt}
	m.flushSession()
	if !client.closed {
		t.Fatal("flushSession did not close MCP clients")
	}
}
