package test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	changeslist "GoWork/Tui/Components/ChangesList"
	messagearea "GoWork/Tui/Components/MessageArea"
	"GoWork/Tui/Components/Promptbar"
	providerselect "GoWork/Tui/Components/ProviderSelect"
	skills "GoWork/Tui/Components/SkillsTab"
	stats "GoWork/Tui/Components/Stats"
	tabs "GoWork/Tui/Components/Tabs"
	gowork "GoWork/internal/gowork"
)

func newModeTestModel() gowork.Model {
	think := false
	return gowork.Model{
		Tabs:        tabs.New("code", "skills", "stats"),
		Prompt:      promptbar.New(),
		MessageArea: messagearea.New(),
		Mode:        gowork.ModePrompt,
		AiThink:     &think,
		ChangesList: changeslist.New(nil),
		Skills:      skills.New(),
		Stats:       stats.New(),
	}
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func TestCtrlLRespectsQuestionnaireAndActiveTab(t *testing.T) {
	tests := []struct {
		name   string
		mode   gowork.UIMode
		tabIdx int
		want   gowork.UIMode
	}{
		{"questionnaire ignores ctrl+l", gowork.ModeQuestionnaire, 0, gowork.ModeQuestionnaire},
		{"stats tab ignores ctrl+l", gowork.ModeStats, 2, gowork.ModeStats},
		{"skills tab ignores ctrl+l", gowork.ModeSkills, 1, gowork.ModeSkills},
		{"code tab opens changes list", gowork.ModePrompt, 0, gowork.ModeChangeHandling},
		{"closing changes on stats restores stats mode", gowork.ModeChangeHandling, 2, gowork.ModeStats},
		{"closing changes on skills restores skills mode", gowork.ModeChangeHandling, 1, gowork.ModeSkills},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModeTestModel()
			m.Mode = tt.mode
			m.Tabs.ActiveIdx = tt.tabIdx

			updated, _ := m.Update(ctrlKey('l'))
			if got := updated.(gowork.Model).Mode; got != tt.want {
				t.Fatalf("mode after ctrl+l = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderPickerRestoresActiveTabMode(t *testing.T) {
	msgs := []struct {
		name string
		msg  tea.Msg
	}{
		{"selected", providerselect.SelectedMsg{Provider: "test", ModelID: "test"}},
		{"cancelled", providerselect.CancelledMsg{}},
	}
	tabsUnder := []struct {
		name string
		idx  int
		want gowork.UIMode
	}{
		{"code", 0, gowork.ModePrompt},
		{"skills", 1, gowork.ModeSkills},
		{"stats", 2, gowork.ModeStats},
	}
	for _, msgCase := range msgs {
		for _, tabCase := range tabsUnder {
			t.Run(msgCase.name+"/"+tabCase.name, func(t *testing.T) {
				m := newModeTestModel()
				m.Mode = gowork.ModeProviderSelect
				m.Tabs.ActiveIdx = tabCase.idx
				// Keep SelectedMsg from actually switching providers; the
				// mode restore happens before that branch.
				*m.AiThink = true

				updated, _ := m.Update(msgCase.msg)
				if got := updated.(gowork.Model).Mode; got != tabCase.want {
					t.Fatalf("mode after %s = %v, want %v", msgCase.name, got, tabCase.want)
				}
			})
		}
	}
}
