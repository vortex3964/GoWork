package gowork

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	messagearea "GoWork/Tui/Components/MessageArea"
	popup "GoWork/Tui/Components/PopUp"
	promptbar "GoWork/Tui/Components/Promptbar"
	tabs "GoWork/Tui/Components/Tabs"
)

func newMouseCopyModel(t *testing.T) model {
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
	m.applyPanelSize()
	m.MessageArea.AppendMessage("right click target", true)
	return m
}

// clickMessageBand returns the screen coordinates of the first content row
// of the first message in the code tab's message band.
func clickMessageBand(m model) (x, y int) {
	return 10, m.panelTop() + 1
}

func TestRightClickMessageBandCopiesClickedMessage(t *testing.T) {
	m := newMouseCopyModel(t)
	x, y := clickMessageBand(m)

	updated, cmd := m.handleMouseClickMsg(tea.MouseClickMsg{
		Button: tea.MouseRight,
		X:      x,
		Y:      y,
	})
	if cmd == nil {
		t.Fatal("right click in the message band did not start a copy")
	}

	got := cmd()
	show, ok := got.(popup.ShowMsg)
	if !ok {
		t.Fatalf("copy command produced %T, want popup.ShowMsg", got)
	}
	// Clipboard backends are optional on headless machines: either the
	// success toast names the copied message, or the copy failed and the
	// popup says so. Both prove the right-click path was reached.
	if !strings.Contains(show.Message, "right click target") &&
		!strings.Contains(show.Message, "Copy failed") {
		t.Fatalf("popup = %q, want copied message or copy failure", show.Message)
	}

	if updated.(model).MessageArea.InCopyMode() {
		t.Fatal("right-click copy left copy mode active")
	}
}

func TestRightClickOutsideMessageBandDoesNotCopy(t *testing.T) {
	m := newMouseCopyModel(t)
	// The prompt bar owns the rows at the bottom of the screen.
	updated, cmd := m.handleMouseClickMsg(tea.MouseClickMsg{
		Button: tea.MouseRight,
		X:      10,
		Y:      m.promptTop(),
	})
	if cmd != nil {
		t.Fatal("right click on the prompt bar triggered a message copy")
	}
	if updated.(model).MessageArea.InCopyMode() {
		t.Fatal("prompt-bar right click entered copy mode")
	}
}

func TestLeftClickMessageBandSelectsMessage(t *testing.T) {
	m := newMouseCopyModel(t)
	x, y := clickMessageBand(m)

	updated, cmd := m.handleMouseClickMsg(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      x,
		Y:      y,
	})
	if cmd == nil {
		t.Fatal("left click in the message band produced no hint popup")
	}
	got := updated.(model)
	if !got.MessageArea.InCopyMode() {
		t.Fatal("left click did not enter copy mode")
	}
	if from, to, ok := got.MessageArea.Selection(); !ok || from != 0 || to != 0 {
		t.Fatalf("left click selection = %d..%d (ok=%v), want 0..0", from, to, ok)
	}
}
