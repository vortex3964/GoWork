package gowork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	dialog "GoWork/Tui/Components/Dialog"
	messagearea "GoWork/Tui/Components/MessageArea"
	promptbar "GoWork/Tui/Components/Promptbar"
	tabs "GoWork/Tui/Components/Tabs"
)

func newSlashTestModel(t *testing.T) model {
	t.Helper()
	think := false
	m := model{
		Tabs:        tabs.New("code", "skills", "stats"),
		Prompt:      promptbar.New(),
		MessageArea: messagearea.New(),
		Mode:        modePrompt,
		winWidth:    100,
		winHeight:   40,
		AiThink:     &think,
	}
	m.Prompt.SetWidth(100)
	m.Prompt.SetCommands(slashCommands())
	m.applyPanelSize()
	return m
}

func TestParseSlashCommand(t *testing.T) {
	cases := []struct {
		input string
		name  string
		args  []string
		err   bool
	}{
		{"/help", "help", nil, false},
		{"/CoFfEe", "coffee", nil, false},
		{"/context docs/a.md", "context", []string{"docs/a.md"}, false},
		{`/context "docs/a b.md"`, "context", []string{"docs/a b.md"}, false},
		{`/context docs/a\ b.md`, "context", []string{"docs/a b.md"}, false},
		{"/", "", nil, false},
		{"  /stats  ", "stats", nil, false},
		{"hello", "", nil, true},
		{`/context "unterminated`, "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			name, args, err := parseSlashCommand(tc.input)
			if (err != nil) != tc.err {
				t.Fatalf("parse error = %v, want err=%v", err, tc.err)
			}
			if tc.err {
				return
			}
			if name != tc.name {
				t.Fatalf("name = %q, want %q", name, tc.name)
			}
			if strings.Join(args, "|") != strings.Join(tc.args, "|") {
				t.Fatalf("args = %v, want %v", args, tc.args)
			}
		})
	}
}

func TestSlashCommandExecution(t *testing.T) {
	t.Run("help opens the modal", func(t *testing.T) {
		m := newSlashTestModel(t)
		cmd := m.runSlashCommand("/help")
		if cmd == nil {
			t.Fatal("/help returned no command")
		}
		msg, ok := cmd().(dialog.ShowMsg)
		if !ok {
			t.Fatalf("/help produced %T, want dialog.ShowMsg", cmd())
		}
		if msg.Title != "Help" || !strings.Contains(msg.Message, "/coffee") {
			t.Fatalf("help dialog = %+v, want title Help and command list", msg)
		}
	})

	t.Run("model opens the picker", func(t *testing.T) {
		m := newSlashTestModel(t)
		m.runSlashCommand("/model")
		if m.Mode != modeProviderSelect {
			t.Fatalf("mode after /model = %v, want provider select", m.Mode)
		}
	})

	t.Run("stats switches tab and mode", func(t *testing.T) {
		m := newSlashTestModel(t)
		m.runSlashCommand("/stats")
		if got := m.Tabs.Active().Name; got != "stats" {
			t.Fatalf("active tab after /stats = %q, want stats", got)
		}
		if m.Mode != modeStats {
			t.Fatalf("mode after /stats = %v, want stats", m.Mode)
		}
	})

	t.Run("skills switches tab and mode", func(t *testing.T) {
		m := newSlashTestModel(t)
		m.runSlashCommand("/skills")
		if got := m.Tabs.Active().Name; got != "skills" {
			t.Fatalf("active tab after /skills = %q, want skills", got)
		}
		if m.Mode != modeSkills {
			t.Fatalf("mode after /skills = %v, want skills", m.Mode)
		}
	})

	t.Run("coffee shows the overlay", func(t *testing.T) {
		m := newSlashTestModel(t)
		cmd := m.runSlashCommand("/coffee")
		if cmd == nil {
			t.Fatal("/coffee returned no tick command")
		}
		if !m.Coffee.IsVisible() {
			t.Fatal("/coffee did not start the animation")
		}
		m.Coffee.Stop()
	})

	t.Run("lsp default reports none configured", func(t *testing.T) {
		m := newSlashTestModel(t)
		before := m.MessageArea.Size()
		m.runSlashCommand("/lsp")
		if m.MessageArea.Size() <= before {
			t.Fatal("/lsp did not add a status message")
		}
		if view := m.MessageArea.View(); !strings.Contains(view, "No language servers configured") {
			t.Fatalf("/lsp default message missing:\n%s", view)
		}
	})

	t.Run("unknown command reports an error", func(t *testing.T) {
		m := newSlashTestModel(t)
		before := m.MessageArea.Size()
		m.runSlashCommand("/nope")
		if m.MessageArea.Size() <= before {
			t.Fatal("unknown command produced no message")
		}
		if view := m.MessageArea.View(); !strings.Contains(view, "Unknown command") {
			t.Fatalf("unknown command message missing:\n%s", view)
		}
	})
}

func TestLSPConfigureUsesInjectedManager(t *testing.T) {
	m := newSlashTestModel(t)
	fake := &fakeLSPManager{}
	m.LSP = fake
	m.runSlashCommand("/lsp configure gopls")
	if fake.configured != "gopls" {
		t.Fatalf("configured = %q, want gopls", fake.configured)
	}
}

type fakeLSPManager struct {
	configured string
}

func (f *fakeLSPManager) Servers() []LSPServer {
	return []LSPServer{{Name: "fake", Language: "go", Status: "ready"}}
}

func (f *fakeLSPManager) Configure(name string) error {
	f.configured = name
	return nil
}

func TestSlashPromptNeverReachesModelContext(t *testing.T) {
	m := newSlashTestModel(t)
	m.Prompt.SetValue("/help")
	cmds := m.submitPrompt()
	if len(m.Context) != 0 {
		t.Fatalf("slash command leaked into context: %+v", m.Context)
	}
	if m.Prompt.Value() != "" {
		t.Fatalf("prompt not cleared after command: %q", m.Prompt.Value())
	}
	if len(cmds) == 0 {
		t.Fatal("/help produced no tea command")
	}
}

func TestSlashPaletteKeyFlow(t *testing.T) {
	m := newSlashTestModel(t)
	m.Prompt.Focus()
	m.Prompt.SetValue("/")
	if !m.Prompt.PaletteVisible() {
		t.Fatal("palette not visible after setting a slash prompt")
	}

	// Selection starts on /help; two downs lands on /stats.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(model)
	if sel, ok := m.Prompt.PaletteSelected(); !ok || sel.Name != "stats" {
		t.Fatalf("palette selection = %v (ok=%v), want stats", sel, ok)
	}

	// Enter accepts the palette selection and runs it.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.Mode != modeStats || m.Tabs.Active().Name != "stats" {
		t.Fatalf("after palette enter: mode=%v tab=%q, want stats", m.Mode, m.Tabs.Active().Name)
	}
	if m.Prompt.PaletteVisible() {
		t.Fatal("palette remained visible after execution")
	}
}

func TestContextCommandAddsBoundedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello context"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newSlashTestModel(t)
	m.root = dir

	m.runSlashCommand("/context notes.txt")
	if len(m.Context) != 1 {
		t.Fatalf("context length = %d, want 1", len(m.Context))
	}
	if m.Context[0].Role != "user" || !strings.Contains(m.Context[0].Content, "hello context") {
		t.Fatalf("context message = %+v", m.Context[0])
	}

	m.runSlashCommand("/context")
	if view := m.MessageArea.View(); !strings.Contains(view, "Estimated context") {
		t.Fatalf("/context estimate missing:\n%s", view)
	}
}
