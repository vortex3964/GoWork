package main

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
)

func newModeTestModel() model {
	think := false
	return model{
		tabs:         tabs.New("code", "skills", "stats"),
		prompt:       promptbar.New(),
		message_area: messagearea.New(),
		mode:         modePrompt,
		aiThink:      &think,
		changesList:  changeslist.New(nil),
		skills:       skills.New(),
		stats:        stats.New(),
	}
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func TestCtrlLRespectsQuestionnaireAndActiveTab(t *testing.T) {
	tests := []struct {
		name   string
		mode   uiMode
		tabIdx int
		want   uiMode
	}{
		{"questionnaire ignores ctrl+l", modeQuestionnaire, 0, modeQuestionnaire},
		{"stats tab ignores ctrl+l", modeStats, 2, modeStats},
		{"skills tab ignores ctrl+l", modeSkills, 1, modeSkills},
		{"code tab opens changes list", modePrompt, 0, modeChangeHandling},
		{"closing changes on stats restores stats mode", modeChangeHandling, 2, modeStats},
		{"closing changes on skills restores skills mode", modeChangeHandling, 1, modeSkills},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModeTestModel()
			m.mode = tt.mode
			m.tabs.ActiveIdx = tt.tabIdx

			updated, _ := m.Update(ctrlKey('l'))
			if got := updated.(model).mode; got != tt.want {
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
		want uiMode
	}{
		{"code", 0, modePrompt},
		{"skills", 1, modeSkills},
		{"stats", 2, modeStats},
	}
	for _, msgCase := range msgs {
		for _, tabCase := range tabsUnder {
			t.Run(msgCase.name+"/"+tabCase.name, func(t *testing.T) {
				m := newModeTestModel()
				m.mode = modeProviderSelect
				m.tabs.ActiveIdx = tabCase.idx
				// Keep SelectedMsg from actually switching providers; the
				// mode restore happens before that branch.
				*m.aiThink = true

				updated, _ := m.Update(msgCase.msg)
				if got := updated.(model).mode; got != tabCase.want {
					t.Fatalf("mode after %s = %v, want %v", msgCase.name, got, tabCase.want)
				}
			})
		}
	}
}
