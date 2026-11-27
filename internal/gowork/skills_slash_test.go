package gowork

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	dialog "GoWork/Tui/Components/Dialog"
	messagearea "GoWork/Tui/Components/MessageArea"
	promptbar "GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/SkillForm"
	tabs "GoWork/Tui/Components/Tabs"
)

func newSkillsEnterTestModel(t *testing.T) model {
	t.Helper()
	think := false
	m := model{
		Tabs:        tabs.New("code", "skills", "stats"),
		Prompt:      promptbar.New(),
		MessageArea: messagearea.New(),
		Mode:        modeSkills,
		winWidth:    100,
		winHeight:   40,
		AiThink:     &think,
		skillForm:   skillform.New(),
	}
	m.Tabs.ActiveIdx = 1
	m.Prompt.SetWidth(100)
	m.Prompt.SetCommands(slashCommands())
	m.applyPanelSize()
	m.Prompt.Focus()
	return m
}

func TestSkillsTabEnterRunsSlashCommands(t *testing.T) {
	m := newSkillsEnterTestModel(t)
	m.Prompt.SetValue("  /help  ")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if m.skillForm.Visible() {
		t.Fatal("/help opened the skill form")
	}
	if cmd == nil {
		t.Fatal("/help on the skills tab produced no command")
	}
	msg, ok := cmd().(dialog.ShowMsg)
	if !ok || msg.Title != "Help" {
		t.Fatalf("/help produced %T (%+v), want the Help dialog", cmd(), msg)
	}
	if m.Prompt.Value() != "" {
		t.Fatalf("prompt not cleared after /help: %q", m.Prompt.Value())
	}
	if m.Mode != modeSkills || m.Tabs.Active().Name != "skills" {
		t.Fatalf("/help left the skills tab: mode=%v tab=%q", m.Mode, m.Tabs.Active().Name)
	}
}

func TestSkillsTabEnterOpensFormForNormalText(t *testing.T) {
	m := newSkillsEnterTestModel(t)
	m.Prompt.SetValue("summarize the open diff")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if !m.skillForm.Visible() {
		t.Fatal("normal prompt text did not open the skill form")
	}
	if m.Prompt.Value() != "summarize the open diff" {
		t.Fatalf("prompt body changed while opening the form: %q", m.Prompt.Value())
	}
}

func TestNewSkillCommandOpensForm(t *testing.T) {
	for _, spec := range slashCommandSpecs {
		if spec.Name == "newskill" {
			goto registered
		}
	}
	t.Fatal("/newskill is missing from the slash-command registry")

registered:
	m := newSkillsEnterTestModel(t)
	m.Prompt.SetValue("/newskill")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if !m.skillForm.Visible() {
		t.Fatal("/newskill did not open the skill form")
	}
	if m.Prompt.Value() != "" {
		t.Fatalf("/newskill leaked the command into the skill body: %q", m.Prompt.Value())
	}

	m = newSkillsEnterTestModel(t)
	m.Prompt.SetValue("/newskill write the release notes")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	if !m.skillForm.Visible() {
		t.Fatal("/newskill with a body did not open the skill form")
	}
	if got := m.Prompt.Value(); got != "write the release notes" {
		t.Fatalf("/newskill body = %q, want the command arguments", got)
	}
}

func TestNewSkillCommandKeepsExistingPromptBody(t *testing.T) {
	m := newSkillsEnterTestModel(t)
	m.Prompt.SetValue("review the staged changes")

	m.runSlashCommand("/newskill")
	if !m.skillForm.Visible() {
		t.Fatal("/newskill did not open the skill form")
	}
	if got := m.Prompt.Value(); got != "review the staged changes" {
		t.Fatalf("prompt body after /newskill = %q, want it preserved", got)
	}
}

func TestSkillsTabCtrlNOpensForm(t *testing.T) {
	t.Run("prompt focused", func(t *testing.T) {
		m := newSkillsEnterTestModel(t)
		m.Prompt.SetValue("review the staged changes")

		updated, _ := m.Update(ctrlKey('n'))
		m = updated.(model)
		if !m.skillForm.Visible() {
			t.Fatal("ctrl+n did not open the skill form")
		}
		if got := m.Prompt.Value(); got != "review the staged changes" {
			t.Fatalf("prompt body after ctrl+n = %q, want it preserved", got)
		}
	})

	t.Run("prompt blurred", func(t *testing.T) {
		m := newSkillsEnterTestModel(t)
		m.Prompt.Blur()

		updated, _ := m.Update(ctrlKey('n'))
		m = updated.(model)
		if !m.skillForm.Visible() {
			t.Fatal("ctrl+n did not open the skill form with the prompt blurred")
		}
	})
}

func TestSkillsHelpTextMentionsNewSkillPaths(t *testing.T) {
	help := helpText()
	for _, want := range []string{"/newskill", "ctrl+n"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help text missing %q:\n%s", want, help)
		}
	}
}
