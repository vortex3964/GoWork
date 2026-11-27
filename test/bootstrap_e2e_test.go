package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gowork "GoWork/internal/gowork"
	"GoWork/tools"
	"GoWork/tools/SkillsTool"
)

// bootstrapFakeMCP satisfies the model-facing MCP runtime seam without
// starting a server. The full install/register/call lifecycle is exercised in
// internal/gowork; here the check is that the shipped tool registry is wired.
type bootstrapFakeMCP struct{}

func (bootstrapFakeMCP) Install(context.Context, tools.MCPInstallOptions) (tools.MCPInstallResult, error) {
	return tools.MCPInstallResult{}, nil
}

func (bootstrapFakeMCP) List() []tools.MCPServerInfo { return nil }

func (bootstrapFakeMCP) Remove(string) error { return nil }

func TestBootstrapSkillShipsAndSeedsOnce(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "skills", "gowork-bootstrap", "SKILL.md"))
	if err != nil {
		t.Fatalf("read bundled bootstrap skill: %v", err)
	}
	if len(content) == 0 || len(content) > 8<<10 {
		t.Fatalf("bundled bootstrap skill is %d bytes, want 1..8192", len(content))
	}
	parsed, err := skillstool.ParseSkillContent(string(content))
	if err != nil {
		t.Fatalf("bundled bootstrap skill is invalid: %v", err)
	}
	if parsed.Name != gowork.BootstrapSkillName {
		t.Fatalf("bundled skill name = %q, want %q", parsed.Name, gowork.BootstrapSkillName)
	}

	configDir := t.TempDir()
	t.Setenv("GOWORK_CONFIG_DIR", configDir)

	seeded, err := gowork.SeedBootstrapSkill(string(content))
	if err != nil {
		t.Fatalf("seed bootstrap skill: %v", err)
	}
	if !seeded {
		t.Fatal("fresh seed reported false")
	}

	mgr, err := skillstool.NewUserManager()
	if err != nil {
		t.Fatalf("NewUserManager: %v", err)
	}
	path := filepath.Join(mgr.Dir(), gowork.BootstrapSkillName, skillstool.SkillFileName)
	installed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed bootstrap skill: %v", err)
	}
	if string(installed) != string(content) {
		t.Fatal("seeded content does not match the bundled SKILL.md")
	}
	if mgr.IsLoaded(gowork.BootstrapSkillName) {
		t.Fatal("bootstrap skill was auto-loaded")
	}
	if _, err := os.Stat(filepath.Join(mgr.Dir(), gowork.BootstrapSkillName, skillstool.ProvenanceFileName)); err != nil {
		t.Fatalf("bootstrap provenance sidecar missing: %v", err)
	}

	seeded, err = gowork.SeedBootstrapSkill(string(content))
	if err != nil || seeded {
		t.Fatalf("second seed = %v, %v; want false, nil", seeded, err)
	}

	modified := string(content) + "\nlocal user edit\n"
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatalf("modify installed skill: %v", err)
	}
	seeded, err = gowork.SeedBootstrapSkill(string(content))
	if err != nil || seeded {
		t.Fatalf("seed over user-modified copy = %v, %v; want false, nil", seeded, err)
	}
	preserved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read user-modified copy: %v", err)
	}
	if string(preserved) != modified {
		t.Fatal("seeding overwrote a user-modified bootstrap skill")
	}
}

func TestBuiltInToolsAndProviderDefsIncludeInstalls(t *testing.T) {
	runtime := bootstrapFakeMCP{}
	toolNames := map[string]tools.AgentTool{}
	for _, tool := range gowork.BuiltInTools(runtime) {
		toolNames[tool.Name()] = tool
	}
	wanted := []string{"skill_install", "skill_load", "mcp_install", "mcp_list", "mcp_remove"}
	for _, name := range wanted {
		if toolNames[name] == nil {
			t.Errorf("BuiltInTools is missing %q", name)
		}
	}
	if toolNames["skill_install"] != nil && toolNames["skill_install"].Kind() != tools.KindNotAllowed {
		t.Error("skill_install must require approval")
	}
	if toolNames["mcp_install"] != nil && toolNames["mcp_install"].Kind() != tools.KindNotAllowed {
		t.Error("mcp_install must require approval")
	}
	if toolNames["mcp_remove"] != nil && toolNames["mcp_remove"].Kind() != tools.KindNotAllowed {
		t.Error("mcp_remove must require approval")
	}
	if toolNames["mcp_list"] != nil && toolNames["mcp_list"].Kind() != tools.KindAllowed {
		t.Error("mcp_list must be read-only/allowed")
	}
	if toolNames["skill_load"] != nil && toolNames["skill_load"].Kind() != tools.KindAllowed {
		t.Error("skill_load must be read-only/allowed")
	}

	defs := map[string]bool{}
	for _, def := range gowork.BuiltInToolDefs(runtime) {
		if strings.TrimSpace(def.Name) == "" || strings.TrimSpace(def.Description) == "" || len(def.InputSchema) == 0 {
			t.Errorf("incomplete provider definition: %+v", def)
		}
		defs[def.Name] = true
	}
	for _, name := range wanted {
		if !defs[name] {
			t.Errorf("BuiltInToolDefs is missing %q", name)
		}
	}

	withoutMCP := map[string]bool{}
	for _, tool := range gowork.BuiltInTools(nil) {
		withoutMCP[tool.Name()] = true
	}
	if withoutMCP["mcp_install"] || withoutMCP["mcp_list"] || withoutMCP["mcp_remove"] {
		t.Fatal("BuiltInTools(nil) advertised MCP tools without a runtime")
	}
	if !withoutMCP["skill_install"] || !withoutMCP["skill_load"] {
		t.Fatal("skill install/load tools disappeared without an MCP runtime")
	}
}

func TestBootstrapGuidanceIsDocumented(t *testing.T) {
	prompt, err := os.ReadFile(filepath.Join("..", "prompts", "system_prompt.md"))
	if err != nil {
		t.Fatalf("read system prompt: %v", err)
	}
	for _, want := range []string{"gowork-bootstrap", "skill_load", "skill_install", "mcp_install"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("system prompt is missing bootstrap pointer %q", want)
		}
	}

	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, want := range []string{
		"GOWORK_CONFIG_DIR", "gowork-bootstrap", "skill_install", "skill_load",
		"mcp_install", "mcp_list", "mcp_remove", "Project-scope", "commit SHA",
	} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README is missing %q", want)
		}
	}

	workflow, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	if !strings.Contains(string(workflow), "cp -R prompts logo skills") {
		t.Error("release workflow does not stage skills/")
	}
	if strings.Count(string(workflow), "prompts logo skills tools README.md") != 2 {
		t.Error("release workflow does not include skills/ in both archive formats")
	}
}
