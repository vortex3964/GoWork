package toolstab

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testModel() Model {
	m := New()
	m.SetSize(120, 32)
	m.SetData(Data{
		Skills: []Skill{
			{Name: "review", Description: "review changes", Loaded: true},
			{Name: "docs", Description: "write docs"},
		},
		MCP: []MCPServer{
			{Name: "files", Transport: "stdio", Enabled: true, Connected: true, Tools: 3},
			{Name: "remote", Transport: "http", Enabled: false},
			{Name: "broken", Transport: "stdio", Enabled: true, Error: "connection refused"},
		},
		LSP: []LSPServer{
			{Name: "gopls", Languages: []string{"go"}, Enabled: true},
			{Name: "pyright", Languages: []string{"python"}, Enabled: false},
		},
		MCPConfigPath: "/tmp/.mcp.json",
		LSPConfigPath: "/tmp/.lsp.json",
	})
	return m
}

func key(code rune, mod tea.KeyMod, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod, Text: text}
}

func TestViewRendersAllThreeSections(t *testing.T) {
	m := testModel()
	view := m.View()
	for _, want := range []string{
		"Skills (1/2 loaded)", "review", "docs",
		"MCP servers (2/3 enabled · 1 connected · 3 tools)", "files", "remote", "broken",
		"connection refused",
		"LSP servers (1/2 enabled)", "gopls", "pyright",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestNavigationSwitchesSectionsAndRows(t *testing.T) {
	m := testModel()
	if m.Section() != SectionSkills {
		t.Fatalf("initial section = %v, want skills", m.Section())
	}
	if sel, ok := m.Selected(); !ok || sel.Name != "review" {
		t.Fatalf("initial selection = %+v, %v", sel, ok)
	}

	m, _ = m.Update(key(tea.KeyDown, 0, ""))
	if sel, _ := m.Selected(); sel.Name != "docs" {
		t.Fatalf("after down = %q, want docs", sel.Name)
	}
	m, _ = m.Update(key(tea.KeyUp, 0, ""))
	if sel, _ := m.Selected(); sel.Name != "review" {
		t.Fatalf("after up = %q, want review", sel.Name)
	}

	m, _ = m.Update(key(tea.KeyRight, 0, ""))
	if m.Section() != SectionMCP {
		t.Fatalf("section after right = %v, want MCP", m.Section())
	}
	if sel, _ := m.Selected(); sel.Name != "files" {
		t.Fatalf("MCP selection = %q, want files", sel.Name)
	}
	m, _ = m.Update(key(tea.KeyDown, 0, ""))
	if sel, _ := m.Selected(); sel.Name != "remote" {
		t.Fatalf("MCP selection after down = %q, want remote", sel.Name)
	}

	m, _ = m.Update(key('3', 0, "3"))
	if m.Section() != SectionLSP {
		t.Fatalf("section after 3 = %v, want LSP", m.Section())
	}
	if sel, _ := m.Selected(); sel.Name != "gopls" {
		t.Fatalf("LSP selection = %q, want gopls", sel.Name)
	}
}

func TestToggleAndRemoveEmitActions(t *testing.T) {
	m := testModel()
	m.SetSection(SectionMCP)

	m, cmd := m.Update(key(tea.KeySpace, 0, " "))
	if cmd == nil {
		t.Fatal("space produced no command")
	}
	toggle, ok := cmd().(ToggleMsg)
	if !ok {
		t.Fatalf("space produced %T, want ToggleMsg", cmd())
	}
	if toggle.Section != SectionMCP || toggle.Name != "files" || toggle.Enabled {
		t.Fatalf("toggle = %+v, want disabling files", toggle)
	}

	m, cmd = m.Update(key('d', 0, "d"))
	if cmd == nil {
		t.Fatal("d produced no command")
	}
	remove, ok := cmd().(RemoveMsg)
	if !ok {
		t.Fatalf("d produced %T, want RemoveMsg", cmd())
	}
	if remove.Section != SectionMCP || remove.Name != "files" {
		t.Fatalf("remove = %+v", remove)
	}

	m.SetSection(SectionSkills)
	m, cmd = m.Update(key('p', 0, "p"))
	if cmd != nil {
		t.Fatalf("p on an already-loaded skill produced %T", cmd())
	}
	m, _ = m.Update(key(tea.KeyDown, 0, ""))
	m, cmd = m.Update(key('p', 0, "p"))
	if cmd == nil {
		t.Fatal("p on an unloaded skill produced no command")
	}
	toggle = cmd().(ToggleMsg)
	if toggle.Name != "docs" || !toggle.Enabled {
		t.Fatalf("load toggle = %+v", toggle)
	}

	m, cmd = m.Update(key('c', 0, "c"))
	if cmd == nil {
		t.Fatal("c produced no command")
	}
	if _, ok := cmd().(CreateSkillMsg); !ok {
		t.Fatalf("c produced %T, want CreateSkillMsg", cmd())
	}
}

func TestMCPAddFormParsesCommandAndURL(t *testing.T) {
	m := testModel()
	m.SetSection(SectionMCP)
	m, _ = m.Update(key('a', 0, "a"))
	if !m.FormVisible() {
		t.Fatal("a did not open the MCP form")
	}
	for _, r := range "files" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, _ = m.Update(key(tea.KeyTab, 0, ""))
	for _, r := range "npx -y pkg" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, cmd := m.Update(key(tea.KeyEnter, 0, ""))
	if cmd == nil {
		t.Fatal("enter did not submit the MCP form")
	}
	msg, ok := cmd().(AddMCPMsg)
	if !ok {
		t.Fatalf("submit produced %T, want AddMCPMsg", cmd())
	}
	if msg.Name != "files" || msg.Command != "npx" || strings.Join(msg.Args, " ") != "-y pkg" || msg.URL != "" {
		t.Fatalf("parsed MCP message = %+v", msg)
	}
	if m.FormVisible() {
		t.Fatal("form stayed open after submit")
	}

	m, _ = m.Update(key('a', 0, "a"))
	for _, r := range "remote" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, _ = m.Update(key(tea.KeyTab, 0, ""))
	for _, r := range "https://example.com/mcp" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, cmd = m.Update(key(tea.KeyEnter, 0, ""))
	msg = cmd().(AddMCPMsg)
	if msg.URL != "https://example.com/mcp" || msg.Command != "" {
		t.Fatalf("URL MCP message = %+v", msg)
	}
}

func TestLSPAddFormAndCancel(t *testing.T) {
	m := testModel()
	m.SetSection(SectionLSP)
	m, _ = m.Update(key('a', 0, "a"))
	if !m.FormVisible() {
		t.Fatal("a did not open the LSP form")
	}
	m, _ = m.Update(key(tea.KeyEscape, 0, ""))
	if m.FormVisible() {
		t.Fatal("esc did not cancel the form")
	}

	m, _ = m.Update(key('a', 0, "a"))
	for _, r := range "gopls" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, _ = m.Update(key(tea.KeyTab, 0, ""))
	for _, r := range "gopls -remote=auto" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, _ = m.Update(key(tea.KeyTab, 0, ""))
	for _, r := range "go, mod" {
		m, _ = m.Update(key(r, 0, string(r)))
	}
	m, cmd := m.Update(key(tea.KeyEnter, 0, ""))
	if cmd == nil {
		t.Fatal("enter did not submit the LSP form")
	}
	msg, ok := cmd().(AddLSPMsg)
	if !ok {
		t.Fatalf("submit produced %T, want AddLSPMsg", cmd())
	}
	if msg.Name != "gopls" || msg.Command != "gopls" || strings.Join(msg.Args, " ") != "-remote=auto" ||
		strings.Join(msg.Languages, ",") != "go,mod" {
		t.Fatalf("parsed LSP message = %+v", msg)
	}
}

func TestFormRequiresEveryField(t *testing.T) {
	m := testModel()
	m.SetSection(SectionMCP)
	m, _ = m.Update(key('a', 0, "a"))
	m, cmd := m.Update(key(tea.KeyEnter, 0, ""))
	if cmd != nil {
		t.Fatalf("empty form produced %T", cmd())
	}
	if !m.FormVisible() {
		t.Fatal("empty form closed instead of reporting the missing fields")
	}
	if !strings.Contains(m.formView(), "fill every field") {
		t.Fatalf("form error missing:\n%s", m.formView())
	}
}

func TestMouseWheelMovesSelection(t *testing.T) {
	m := testModel()
	m.HandleMouseWheel(false)
	if sel, _ := m.Selected(); sel.Name != "docs" {
		t.Fatalf("wheel down selection = %q, want docs", sel.Name)
	}
	m.HandleMouseWheel(true)
	if sel, _ := m.Selected(); sel.Name != "review" {
		t.Fatalf("wheel up selection = %q, want review", sel.Name)
	}
}
