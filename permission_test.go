package main

import (
	"testing"

	"GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/Tabs"
)

func TestEndTurnResetsAutoAcceptTools(t *testing.T) {
	think := false
	m := model{
		tabs:    tabs.New("code"),
		prompt:  promptbar.New(),
		aiThink: &think,
	}
	m.autoAcceptTools = true

	m.endTurn()

	if m.autoAcceptTools {
		t.Fatal("endTurn left autoAcceptTools set; Accept All leaked into the next turn")
	}
}
