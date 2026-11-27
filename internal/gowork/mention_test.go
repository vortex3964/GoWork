package gowork

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"GoWork/Tui/Components/MentionMenu"
	"GoWork/internal/lsp"
	"GoWork/tools"
	"GoWork/tools/SkillsTool"
)

func installMentionSkills(t *testing.T, skills map[string]string) *skillstool.Manager {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "skills")
	for name, desc := range skills {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nbody\n", name, desc)
		if err := os.WriteFile(filepath.Join(skillDir, skillstool.SkillFileName), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mgr := skillstool.NewManagerAt(dir)
	mgr.Discover()
	prev := skillstool.GetManager()
	skillstool.SetManager(mgr)
	t.Cleanup(func() { skillstool.SetManager(prev) })
	return mgr
}

func findMention(items []mentionmenu.Item, kind mentionmenu.Kind, name string) (mentionmenu.Item, bool) {
	for _, item := range items {
		if item.Kind == kind && item.Name == name {
			return item, true
		}
	}
	return mentionmenu.Item{}, false
}

func TestMentionItemsEnumerateAllSources(t *testing.T) {
	mgr := installMentionSkills(t, map[string]string{
		"go-testing": "Run Go tests",
		"docs":       "Write documentation",
	})
	if !mgr.Load("go-testing") {
		t.Fatal("could not load go-testing")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "guides"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	appData := t.TempDir()
	mcpJSON := `{"mcpServers":{"github":{"command":"npx"},"remote":{"url":"https://example.test/mcp"}}}`
	if err := os.WriteFile(filepath.Join(appData, mcpConfigFileName), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := newToolRuntime(appData, root)
	rt.loadMCPConfig()
	if err := rt.lspCfg.Add(lsp.ServerConfig{
		Name:        "gopls",
		Command:     "gopls",
		LanguageIDs: []string{"go"},
		Enabled:     true,
	}); err != nil {
		t.Fatal(err)
	}

	m := newSlashTestModel(t)
	m.root = root
	m.toolRuntime = rt
	items := m.mentionItems()

	loaded, ok := findMention(items, mentionmenu.KindSkill, "go-testing")
	if !ok || !strings.Contains(loaded.Description, "loaded") || !strings.Contains(loaded.Description, "Run Go tests") {
		t.Fatalf("loaded skill item = %+v (ok=%v)", loaded, ok)
	}
	unloaded, ok := findMention(items, mentionmenu.KindSkill, "docs")
	if !ok || unloaded.Description != "Write documentation" {
		t.Fatalf("unloaded skill item = %+v (ok=%v)", unloaded, ok)
	}
	if got := loaded.Mention(); got != "@go-testing" {
		t.Fatalf("skill mention = %q", got)
	}

	github, ok := findMention(items, mentionmenu.KindMCP, "github")
	if !ok || !strings.Contains(github.Description, "stdio") || !strings.Contains(github.Description, "disconnected") {
		t.Fatalf("MCP stdio item = %+v (ok=%v)", github, ok)
	}
	if got := github.Mention(); got != "@mcp:github" {
		t.Fatalf("MCP mention = %q", got)
	}
	remote, ok := findMention(items, mentionmenu.KindMCP, "remote")
	if !ok || !strings.Contains(remote.Description, "http") {
		t.Fatalf("MCP remote item = %+v (ok=%v)", remote, ok)
	}

	gopls, ok := findMention(items, mentionmenu.KindLSP, "gopls")
	if !ok || !strings.Contains(gopls.Description, "go") || !strings.Contains(gopls.Description, "enabled") {
		t.Fatalf("LSP item = %+v (ok=%v)", gopls, ok)
	}
	if got := gopls.Mention(); got != "@lsp:gopls" {
		t.Fatalf("LSP mention = %q", got)
	}

	for _, want := range []string{"@README.md", "@docs", "@docs/guide.md", "@docs/guides"} {
		if _, ok := findMentionInsert(items, want); !ok {
			t.Fatalf("project item %q missing from enumeration: %+v", want, items)
		}
	}
	if _, ok := findMentionInsert(items, "@.git/config"); ok {
		t.Fatal(".git contents were enumerated")
	}
}

func TestMentionItemsUseInjectedLSPManager(t *testing.T) {
	m := newSlashTestModel(t)
	m.LSP = &fakeLSPManager{}
	items := m.mentionItems()
	server, ok := findMention(items, mentionmenu.KindLSP, "fake")
	if !ok {
		t.Fatalf("injected LSP server missing from enumeration: %+v", items)
	}
	if !strings.Contains(server.Description, "go") || !strings.Contains(server.Description, "ready") {
		t.Fatalf("injected LSP description = %q, want language and status", server.Description)
	}
	if got := server.Mention(); got != "@lsp:fake" {
		t.Fatalf("injected LSP mention = %q", got)
	}
}

func findMentionInsert(items []mentionmenu.Item, insert string) (mentionmenu.Item, bool) {
	for _, item := range items {
		if item.Mention() == insert {
			return item, true
		}
	}
	return mentionmenu.Item{}, false
}

func typeMentionPrompt(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(model)
	}
	return m
}

func pressMentionKey(t *testing.T, m model, key tea.KeyPressMsg) model {
	t.Helper()
	updated, _ := m.Update(key)
	return updated.(model)
}

func TestMentionKeyFlowCompletesThenSubmits(t *testing.T) {
	installMentionSkills(t, map[string]string{"alpha": "First skill"})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newSlashTestModel(t)
	m.root = root
	m.Prompt.Focus()
	m.promptHeight = m.Prompt.Height()

	m = typeMentionPrompt(t, m, "@al")
	if got := m.Prompt.Value(); got != "@al" {
		t.Fatalf("prompt after typing = %q, want @al", got)
	}
	if !m.Prompt.MentionVisible() {
		t.Fatal("typing @al did not open the mention menu")
	}
	if sel, ok := m.Prompt.MentionSelected(); !ok || sel.Name != "alpha" {
		t.Fatalf("mention selection = %v (ok=%v), want alpha", sel, ok)
	}

	// Down/up move the highlight without touching the prompt text.
	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.Prompt.Value(); got != "@al" {
		t.Fatalf("mention navigation changed the prompt to %q", got)
	}
	if sel, _ := m.Prompt.MentionSelected(); sel.Name == "" {
		t.Fatal("mention selection disappeared after down")
	}
	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if sel, _ := m.Prompt.MentionSelected(); sel.Name != "alpha" {
		t.Fatalf("selection after up = %s, want alpha", sel.Name)
	}

	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.Prompt.Value(); got != "@alpha " {
		t.Fatalf("prompt after tab completion = %q, want %q", got, "@alpha ")
	}
	if m.Prompt.MentionVisible() {
		t.Fatal("mention menu stayed open after tab completion")
	}
	if m.Mode != modePrompt {
		t.Fatalf("tab completion submitted the prompt: mode=%v", m.Mode)
	}
	if len(m.Context) != 0 {
		t.Fatalf("tab completion leaked into model context: %+v", m.Context)
	}

	// Enter completes too, without submitting.
	m.Prompt.Reset()
	m.Prompt.SetValue("@al")
	m.Prompt.SetMentionItems(m.mentionItems())
	if !m.Prompt.MentionVisible() {
		t.Fatal("mention menu did not reopen for @al")
	}
	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.Prompt.Value(); got != "@alpha " {
		t.Fatalf("prompt after enter completion = %q, want %q", got, "@alpha ")
	}
	if len(m.Context) != 0 {
		t.Fatalf("enter completion leaked into model context: %+v", m.Context)
	}

	// A second, explicit Enter sends the completed text to the model.
	holder := &tools.ProviderHolder{}
	holder.Set(fastStreamProvider{})
	m.model = holder
	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.Context) != 1 {
		t.Fatalf("context length after submit = %d, want 1", len(m.Context))
	}
	if got := m.Context[0].Content; got != "@alpha " {
		t.Fatalf("submitted content = %q, want %q", got, "@alpha ")
	}
}

func TestMentionEscDismissesAndTypingReopens(t *testing.T) {
	installMentionSkills(t, map[string]string{"alpha": "First skill"})
	m := newSlashTestModel(t)
	m.Prompt.Focus()
	m.promptHeight = m.Prompt.Height()

	m = typeMentionPrompt(t, m, "@al")
	if !m.Prompt.MentionVisible() {
		t.Fatal("mention menu did not open")
	}
	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Prompt.MentionVisible() {
		t.Fatal("esc did not dismiss the mention menu")
	}
	if got := m.Prompt.Value(); got != "@al" {
		t.Fatalf("esc cleared the prompt: %q", got)
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("esc changed prompt state: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}

	m = typeMentionPrompt(t, m, "p")
	if got := m.Prompt.Value(); got != "@alp" {
		t.Fatalf("prompt after editing = %q, want @alp", got)
	}
	if !m.Prompt.MentionVisible() {
		t.Fatal("editing the dismissed query did not reopen the menu")
	}
}

func TestMentionMenuOnlyOnCodeTab(t *testing.T) {
	installMentionSkills(t, map[string]string{"alpha": "First skill"})
	m := newSlashTestModel(t)
	m.Prompt.Focus()

	m = typeMentionPrompt(t, m, "@al")
	if !m.Prompt.MentionVisible() {
		t.Fatal("mention menu did not open on the code tab")
	}

	// ctrl+tab is the tab-switch key (plain tab completes a mention while
	// the menu is visible). Leaving the code tab must hide the menu.
	m = pressMentionKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModCtrl})
	if got := m.Tabs.Active().Name; got != "skills" {
		t.Fatalf("active tab after ctrl+tab = %q, want skills", got)
	}
	if m.Prompt.MentionVisible() {
		t.Fatal("mention menu stayed visible after leaving the code tab")
	}

	m.Prompt.Focus()
	m.Prompt.SetValue("@al")
	if m.Prompt.MentionVisible() {
		t.Fatal("mention menu opened on the skills tab")
	}
}
