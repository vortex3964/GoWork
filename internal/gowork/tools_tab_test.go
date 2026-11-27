package gowork

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	messagearea "GoWork/Tui/Components/MessageArea"
	promptbar "GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/SkillForm"
	tabs "GoWork/Tui/Components/Tabs"
	toolstab "GoWork/Tui/Components/ToolsTab"
	"GoWork/internal/lsp"
	"GoWork/internal/mcp"
	"GoWork/tools"
	"GoWork/tools/SkillsTool"
)

func newToolsTestModel(t *testing.T) model {
	t.Helper()
	think := false
	m := model{
		Tabs:        tabs.New("code", "skills", "tools", "stats"),
		Prompt:      promptbar.New(),
		MessageArea: messagearea.New(),
		Mode:        modePrompt,
		winWidth:    100,
		winHeight:   40,
		AiThink:     &think,
		root:        t.TempDir(),
		skillForm:   skillform.New(),
	}
	m.Prompt.SetWidth(100)
	m.Prompt.SetCommands(slashCommands())
	m.Tools.SetSize(100, 30)
	m.applyPanelSize()
	return m
}

func ctrlKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

func TestCtrlTTogglesToolsTab(t *testing.T) {
	m := newToolsTestModel(t)

	mdl, _ := m.Update(ctrlKey('t'))
	m = mdl.(model)
	if got := m.Tabs.Active().Name; got != "tools" {
		t.Fatalf("active tab after ctrl+t = %q, want tools", got)
	}
	if m.Mode != modeTools {
		t.Fatalf("mode after ctrl+t = %v, want tools", m.Mode)
	}

	mdl, _ = m.Update(ctrlKey('t'))
	m = mdl.(model)
	if got := m.Tabs.Active().Name; got != "code" {
		t.Fatalf("active tab after second ctrl+t = %q, want code", got)
	}
	if m.Mode != modePrompt {
		t.Fatalf("mode after second ctrl+t = %v, want prompt", m.Mode)
	}
}

func TestToolsSlashCommandSelectsTab(t *testing.T) {
	m := newToolsTestModel(t)
	cmd := m.runSlashCommand("/tools")
	if cmd != nil {
		// selectTabCmd is synchronous; a command here would hit the model.
		_ = cmd()
	}
	if got := m.Tabs.Active().Name; got != "tools" {
		t.Fatalf("active tab after /tools = %q, want tools", got)
	}
	if m.Mode != modeTools {
		t.Fatalf("mode after /tools = %v, want tools", m.Mode)
	}
	for _, spec := range slashCommandSpecs {
		if spec.Name == "tools" {
			return
		}
	}
	t.Fatal("/tools is missing from the slash-command registry")
}

func TestToolsTabNavigationAndActions(t *testing.T) {
	m := newToolsTestModel(t)
	m.Tabs.ActiveIdx = 2
	m.syncModeToTab()

	// Right moves to MCP, then a opens the add form. The form owns keys and
	// Esc returns to the list without leaving the tab.
	mdl, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = mdl.(model)
	if m.Tools.Section() != toolstab.SectionMCP {
		t.Fatalf("tools section = %v, want MCP", m.Tools.Section())
	}
	mdl, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = mdl.(model)
	if !m.Tools.FormVisible() {
		t.Fatal("a did not open the MCP form")
	}
	mdl, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mdl.(model)
	if m.Tools.FormVisible() {
		t.Fatal("esc did not close the MCP form")
	}
	if m.Mode != modeTools {
		t.Fatalf("closing the form left tools mode: %v", m.Mode)
	}

	mdl, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mdl.(model)
	if got := m.Tabs.Active().Name; got != "code" {
		t.Fatalf("esc did not leave the tools tab: %q", got)
	}
}

func TestToolsTabRendersLiveSnapshot(t *testing.T) {
	m := newToolsTestModel(t)
	m.Tools.SetSize(120, 40)
	m.Tools.SetData(toolstab.Data{
		Skills: []toolstab.Skill{{Name: "review", Loaded: true}},
		MCP: []toolstab.MCPServer{{
			Name: "files", Transport: "stdio", Enabled: true, Connected: true, Tools: 2,
		}},
		LSP: []toolstab.LSPServer{{Name: "gopls", Languages: []string{"go"}, Enabled: true}},
	})
	view := m.Tools.View()
	for _, want := range []string{"review", "files", "gopls", "connected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tools view missing %q:\n%s", want, view)
		}
	}
}

func TestToolsTabSkillToggleAndCreate(t *testing.T) {
	old := skillstool.GetManager()
	t.Cleanup(func() { skillstool.SetManager(old) })
	mgr := skillstool.NewManager(t.TempDir())
	skillstool.SetManager(mgr)
	skill, err := mgr.Create("review", "review code", "read the diff")
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}

	m := newToolsTestModel(t)
	m.Tabs.ActiveIdx = 2
	m.syncModeToTab()
	m.refreshTools()
	if view := m.Tools.View(); !strings.Contains(view, skill.Name) {
		t.Fatalf("tools tab did not list the skill:\n%s", view)
	}

	mdl, _ := m.Update(toolstab.ToggleMsg{Section: toolstab.SectionSkills, Name: skill.Name, Enabled: true})
	m = mdl.(model)
	if !mgr.IsLoaded(skill.Name) {
		t.Fatal("tools toggle did not load the skill")
	}
	mdl, _ = m.Update(toolstab.ToggleMsg{Section: toolstab.SectionSkills, Name: skill.Name, Enabled: false})
	m = mdl.(model)
	if mgr.IsLoaded(skill.Name) {
		t.Fatal("tools toggle did not unload the skill")
	}

	mdl, _ = m.Update(toolstab.CreateSkillMsg{})
	m = mdl.(model)
	if !m.Prompt.Focused() {
		t.Fatal("empty-prompt create did not focus the prompt bar")
	}
	m.Prompt.SetValue("skill body")
	mdl, _ = m.Update(toolstab.CreateSkillMsg{})
	m = mdl.(model)
	if !m.skillForm.Visible() {
		t.Fatal("create with prompt text did not open the skill form")
	}
	if m.Prompt.Focused() {
		t.Fatal("opening the skill form did not blur the prompt")
	}

	// The modal must own the keyboard from the Tools tab too: type a name,
	// tab to the description, and submit.
	for _, r := range "newskill" {
		mdl, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mdl.(model)
	}
	mdl, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = mdl.(model)
	for _, r := range "new description" {
		mdl, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = mdl.(model)
	}
	mdl, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mdl.(model)
	if cmd == nil {
		t.Fatalf("submitting the skill form produced no command:\n%s", m.skillForm.View())
	}
	submit, ok := cmd().(skillform.SubmitMsg)
	if !ok {
		t.Fatalf("skill form produced %T, want SubmitMsg", cmd())
	}
	mdl, _ = m.Update(submit)
	m = mdl.(model)
	if !mgr.Exists("newskill") {
		t.Fatal("submitted skill was not created")
	}
}

func TestToolsTabAppViewRendersPane(t *testing.T) {
	m := newToolsTestModel(t)
	m.Tabs.ActiveIdx = 2
	m.syncModeToTab()
	view := m.View().Content
	for _, want := range []string{"Skills", "MCP servers", "LSP servers", "TOOLS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("app view missing %q:\n%s", want, view)
		}
	}
}

func TestToolsTabAddAndRemoveServersUpdatesRegistry(t *testing.T) {
	rt := newToolRuntime(t.TempDir(), t.TempDir())
	rt.newMCPClient = func(mcp.ServerConfig) (mcpClient, error) {
		return &fakeMCPClient{tools: fakeTools()}, nil
	}
	rt.Load()
	t.Cleanup(func() { _ = rt.Close() })

	m := newToolsTestModel(t)
	m.toolRuntime = rt
	m.baseTools = []tools.AgentTool{}

	mdl, _ := m.Update(toolstab.AddMCPMsg{Name: "files", Command: "npx"})
	m = mdl.(model)
	mcpName := tools.MCPToolName("files", "echo")
	if _, ok := m.tool_dispatcher.Lookup(mcpName); !ok {
		t.Fatalf("added MCP tool %q is not dispatchable", mcpName)
	}
	if view := m.Tools.View(); !strings.Contains(view, "files") {
		t.Fatalf("tools tab did not list the new server:\n%s", view)
	}

	mdl, _ = m.Update(toolstab.ToggleMsg{Section: toolstab.SectionMCP, Name: "files", Enabled: false})
	m = mdl.(model)
	if _, ok := m.tool_dispatcher.Lookup(mcpName); ok {
		t.Fatalf("disabled MCP tool %q is still dispatchable", mcpName)
	}
	mdl, _ = m.Update(toolstab.RemoveMsg{Section: toolstab.SectionMCP, Name: "files"})
	m = mdl.(model)
	if len(rt.MCPServers()) != 0 {
		t.Fatalf("removed MCP server stayed configured: %+v", rt.MCPServers())
	}

	mdl, _ = m.Update(toolstab.AddLSPMsg{Name: "gopls", Command: "gopls", Languages: []string{"go"}})
	m = mdl.(model)
	if _, ok := m.tool_dispatcher.Lookup("lsp_diagnostics"); !ok {
		t.Fatal("added LSP tools are not dispatchable")
	}
	mdl, _ = m.Update(toolstab.RemoveMsg{Section: toolstab.SectionLSP, Name: "gopls"})
	m = mdl.(model)
	if _, ok := m.tool_dispatcher.Lookup("lsp_diagnostics"); ok {
		t.Fatal("removed LSP tools are still dispatchable")
	}
}

func TestLSPCommandUsesRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	rt := newToolRuntime(dir, t.TempDir())
	if err := os.WriteFile(rt.lspPath, []byte(`{"servers":[
		{"name":"gopls","command":"gopls","language_ids":["go"],"enabled":false}
	]}`), 0o600); err != nil {
		t.Fatalf("write LSP config: %v", err)
	}
	rt.Load()
	t.Cleanup(func() { _ = rt.Close() })

	m := newToolsTestModel(t)
	m.LSP = rt
	m.runSlashCommand("/lsp")
	view := m.MessageArea.View()
	if !strings.Contains(view, "gopls") || !strings.Contains(view, "disabled") {
		t.Fatalf("/lsp did not list the configured server:\n%s", view)
	}

	m.runSlashCommand("/lsp configure gopls")
	data, err := os.ReadFile(rt.lspPath)
	if err != nil {
		t.Fatalf("read LSP config: %v", err)
	}
	cfg, err := lsp.ParseConfig(data)
	if err != nil {
		t.Fatalf("parse LSP config: %v", err)
	}
	server, ok := cfg.Get("gopls")
	if !ok || !server.Enabled {
		t.Fatalf("/lsp configure did not enable/persist gopls: %+v, %v", server, ok)
	}
}
