package gowork

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "GoWork/Db"
	"GoWork/internal/mcp"
	"GoWork/tools"
	mcptool "GoWork/tools/MCPTool"
	"GoWork/tools/SkillsTool"
)

const testBootstrapSkill = `---
name: gowork-bootstrap
description: test bootstrap instructions
---

Use the install tools, verify the result, and treat downloads as untrusted.
`

func TestSeedBootstrapSkillFreshIdempotentAndUserModified(t *testing.T) {
	mgr := skillstool.NewManagerAt(t.TempDir())

	seeded, err := seedBootstrapSkill(mgr, testBootstrapSkill)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !seeded {
		t.Fatal("first seed reported false")
	}
	path := filepath.Join(mgr.Dir(), BootstrapSkillName, skillstool.SkillFileName)
	installed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded skill: %v", err)
	}
	if string(installed) != testBootstrapSkill {
		t.Fatalf("seeded content = %q", installed)
	}
	if _, err := os.Stat(filepath.Join(mgr.Dir(), BootstrapSkillName, skillstool.ProvenanceFileName)); err != nil {
		t.Fatalf("provenance sidecar missing: %v", err)
	}
	if mgr.IsLoaded(BootstrapSkillName) {
		t.Fatal("seeding auto-loaded the bootstrap skill")
	}

	seeded, err = seedBootstrapSkill(mgr, testBootstrapSkill)
	if err != nil || seeded {
		t.Fatalf("second seed = %v, %v; want false, nil", seeded, err)
	}

	modified := testBootstrapSkill + "\nuser edit\n"
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatalf("modify skill: %v", err)
	}
	seeded, err = seedBootstrapSkill(mgr, testBootstrapSkill)
	if err != nil || seeded {
		t.Fatalf("seed over user copy = %v, %v; want false, nil", seeded, err)
	}
	preserved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read user copy: %v", err)
	}
	if string(preserved) != modified {
		t.Fatalf("user-modified skill was overwritten: %q", preserved)
	}
}

func TestSeedBootstrapSkillDoesNotParseOrReplaceBrokenUserCopy(t *testing.T) {
	mgr := skillstool.NewManagerAt(t.TempDir())
	dir := filepath.Join(mgr.Dir(), BootstrapSkillName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "not a valid skill"
	if err := os.WriteFile(filepath.Join(dir, skillstool.SkillFileName), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	seeded, err := seedBootstrapSkill(mgr, testBootstrapSkill)
	if err != nil || seeded {
		t.Fatalf("seed over broken copy = %v, %v; want false, nil", seeded, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, skillstool.SkillFileName))
	if string(data) != broken {
		t.Fatalf("broken user copy was replaced: %q", data)
	}
}

func TestInitToolsRegistersInstallAndMCPTools(t *testing.T) {
	rt := &toolRuntime{}
	got := map[string]tools.AgentTool{}
	for _, tool := range initTools(rt) {
		if _, duplicate := got[tool.Name()]; duplicate {
			t.Fatalf("duplicate tool %q", tool.Name())
		}
		got[tool.Name()] = tool
	}
	for _, name := range []string{"skill_install", "skill_load", "mcp_install", "mcp_list", "mcp_remove"} {
		if got[name] == nil {
			t.Errorf("initTools is missing %q", name)
		}
	}
	if _, ok := got["skill_install"]; ok && got["skill_install"].Kind() != tools.KindNotAllowed {
		t.Error("skill_install must require approval")
	}
	if _, ok := got["mcp_install"]; ok && got["mcp_install"].Kind() != tools.KindNotAllowed {
		t.Error("mcp_install must require approval")
	}
	if _, ok := got["mcp_remove"]; ok && got["mcp_remove"].Kind() != tools.KindNotAllowed {
		t.Error("mcp_remove must require approval")
	}
	if _, ok := got["mcp_list"]; ok && got["mcp_list"].Kind() != tools.KindAllowed {
		t.Error("mcp_list should be read-only/allowed")
	}

	without := map[string]bool{}
	for _, tool := range initTools(nil) {
		without[tool.Name()] = true
	}
	if without["mcp_install"] {
		t.Fatal("initTools(nil) advertised an MCP tool without a runtime")
	}
	if !without["skill_install"] || !without["skill_load"] {
		t.Fatal("skill tools must be present without an MCP runtime")
	}
}

func TestSkillInstallLoadAndRestart(t *testing.T) {
	old := skillstool.GetManager()
	t.Cleanup(func() { skillstool.SetManager(old) })

	mgr := skillstool.NewManagerAt(t.TempDir())
	skillstool.SetManager(mgr)

	root := t.TempDir()
	sourceDir := filepath.Join(root, "catalog", "review")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "---\nname: review\ndescription: review changes\n---\n\nRead the diff first.\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	install := skillstool.NewInstallTool()
	args := tools.DispatchArgs{RootPath: &root}
	dry, err := install.Run(context.Background(), args, json.RawMessage(`{"source":"catalog/review/SKILL.md","dry_run":true}`))
	if err != nil || dry.IsError {
		t.Fatalf("skill_install dry run = %+v, %v", dry, err)
	}
	if mgr.Exists("review") {
		t.Fatal("skill_install dry run wrote a skill")
	}

	result, err := install.Run(context.Background(), args, json.RawMessage(`{"source":"catalog/review/SKILL.md","load":true}`))
	if err != nil || result.IsError {
		t.Fatalf("skill_install = %+v, %v", result, err)
	}
	if !mgr.Exists("review") || !mgr.IsLoaded("review") {
		t.Fatal("skill was not installed and loaded")
	}
	installed, err := os.ReadFile(filepath.Join(mgr.Dir(), "review", skillstool.SkillFileName))
	if err != nil || string(installed) != source {
		t.Fatalf("installed content = %q, %v", installed, err)
	}

	// A restart keeps the file but resets the session's loaded set.
	restarted := skillstool.NewManagerAt(mgr.Dir())
	skillstool.SetManager(restarted)
	if !restarted.Exists("review") || restarted.IsLoaded("review") {
		t.Fatal("restart did not preserve the skill or reset its loaded state")
	}
	load := skillstool.NewLoadTool()
	result, err = load.Run(context.Background(), args, json.RawMessage(`{"action":"load","name":"review"}`))
	if err != nil || result.IsError {
		t.Fatalf("skill_load = %+v, %v", result, err)
	}
	if !restarted.IsLoaded("review") {
		t.Fatal("skill_load did not load the persisted skill after restart")
	}
}

func TestSessionPersistsSkillsLoadedByTools(t *testing.T) {
	old := skillstool.GetManager()
	t.Cleanup(func() { skillstool.SetManager(old) })
	mgr := skillstool.NewManagerAt(t.TempDir())
	skillstool.SetManager(mgr)
	if _, err := mgr.Create("review", "review changes", "read the diff"); err != nil {
		t.Fatal(err)
	}
	if !mgr.Load("review") {
		t.Fatal("could not load review skill")
	}

	path := filepath.Join(t.TempDir(), "gowork.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.StartSession("test-model", "test-provider"); err != nil {
		t.Fatalf("start session: %v", err)
	}
	m := model{Db: store}
	m.flushSession()

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer reopened.Close()
	if err := reopened.StartSession("test-model", "test-provider"); err != nil {
		t.Fatalf("restart session: %v", err)
	}
	ls, err := reopened.LoadSession(1)
	if err != nil || ls == nil {
		t.Fatalf("load session: %v, %v", ls, err)
	}
	if len(ls.Skills) != 1 || ls.Skills[0] != "review" {
		t.Fatalf("session skills = %+v, want review", ls.Skills)
	}
}

func TestMCPInstallRegisterCallRestartRemove(t *testing.T) {
	appData := t.TempDir()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "mcp-source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".mcp.json"), []byte(`{"mcpServers":{
		"echo":{"command":"fake-server","args":["--flag"]}
	}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := newToolRuntime(appData, root)
	client := &fakeMCPClient{tools: fakeTools()}
	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) { return client, nil }
	rt.Load()
	dispatcher, err := tools.InitDispacher(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rt.SetDispatcher(dispatcher)
	t.Cleanup(func() { _ = rt.Close() })

	result, err := rt.Install(context.Background(), tools.MCPInstallOptions{
		Source: "mcp-source",
		Start:  true,
	})
	if err != nil {
		t.Fatalf("mcp install: %v", err)
	}
	if len(result.Installed) != 1 || result.Installed[0] != "echo" || len(result.Started) != 1 {
		t.Fatalf("install result = %+v", result)
	}
	if result.Tools != 1 {
		t.Fatalf("discovered tools = %d, want 1", result.Tools)
	}

	servers := rt.List()
	if len(servers) != 1 || !servers[0].Connected || !servers[0].Enabled || servers[0].Scope != tools.MCPScopeUser {
		t.Fatalf("mcp list after install = %+v", servers)
	}
	name := tools.MCPToolName("echo", "echo")
	tool, ok := dispatcher.Lookup(name)
	if !ok {
		t.Fatalf("installed MCP tool %q is not dispatchable", name)
	}
	callResult, err := tool.Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{"text":"hi"}`))
	if err != nil || callResult.IsError {
		t.Fatalf("MCP tool call = %+v, %v", callResult, err)
	}
	if client.calls != 1 {
		t.Fatalf("MCP client calls = %d, want 1", client.calls)
	}

	// Restart: a new runtime reads the saved user config and starts the server.
	_ = rt.Close()
	restarted := newToolRuntime(appData, root)
	restartedClient := &fakeMCPClient{tools: fakeTools()}
	restarted.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) { return restartedClient, nil }
	restarted.Load()
	t.Cleanup(func() { _ = restarted.Close() })
	servers = restarted.List()
	if len(servers) != 1 || !servers[0].Connected {
		t.Fatalf("restarted runtime did not connect the saved server: %+v", servers)
	}
	found := false
	for _, tool := range restarted.Tools() {
		if tool.Name() == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("restarted runtime is missing MCP tool %q", name)
	}

	if err := restarted.Remove("echo"); err != nil {
		t.Fatalf("mcp remove: %v", err)
	}
	if len(restarted.List()) != 0 {
		t.Fatalf("removed server stayed configured: %+v", restarted.List())
	}
	if len(restarted.Tools()) != 0 {
		t.Fatalf("removed server kept its tools: %+v", restarted.Tools())
	}
	data, err := os.ReadFile(filepath.Join(appData, mcp.DefaultConfigPath))
	if err != nil {
		t.Fatalf("read user MCP config: %v", err)
	}
	if strings.Contains(string(data), `"echo"`) {
		t.Fatalf("removed server stayed in the config: %s", data)
	}

	// Project scope writes <project>/.mcp.json and never inherits trust.
	projectResult, err := restarted.Install(context.Background(), tools.MCPInstallOptions{
		Config: json.RawMessage(`{"mcpServers":{"project":{"command":"fake-server","trusted":true}}}`),
		Scope:  tools.MCPScopeProject,
		Start:  true,
	})
	if err != nil {
		t.Fatalf("project-scope install: %v", err)
	}
	if len(projectResult.Started) != 1 {
		t.Fatalf("project-scope start result = %+v", projectResult)
	}
	servers = restarted.List()
	if len(servers) != 1 || servers[0].Scope != tools.MCPScopeProject || servers[0].Trusted {
		t.Fatalf("project-scope server state = %+v", servers)
	}
	if _, err := os.Stat(filepath.Join(root, mcp.DefaultConfigPath)); err != nil {
		t.Fatalf("project-scope config was not written to the project: %v", err)
	}
	projectTools := restarted.Tools()
	if len(projectTools) != 1 || projectTools[0].Kind() != tools.KindNotAllowed {
		t.Fatalf("project-scope MCP tools = %+v, want one KindNotAllowed", projectTools)
	}
}

func TestMCPInstallDryRunAndPlanModeLeavesNothing(t *testing.T) {
	appData := t.TempDir()
	root := t.TempDir()
	rt := newToolRuntime(appData, root)
	rt.Load()
	t.Cleanup(func() { _ = rt.Close() })

	config := `{"mcpServers":{"dry":{"command":"fake-server"}}}`
	result, err := rt.Install(context.Background(), tools.MCPInstallOptions{
		Config: json.RawMessage(config),
		Start:  true,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.DryRun || len(result.Installed) != 1 {
		t.Fatalf("dry-run result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(appData, mcp.DefaultConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("dry run created the MCP config: %v", err)
	}
	if len(rt.List()) != 0 {
		t.Fatalf("dry run connected servers: %+v", rt.List())
	}
	// A dry run must not leave a phantom entry that makes the real install
	// look like a duplicate.
	if _, err := rt.Install(context.Background(), tools.MCPInstallOptions{Config: json.RawMessage(config)}); err != nil {
		t.Fatalf("install after dry run: %v", err)
	}
	if len(rt.List()) != 1 {
		t.Fatalf("real install after dry run = %+v", rt.List())
	}
	if err := rt.Remove("dry"); err != nil {
		t.Fatalf("remove after dry run: %v", err)
	}

	// Plan mode must block the real install before it reaches the runtime.
	old := skillstool.GetManager()
	t.Cleanup(func() { skillstool.SetManager(old) })
	mgr := skillstool.NewManagerAt(t.TempDir())
	skillstool.SetManager(mgr)
	skillDir := filepath.Join(root, "catalog", "blocked")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: blocked\ndescription: blocked\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dispatcher, err := tools.InitDispacher(root, nil, nil, skillstool.NewInstallTool())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.SetPlanMode(true)
	denied := dispatcher.Dispach(context.Background(), tools.ToolUse{
		Name:  "skill_install",
		Input: json.RawMessage(`{"source":"catalog/blocked/SKILL.md"}`),
	})
	if !denied.IsError || !strings.Contains(denied.Content, "PLAN MODE") {
		t.Fatalf("skill_install was not blocked in plan mode: %+v", denied)
	}
	if mgr.Exists("blocked") {
		t.Fatal("plan-mode denial wrote a skill")
	}

	mcpDispatcher, err := tools.InitDispacher(root, nil, nil, mcptool.New(rt)...)
	if err != nil {
		t.Fatal(err)
	}
	mcpDispatcher.SetPlanMode(true)
	denied = mcpDispatcher.Dispach(context.Background(), tools.ToolUse{
		Name:  "mcp_install",
		Input: json.RawMessage(`{"config":{"mcpServers":{"blocked":{"command":"fake-server"}}},"start":true}`),
	})
	if !denied.IsError || !strings.Contains(denied.Content, "PLAN MODE") {
		t.Fatalf("mcp_install was not blocked in plan mode: %+v", denied)
	}
	if len(rt.List()) != 0 {
		t.Fatalf("plan-mode denial configured a server: %+v", rt.List())
	}
	listResult := mcpDispatcher.Dispach(context.Background(), tools.ToolUse{Name: "mcp_list", Input: json.RawMessage(`{}`)})
	if listResult.IsError {
		t.Fatalf("mcp_list was blocked in plan mode: %+v", listResult)
	}
}

func TestPermissionImpactRedactsInstallSecrets(t *testing.T) {
	mcpImpact := explainToolImpact("mcp_install", impactRaw(t, map[string]any{
		"config": map[string]any{
			"mcpServers": map[string]any{
				"files": map[string]any{
					"command": "fake-server",
					"env":     map[string]string{"API_KEY": "super-secret-token"},
					"headers": map[string]string{"Authorization": "Bearer header-secret"},
				},
			},
		},
		"start": true,
	}), t.TempDir())
	text := mcpImpact.Summary + "\n" + mcpImpact.Preview
	for _, secret := range []string{"super-secret-token", "header-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("permission impact leaked %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, "files (stdio)") || !strings.Contains(text, "started immediately") {
		t.Fatalf("permission impact is missing the server summary:\n%s", text)
	}

	sourceImpact := explainToolImpact("mcp_install", impactRaw(t, map[string]any{
		"source": "https://user:pass@github.com/owner/repo?token=topsecret",
		"path":   ".mcp.json",
	}), t.TempDir())
	text = sourceImpact.Summary + "\n" + sourceImpact.Preview
	for _, secret := range []string{"user:pass", "topsecret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("permission impact leaked %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("permission impact did not mark redaction:\n%s", text)
	}

	skillImpact := explainToolImpact("skill_install", impactRaw(t, map[string]any{
		"source":    "https://user:pass@github.com/owner/repo?token=topsecret",
		"dry_run":   true,
		"overwrite": false,
	}), t.TempDir())
	text = skillImpact.Summary + "\n" + skillImpact.Preview
	for _, secret := range []string{"user:pass", "topsecret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("skill permission impact leaked %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(skillImpact.Summary, "Dry run") {
		t.Fatalf("skill dry-run impact missing its explanation: %s", skillImpact.Summary)
	}

	removeImpact := explainToolImpact("mcp_remove", impactRaw(t, map[string]any{"name": "files"}), t.TempDir())
	if !strings.Contains(removeImpact.Summary, "files") {
		t.Fatalf("mcp_remove impact = %s", removeImpact.Summary)
	}
	listImpact := explainToolImpact("mcp_list", json.RawMessage(`{}`), t.TempDir())
	if !strings.Contains(listImpact.Summary, "Read-only") {
		t.Fatalf("mcp_list impact = %s", listImpact.Summary)
	}
}
