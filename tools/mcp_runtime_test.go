package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"GoWork/internal/mcp"
	"GoWork/internal/netfetch"
	"GoWork/providers"
)

// TestMCPRuntimeHelperProcess turns the test binary into a small stdio MCP
// server when the runtime starts it with GO_WANT_TOOLS_MCP_HELPER=1.
func TestMCPRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TOOLS_MCP_HELPER") != "1" {
		return
	}
	serveRuntimeFakeServer(os.Stdin, os.Stdout)
	os.Exit(0)
}

func serveRuntimeFakeServer(in io.Reader, out io.Writer) {
	if os.Getenv("MCP_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "fatal: token rejected:", os.Getenv("MCP_HELPER_FAIL_SECRET"))
		os.Exit(1)
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	encoder := json.NewEncoder(out)

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &msg) != nil || msg.Method == "" {
			continue
		}
		hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
		respond := func(result any) {
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result":  result,
			})
		}
		fail := func(code int, message string) {
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"error":   map[string]any{"code": code, "message": message},
			})
		}

		switch msg.Method {
		case "initialize":
			if !hasID {
				continue
			}
			respond(map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "runtime-fake", "version": "1.0.0"},
			})
		case "notifications/initialized":
			continue
		case "ping":
			respond(map[string]any{})
		case "tools/list":
			respond(map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo text back",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required":   []string{"text"},
					},
				}},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if params.Name != "echo" {
				fail(-32602, "unknown tool")
				continue
			}
			text := fmt.Sprint(params.Arguments["text"])
			if text == "cwd" {
				if cwd, err := os.Getwd(); err == nil {
					text = cwd
				}
			}
			respond(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + text}},
			})
		default:
			if hasID {
				fail(-32601, "method not found")
			}
		}
	}
}

func runtimeStdioServer(name, cwd string) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:            name,
		Command:         os.Args[0],
		Args:            []string{"-test.run=^TestMCPRuntimeHelperProcess$"},
		CWD:             cwd,
		Env:             map[string]string{"GO_WANT_TOOLS_MCP_HELPER": "1", "MCP_HELPER_SECRET": "env-secret-value"},
		StartupTimeout:  mcp.Duration(5 * time.Second),
		RequestTimeout:  mcp.Duration(5 * time.Second),
		ShutdownTimeout: mcp.Duration(2 * time.Second),
	}
}

func writeRuntimeConfig(t *testing.T, path string, servers ...mcp.ServerConfig) {
	t.Helper()
	cfg := &mcp.Config{}
	for _, server := range servers {
		if err := cfg.AddServer(server); err != nil {
			t.Fatalf("AddServer(%s): %v", server.Name, err)
		}
	}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save(%s): %v", path, err)
	}
}

func findProviderDef(name string) bool {
	for _, def := range providers.ToolDefs() {
		if def.Name == name {
			return true
		}
	}
	return false
}

func TestMCPRuntimeDynamicStdioRegistration(t *testing.T) {
	root := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, ".mcp.json")
	serverCWD := filepath.Join(root, "server-cwd")
	if err := os.MkdirAll(serverCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRuntimeConfig(t, configPath, runtimeStdioServer("fake", serverCWD))

	dispatcher, err := InitDispacher(root, nil, nil)
	if err != nil {
		t.Fatalf("InitDispacher: %v", err)
	}
	runtime := NewMCPRuntime(MCPRuntimeOptions{
		ConfigPath:  configPath,
		ProjectRoot: root,
		Dispatcher:  dispatcher,
	})
	defer func() { _ = runtime.Close() }()

	if err := runtime.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := runtime.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	toolName := MCPToolName("fake", "echo")
	tool, ok := dispatcher.Lookup(toolName)
	if !ok {
		t.Fatalf("tool %q not registered", toolName)
	}
	if tool.Kind() != KindNotAllowed {
		t.Errorf("untrusted user server kind = %v, want KindNotAllowed", tool.Kind())
	}
	if !findProviderDef(toolName) {
		t.Fatalf("provider definition %q not registered", toolName)
	}

	result := dispatcher.Dispach(context.Background(), ToolUse{
		Name:  toolName,
		Input: json.RawMessage(`{"text":"hello"}`),
	})
	if result.IsError || result.Content != "echo: hello" {
		t.Fatalf("dispatch result = %+v", result)
	}

	// stdio cmd.Dir must be the configured cwd.
	result = dispatcher.Dispach(context.Background(), ToolUse{
		Name:  toolName,
		Input: json.RawMessage(`{"text":"cwd"}`),
	})
	if result.IsError || strings.TrimSpace(result.Content) != "echo: "+serverCWD {
		t.Fatalf("cwd result = %+v, want %q", result, "echo: "+serverCWD)
	}

	servers := runtime.List()
	if len(servers) != 1 || servers[0].Name != "fake" || !servers[0].Connected || servers[0].Tools != 1 {
		t.Fatalf("List = %+v", servers)
	}
	if servers[0].Error != "" {
		t.Fatalf("unexpected server error: %s", servers[0].Error)
	}

	if err := runtime.Remove("fake"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := dispatcher.Lookup(toolName); ok {
		t.Fatalf("tool %q still registered after Remove", toolName)
	}
	if findProviderDef(toolName) {
		t.Fatalf("provider definition %q still registered after Remove", toolName)
	}
	if got := runtime.List(); len(got) != 0 {
		t.Fatalf("servers after Remove = %+v", got)
	}
}

func TestMCPRuntimeStartupFailureIsolated(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	bad := mcp.ServerConfig{
		Name:            "broken",
		Command:         filepath.Join(t.TempDir(), "does-not-exist"),
		StartupTimeout:  mcp.Duration(2 * time.Second),
		ShutdownTimeout: mcp.Duration(1 * time.Second),
	}
	writeRuntimeConfig(t, configPath, runtimeStdioServer("good", ""), bad)

	dispatcher, err := InitDispacher(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: root, Dispatcher: dispatcher})
	defer func() { _ = runtime.Close() }()

	startErr := runtime.StartAll(context.Background())
	if startErr == nil {
		t.Fatal("StartAll returned nil with a broken server")
	}

	servers := runtime.List()
	if len(servers) != 2 {
		t.Fatalf("List = %+v, want both config entries", servers)
	}
	var good, broken *MCPServerInfo
	for i := range servers {
		switch servers[i].Name {
		case "good":
			good = &servers[i]
		case "broken":
			broken = &servers[i]
		}
	}
	if good == nil || !good.Connected || good.Tools != 1 {
		t.Fatalf("good server = %+v", good)
	}
	if broken == nil || broken.Error == "" {
		t.Fatalf("broken server = %+v, want an error", broken)
	}
	// The failed entry stays configured for a later retry.
	if server, ok := readConfigServer(t, configPath, "broken"); !ok || server.Name != "broken" {
		t.Fatalf("broken server config was removed: %+v", server)
	}
	if _, ok := dispatcher.Lookup(MCPToolName("good", "echo")); !ok {
		t.Fatal("good server tools were not registered")
	}
}

func TestMCPRuntimeStartupErrorsRedactSecrets(t *testing.T) {
	const secret = "stderr-secret-token"
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	server := runtimeStdioServer("leaky", "")
	server.Env["MCP_HELPER_FAIL"] = "1"
	server.Env["MCP_HELPER_FAIL_SECRET"] = secret
	writeRuntimeConfig(t, configPath, server)

	runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: t.TempDir()})
	defer func() { _ = runtime.Close() }()

	err := runtime.StartAll(context.Background())
	if err == nil {
		t.Fatal("StartAll succeeded with a failing server")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("startup error leaked the stderr secret: %v", err)
	}
	servers := runtime.List()
	if len(servers) != 1 || servers[0].Error == "" {
		t.Fatalf("List = %+v, want a recorded failure", servers)
	}
	if strings.Contains(servers[0].Error, secret) {
		t.Fatalf("List error leaked the stderr secret: %q", servers[0].Error)
	}
}

func readConfigServer(t *testing.T, path, name string) (mcp.ServerConfig, bool) {
	t.Helper()
	cfg, err := mcp.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg.Server(name)
}

func TestMCPRuntimeInvalidConfigDoesNotFailStartup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: t.TempDir()})
	defer func() { _ = runtime.Close() }()

	if err := runtime.Init(context.Background()); err == nil {
		t.Fatal("Init accepted a malformed config")
	}
	if runtime.ConfigError() == "" {
		t.Fatal("ConfigError is empty")
	}
	// No configured servers means nothing can block app startup.
	if err := runtime.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll with malformed config: %v", err)
	}
	if got := runtime.List(); len(got) != 0 {
		t.Fatalf("List = %+v", got)
	}
}

type runtimeTestFetcher struct {
	client *http.Client
}

func (f runtimeTestFetcher) Fetch(ctx context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &netfetch.Response{
		URL:        rawURL,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func TestMCPRuntimeInstallFromHTTPTestConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	server := runtimeStdioServer("downloaded", "")
	document, err := json.Marshal(map[string]any{
		"servers": map[string]any{"downloaded": server},
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document)
	}))
	defer httpServer.Close()

	dispatcher, err := InitDispacher(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewMCPRuntime(MCPRuntimeOptions{
		ConfigPath:  configPath,
		ProjectRoot: root,
		Dispatcher:  dispatcher,
		Fetcher:     runtimeTestFetcher{client: httpServer.Client()},
	})
	defer func() { _ = runtime.Close() }()

	result, err := runtime.Install(context.Background(), MCPInstallOptions{
		Source: httpServer.URL + "/mcp.json",
		Scope:  MCPScopeUser,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Servers != 1 || len(result.Installed) != 1 || result.Installed[0] != "downloaded" {
		t.Fatalf("Install result = %+v", result)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("installed config missing: %v", err)
	}
	if info, err := os.Stat(configPath); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}
	if _, ok := readConfigServer(t, configPath, "downloaded"); !ok {
		t.Fatal("downloaded server not saved")
	}

	if err := runtime.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll after install: %v", err)
	}
	name := MCPToolName("downloaded", "echo")
	if _, ok := dispatcher.Lookup(name); !ok {
		t.Fatalf("installed tool %q not registered", name)
	}
}

func TestMCPRuntimeInstallDryRunWritesNothing(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: t.TempDir()})
	defer func() { _ = runtime.Close() }()

	server := runtimeStdioServer("dry", "")
	document, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"dry": server}})
	result, err := runtime.Install(context.Background(), MCPInstallOptions{
		Config: document,
		DryRun: true,
		Start:  true,
	})
	if err != nil {
		t.Fatalf("dry-run Install: %v", err)
	}
	if !result.DryRun || result.Servers != 1 {
		t.Fatalf("dry-run result = %+v", result)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote config (stat err = %v)", err)
	}
	if got := runtime.List(); len(got) != 0 {
		t.Fatalf("dry run changed runtime state: %+v", got)
	}
}

func TestMCPRuntimeInstallRejectsPrivateServerURL(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: t.TempDir()})
	defer func() { _ = runtime.Close() }()

	document := []byte(`{"mcpServers":{"internal":{"url":"http://127.0.0.1:9999/mcp"}}}`)
	_, err := runtime.Install(context.Background(), MCPInstallOptions{Config: document})
	if err == nil || !strings.Contains(err.Error(), "allowPrivate") {
		t.Fatalf("Install error = %v, want private URL rejection", err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("rejected install wrote config (stat err = %v)", statErr)
	}
}

func TestMCPRuntimeInstallErrorsDoNotLeakSecrets(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: t.TempDir()})
	defer func() { _ = runtime.Close() }()

	const secret = "super-secret-env-value"
	document := []byte(`{"mcpServers":{"bad":{"command":"echo","url":"https://example.com","env":{"TOKEN":"` + secret + `"}}}}`)
	_, err := runtime.Install(context.Background(), MCPInstallOptions{Config: document})
	if err == nil {
		t.Fatal("Install accepted an invalid server")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("install error leaked an env value: %v", err)
	}

	valid := []byte(`{"mcpServers":{"local":{"command":"echo","env":{"TOKEN":"` + secret + `"},"headers":{"Authorization":"Bearer ` + secret + `"}}}}`)
	result, err := runtime.Install(context.Background(), MCPInstallOptions{Config: valid})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", result), secret) {
		t.Fatalf("install result leaked a secret: %+v", result)
	}
	for _, server := range runtime.List() {
		if strings.Contains(fmt.Sprintf("%+v", server), secret) {
			t.Fatalf("List leaked a secret: %+v", server)
		}
	}
}

func TestMCPRuntimeTrustedUserServerButNeverProjectScope(t *testing.T) {
	root := t.TempDir()
	server := runtimeStdioServer("trusted", "")
	server.Trusted = true
	server.AutoApprove = true

	t.Run("user", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), ".mcp.json")
		writeRuntimeConfig(t, configPath, server)
		dispatcher, err := InitDispacher(root, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewMCPRuntime(MCPRuntimeOptions{ConfigPath: configPath, ProjectRoot: root, Dispatcher: dispatcher})
		defer func() { _ = runtime.Close() }()
		if err := runtime.StartAll(context.Background()); err != nil {
			t.Fatalf("StartAll: %v", err)
		}
		tool, ok := dispatcher.Lookup(MCPToolName("trusted", "echo"))
		if !ok {
			t.Fatal("trusted tool missing")
		}
		if tool.Kind() != KindAllowed {
			t.Fatalf("trusted user tool kind = %v, want KindAllowed", tool.Kind())
		}
	})

	t.Run("project", func(t *testing.T) {
		projectPath := filepath.Join(root, ".mcp.json")
		writeRuntimeConfig(t, projectPath, server)
		dispatcher, err := InitDispacher(root, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewMCPRuntime(MCPRuntimeOptions{
			ConfigPath:        filepath.Join(t.TempDir(), ".mcp.json"),
			ProjectRoot:       root,
			ProjectConfigPath: projectPath,
			Dispatcher:        dispatcher,
		})
		defer func() { _ = runtime.Close() }()
		if err := runtime.StartAll(context.Background()); err != nil {
			t.Fatalf("StartAll: %v", err)
		}
		tool, ok := dispatcher.Lookup(MCPToolName("trusted", "echo"))
		if !ok {
			t.Fatal("project tool missing")
		}
		if tool.Kind() != KindNotAllowed {
			t.Fatalf("project-scope tool kind = %v, want KindNotAllowed", tool.Kind())
		}
	})
}

type fakeRuntimeClient struct {
	mu     sync.Mutex
	tools  []mcp.Tool
	closed bool
}

func (f *fakeRuntimeClient) Start(context.Context) error { return nil }
func (f *fakeRuntimeClient) ListTools(context.Context) ([]mcp.Tool, error) {
	return append([]mcp.Tool(nil), f.tools...), nil
}
func (f *fakeRuntimeClient) CallTool(context.Context, string, any) (*mcp.CallResult, error) {
	return &mcp.CallResult{Text: "ok"}, nil
}
func (f *fakeRuntimeClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
