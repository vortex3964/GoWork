package test

import (
	"testing"

	"GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/Tabs"
	gowork "GoWork/internal/gowork"
)

func TestEndTurnResetsAutoAcceptTools(t *testing.T) {
	think := false
	m := gowork.Model{
		Tabs:    tabs.New("code"),
		Prompt:  promptbar.New(),
		AiThink: &think,
	}
	m.AutoAcceptTools = true

	m.EndTurn()

	if m.AutoAcceptTools {
		t.Fatal("endTurn left autoAcceptTools set; Accept All leaked into the next turn")
	}
}
