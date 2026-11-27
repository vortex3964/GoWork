package test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"GoWork/internal/mcp"
	"GoWork/internal/netfetch"
	"GoWork/tools"
	mcptool "GoWork/tools/MCPTool"
	skillstool "GoWork/tools/SkillsTool"
)

// agenticFetcher adapts a test HTTP client to remotesource.Fetcher. The
// production netfetch guard intentionally blocks loopback addresses, so tests
// point at httptest through this narrow seam.
type agenticFetcher struct {
	client *http.Client
}

func (f agenticFetcher) Fetch(ctx context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &netfetch.StatusError{Code: resp.StatusCode, Status: resp.Status, Header: resp.Header.Clone()}
	}
	return &netfetch.Response{
		URL:        rawURL,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func agenticSkill(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
}

func useAgenticSkillManager(t *testing.T) *skillstool.Manager {
	t.Helper()
	old := skillstool.GetManager()
	mgr := skillstool.NewManagerAt(t.TempDir())
	skillstool.SetManager(mgr)
	t.Cleanup(func() { skillstool.SetManager(old) })
	return mgr
}

func runAgenticTool(t *testing.T, tool tools.AgentTool, root string, input any) tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s input: %v", tool.Name(), err)
	}
	result, err := tool.Run(context.Background(), tools.DispatchArgs{RootPath: &root}, raw)
	if err != nil {
		t.Fatalf("%s returned a Go error: %v", tool.Name(), err)
	}
	return result
}

func TestAgenticSkillInstallFromFakeGitHubAmbiguousDryRunLoadAndUse(t *testing.T) {
	const token = "ghp_agentsecret"
	alpha := agenticSkill("alpha", "Alpha skill.", "alpha body")
	beta := agenticSkill("beta", "Beta skill.", "beta body")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/skills":
			_, _ = io.WriteString(w, `{"default_branch":"main"}`)
		case r.URL.Path == "/repos/acme/skills/git/trees/main":
			// Deliberately unsorted: the tool must report sorted candidates.
			_, _ = io.WriteString(w, `{"sha":"main","truncated":false,"tree":[`+
				`{"path":"skills/beta/SKILL.md","mode":"100644","type":"blob","sha":"b","size":10},`+
				`{"path":"skills/alpha/SKILL.md","mode":"100644","type":"blob","sha":"a","size":10}`+
				`]}`)
		case r.URL.Path == "/repos/acme/skills/contents/skills/alpha/SKILL.md":
			_, _ = io.WriteString(w, alpha)
		case r.URL.Path == "/repos/acme/skills/contents/skills/beta/SKILL.md":
			_, _ = io.WriteString(w, beta)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	mgr := useAgenticSkillManager(t)
	install := skillstool.NewInstallToolWithOptions(skillstool.InstallToolOptions{
		Fetcher:    agenticFetcher{client: server.Client()},
		APIBaseURL: server.URL,
		Token:      token,
	})
	root := t.TempDir()

	ambiguous := runAgenticTool(t, install, root, map[string]any{"source": "acme/skills"})
	if !ambiguous.IsError {
		t.Fatalf("ambiguous source installed without path: %s", ambiguous.Content)
	}
	alphaAt := strings.Index(ambiguous.Content, "skills/alpha/SKILL.md")
	betaAt := strings.Index(ambiguous.Content, "skills/beta/SKILL.md")
	if alphaAt < 0 || betaAt < 0 || alphaAt > betaAt {
		t.Fatalf("candidate paths are missing or unsorted:\n%s", ambiguous.Content)
	}
	if !strings.Contains(ambiguous.Content, "next call:") {
		t.Fatalf("ambiguous error has no next call:\n%s", ambiguous.Content)
	}
	if strings.Contains(ambiguous.Content, token) {
		t.Fatalf("ambiguous error leaked the token:\n%s", ambiguous.Content)
	}
	if mgr.Exists("alpha") || mgr.Exists("beta") {
		t.Fatal("ambiguous install wrote a skill")
	}

	dry := runAgenticTool(t, install, root, map[string]any{
		"source":  "acme/skills",
		"path":    "skills/beta/SKILL.md",
		"dry_run": true,
	})
	if dry.IsError {
		t.Fatalf("dry run failed: %s", dry.Content)
	}
	if !strings.Contains(dry.Content, "verified:") || !strings.Contains(dry.Content, "nothing was written") ||
		!strings.Contains(dry.Content, "next call:") {
		t.Fatalf("dry-run result is not actionable:\n%s", dry.Content)
	}
	if mgr.Exists("beta") {
		t.Fatal("dry run installed the skill")
	}

	installed := runAgenticTool(t, install, root, map[string]any{
		"source": "acme/skills",
		"path":   "skills/beta/SKILL.md",
		"load":   true,
	})
	if installed.IsError {
		t.Fatalf("install failed: %s", installed.Content)
	}
	for _, want := range []string{
		`installed skill "beta"`,
		"loaded: yes",
		"verified: SKILL.md is on disk and discoverable",
	} {
		if !strings.Contains(installed.Content, want) {
			t.Errorf("install result missing %q:\n%s", want, installed.Content)
		}
	}
	if strings.Contains(installed.Content, token) {
		t.Fatalf("install result leaked the token:\n%s", installed.Content)
	}
	if !mgr.IsLoaded("beta") {
		t.Fatal("load=true did not load the installed skill")
	}

	loaded := runAgenticTool(t, skillstool.New(), root, map[string]any{"name": "beta"})
	if loaded.IsError || loaded.Content != beta {
		t.Fatalf("skill tool did not return the installed body: %+v", loaded)
	}

	// A new session keeps the file but resets the loaded set; skill_load can
	// load the persisted skill and the skill tool can then use it.
	restarted := skillstool.NewManagerAt(mgr.Dir())
	skillstool.SetManager(restarted)
	if restarted.IsLoaded("beta") {
		t.Fatal("new session unexpectedly kept the loaded set")
	}
	load := runAgenticTool(t, skillstool.NewLoadTool(), root, map[string]any{"action": "load", "name": "beta"})
	if load.IsError || !restarted.IsLoaded("beta") {
		t.Fatalf("skill_load after restart = %+v", load)
	}
	loaded = runAgenticTool(t, skillstool.New(), root, map[string]any{"name": "beta"})
	if loaded.IsError || loaded.Content != beta {
		t.Fatalf("skill tool after restart = %+v", loaded)
	}
}

type agenticMCPClient struct {
	mu       sync.Mutex
	tools    []mcp.Tool
	startErr error
	calls    int
	closed   bool
}

func (c *agenticMCPClient) Start(context.Context) error { return c.startErr }

func (c *agenticMCPClient) ListTools(context.Context) ([]mcp.Tool, error) {
	return append([]mcp.Tool(nil), c.tools...), nil
}

func (c *agenticMCPClient) CallTool(context.Context, string, any) (*mcp.CallResult, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &mcp.CallResult{Text: "echo: hi"}, nil
}

func (c *agenticMCPClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func agenticMCPTools() []mcp.Tool {
	return []mcp.Tool{{
		Name:        "echo",
		Description: "Echo text back.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
	}}
}

func agenticMCPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/mcp-configs":
			_, _ = io.WriteString(w, `{"default_branch":"main"}`)
		case r.URL.Path == "/repos/acme/mcp-configs/git/trees/main":
			_, _ = io.WriteString(w, `{"sha":"main","truncated":false,"tree":[`+
				`{"path":"configs/zeta.mcp.json","mode":"100644","type":"blob","sha":"z","size":10},`+
				`{"path":"configs/alpha.mcp.json","mode":"100644","type":"blob","sha":"a","size":10}`+
				`]}`)
		case r.URL.Path == "/repos/acme/mcp-configs/contents/configs/alpha.mcp.json":
			_, _ = io.WriteString(w, `{"mcpServers":{"alpha":{"command":"fake-server"}}}`)
		case r.URL.Path == "/repos/acme/mcp-configs/contents/configs/zeta.mcp.json":
			_, _ = io.WriteString(w, `{"mcpServers":{"echo":{"command":"fake-server","args":["--flag"]}}}`)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestAgenticMCPInstallConnectCallRestartAndRemove(t *testing.T) {
	server := httptest.NewServer(agenticMCPHandler())
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	root := t.TempDir()
	dispatcher, err := tools.InitDispacher(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &agenticMCPClient{tools: agenticMCPTools()}
	runtime := tools.NewMCPRuntime(tools.MCPRuntimeOptions{
		ConfigPath:  configPath,
		ProjectRoot: root,
		Dispatcher:  dispatcher,
		Fetcher:     agenticFetcher{client: server.Client()},
		APIBaseURL:  server.URL,
		ClientFactory: func(mcp.ServerConfig) (tools.MCPRuntimeClient, error) {
			return client, nil
		},
	})
	if err := runtime.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	install := mcptool.NewInstall(runtime)

	ambiguous := runAgenticTool(t, install, root, map[string]any{"source": "acme/mcp-configs"})
	if !ambiguous.IsError {
		t.Fatalf("ambiguous MCP source installed without path: %s", ambiguous.Content)
	}
	alphaAt := strings.Index(ambiguous.Content, "configs/alpha.mcp.json")
	zetaAt := strings.Index(ambiguous.Content, "configs/zeta.mcp.json")
	if alphaAt < 0 || zetaAt < 0 || alphaAt > zetaAt {
		t.Fatalf("MCP candidates are missing or unsorted:\n%s", ambiguous.Content)
	}
	if !strings.Contains(ambiguous.Content, "next call:") {
		t.Fatalf("ambiguous MCP error has no next call:\n%s", ambiguous.Content)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("ambiguous MCP install wrote config: %v", err)
	}

	dry := runAgenticTool(t, install, root, map[string]any{
		"source":  "acme/mcp-configs",
		"path":    "configs/zeta.mcp.json",
		"start":   true,
		"dry_run": true,
	})
	if dry.IsError || !strings.Contains(dry.Content, "next call:") {
		t.Fatalf("MCP dry run = %+v", dry)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("MCP dry run wrote config: %v", err)
	}

	installed := runAgenticTool(t, install, root, map[string]any{
		"source": "acme/mcp-configs",
		"path":   "configs/zeta.mcp.json",
		"start":  true,
	})
	if installed.IsError {
		t.Fatalf("MCP install failed: %s", installed.Content)
	}
	for _, want := range []string{
		`Verified: server "echo" connected, 1 tool(s) available.`,
		"next model turn",
	} {
		if !strings.Contains(installed.Content, want) {
			t.Errorf("MCP install result missing %q:\n%s", want, installed.Content)
		}
	}
	if len(runtime.List()) != 1 || !runtime.List()[0].Connected || runtime.List()[0].Tools != 1 {
		t.Fatalf("runtime list after install = %+v", runtime.List())
	}

	name := tools.MCPToolName("echo", "echo")
	tool, ok := dispatcher.Lookup(name)
	if !ok {
		t.Fatalf("installed MCP tool %q is not dispatchable", name)
	}
	if called := runAgenticMCPTool(t, tool); called != "echo: hi" {
		t.Fatalf("MCP tool call = %q, want %q", called, "echo: hi")
	}

	// Simulate a restart: the saved config must reconnect the server and
	// re-register the same tool without another install.
	if err := runtime.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	restartedDispatcher, err := tools.InitDispacher(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restartedClient := &agenticMCPClient{tools: agenticMCPTools()}
	restarted := tools.NewMCPRuntime(tools.MCPRuntimeOptions{
		ConfigPath:  configPath,
		ProjectRoot: root,
		Dispatcher:  restartedDispatcher,
		ClientFactory: func(mcp.ServerConfig) (tools.MCPRuntimeClient, error) {
			return restartedClient, nil
		},
	})
	if err := restarted.Init(context.Background()); err != nil {
		t.Fatalf("restart Init: %v", err)
	}
	if err := restarted.StartAll(context.Background()); err != nil {
		t.Fatalf("restart StartAll: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	servers := restarted.List()
	if len(servers) != 1 || servers[0].Name != "echo" || !servers[0].Connected || servers[0].Tools != 1 {
		t.Fatalf("restarted runtime did not reconnect the saved server: %+v", servers)
	}
	restartedTool, ok := restartedDispatcher.Lookup(name)
	if !ok {
		t.Fatalf("restarted runtime did not re-register %q", name)
	}
	if got := runAgenticMCPTool(t, restartedTool); got != "echo: hi" {
		t.Fatalf("restarted MCP tool call = %q", got)
	}

	removed := runAgenticTool(t, mcptool.NewRemove(restarted), root, map[string]any{"name": "echo"})
	if removed.IsError {
		t.Fatalf("mcp_remove failed: %s", removed.Content)
	}
	if _, ok := restartedDispatcher.Lookup(name); ok {
		t.Fatal("removed MCP tool stayed dispatchable")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after remove: %v", err)
	}
	if strings.Contains(string(data), `"echo"`) {
		t.Fatalf("removed server stayed in the config: %s", data)
	}
}

// runAgenticMCPTool dispatches one MCP bridge call without approval plumbing.
func runAgenticMCPTool(t *testing.T, tool tools.AgentTool) string {
	t.Helper()
	result, err := tool.Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("MCP tool Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("MCP tool result = %+v", result)
	}
	return result.Content
}

func TestAgenticFailedInstallsLeaveNothingAndDeniedFlowsDoNotWrite(t *testing.T) {
	root := t.TempDir()
	mgr := useAgenticSkillManager(t)

	// A provenance path that cannot be replaced must not leave a SKILL.md.
	sourceDir := filepath.Join(root, "catalog", "partial")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	partial := agenticSkill("partial", "Partial skill.", "partial body")
	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	blockedDir := filepath.Join(mgr.Dir(), "partial")
	if err := os.MkdirAll(filepath.Join(blockedDir, skillstool.ProvenanceFileName), 0o755); err != nil {
		t.Fatal(err)
	}
	install := skillstool.NewInstallTool()
	failed := runAgenticTool(t, install, root, map[string]any{"source": "catalog/partial/SKILL.md"})
	if !failed.IsError {
		t.Fatalf("install with a blocked provenance path succeeded: %s", failed.Content)
	}
	if _, err := os.Stat(filepath.Join(blockedDir, skillstool.SkillFileName)); !os.IsNotExist(err) {
		t.Fatalf("failed skill install left SKILL.md behind: %v", err)
	}

	// A startup failure rolls back the new config and restores the previous
	// one without leaving a process record behind.
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	runtime := tools.NewMCPRuntime(tools.MCPRuntimeOptions{
		ConfigPath:  configPath,
		ProjectRoot: root,
		ClientFactory: func(cfg mcp.ServerConfig) (tools.MCPRuntimeClient, error) {
			if cfg.Name == "broken" {
				return &agenticMCPClient{startErr: errors.New("boom")}, nil
			}
			return &agenticMCPClient{tools: agenticMCPTools()}, nil
		},
	})
	if err := runtime.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	mcpInstall := mcptool.NewInstall(runtime)
	good := runAgenticTool(t, mcpInstall, root, map[string]any{
		"config": map[string]any{"mcpServers": map[string]any{"good": map[string]any{"command": "fake-server"}}},
	})
	if good.IsError {
		t.Fatalf("seed MCP install failed: %s", good.Content)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	broken := runAgenticTool(t, mcpInstall, root, map[string]any{
		"config": map[string]any{"mcpServers": map[string]any{"broken": map[string]any{"command": "fake-server"}}},
		"start":  true,
	})
	if !broken.IsError {
		t.Fatalf("startup failure was reported as success: %s", broken.Content)
	}
	for _, want := range []string{"rolled back", "next call:"} {
		if !strings.Contains(broken.Content, want) {
			t.Errorf("startup failure missing %q:\n%s", want, broken.Content)
		}
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config disappeared after rollback: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("rollback changed the previous config:\nbefore: %s\nafter: %s", before, after)
	}
	if strings.Contains(string(after), `"broken"`) {
		t.Fatalf("failed server stayed in the config: %s", after)
	}
	for _, server := range runtime.List() {
		if server.Name == "broken" {
			t.Fatalf("failed server stayed in runtime state: %+v", server)
		}
	}

	// Plan mode must deny installs before they reach disk or the runtime.
	deniedRoot := t.TempDir()
	deniedSource := filepath.Join(deniedRoot, "catalog", "fresh")
	if err := os.MkdirAll(deniedSource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deniedSource, "SKILL.md"), []byte(agenticSkill("fresh", "Fresh skill.", "body")), 0o644); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := tools.InitDispacher(deniedRoot, nil, nil, install, mcpInstall)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.SetPlanMode(true)
	skillDenied := dispatcher.Dispach(context.Background(), tools.ToolUse{
		Name:  "skill_install",
		Input: json.RawMessage(`{"source":"catalog/fresh/SKILL.md"}`),
	})
	if !skillDenied.IsError || !strings.Contains(skillDenied.Content, "PLAN MODE") {
		t.Fatalf("plan mode did not deny skill_install: %+v", skillDenied)
	}
	if mgr.Exists("fresh") {
		t.Fatal("plan-mode denial installed a skill")
	}
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	mcpDenied := dispatcher.Dispach(context.Background(), tools.ToolUse{
		Name:  "mcp_install",
		Input: json.RawMessage(`{"config":{"mcpServers":{"denied":{"command":"fake-server"}}},"start":true}`),
	})
	if !mcpDenied.IsError || !strings.Contains(mcpDenied.Content, "PLAN MODE") {
		t.Fatalf("plan mode did not deny mcp_install: %+v", mcpDenied)
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(configBefore) != string(configAfter) {
		t.Fatal("plan-mode denial changed the MCP config")
	}
}
