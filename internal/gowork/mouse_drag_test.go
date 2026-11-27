package gowork

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	popup "GoWork/Tui/Components/PopUp"
)

func dragClick(t *testing.T, m model, x, y int) model {
	t.Helper()
	updated, _ := m.handleMouseClickMsg(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	return updated.(model)
}

func dragMotion(t *testing.T, m model, x, y int) model {
	t.Helper()
	updated, _ := m.handleMouseMotionMsg(tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y})
	return updated.(model)
}

func dragRelease(t *testing.T, m model, x, y int) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.handleMouseReleaseMsg(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y})
	return updated.(model), cmd
}

func TestMessageBandDragCopiesArbitraryText(t *testing.T) {
	m := newMouseCopyModel(t)
	x, y := 6, m.panelTop()+2 // first row of the first message body

	m = dragClick(t, m, x, y)
	if !m.MessageArea.TextDragActive() {
		t.Fatal("left press did not arm a message text drag")
	}
	m = dragMotion(t, m, x+5, y)
	if !m.MessageArea.TextDragMoved() {
		t.Fatal("motion did not turn the press into a text drag")
	}
	text, ok := m.MessageArea.TextDragText()
	if !ok || text != "right" {
		t.Fatalf("drag preview = %q (ok=%v), want %q", text, ok, "right")
	}

	m, cmd := dragRelease(t, m, x+5, y)
	if cmd == nil {
		t.Fatal("releasing a text drag produced no copy command")
	}
	show, ok := cmd().(popup.ShowMsg)
	if !ok {
		t.Fatalf("copy command produced %T, want popup.ShowMsg", cmd())
	}
	if !strings.Contains(show.Message, "right") && !strings.Contains(show.Message, "Copy failed") {
		t.Fatalf("popup = %q, want copied text or copy failure", show.Message)
	}
	if m.MessageArea.TextDragActive() {
		t.Fatal("drag state survived the mouse release")
	}
}

func TestPromptDragCopiesArbitraryText(t *testing.T) {
	m := newMouseCopyModel(t)
	m.Prompt.SetValue("hello prompt text")
	m.applyPanelSize()
	x, y := 5, m.promptTop()+1 // first prompt character row

	m = dragClick(t, m, x, y)
	if !m.Prompt.Focused() {
		t.Fatal("prompt click did not focus the textarea")
	}
	if !m.Prompt.TextDragActive() {
		t.Fatal("prompt click did not arm a text drag")
	}
	m = dragMotion(t, m, x+5, y)
	text, ok := m.Prompt.TextDragText()
	if !ok || text != "hello" {
		t.Fatalf("prompt drag preview = %q (ok=%v), want %q", text, ok, "hello")
	}

	m, cmd := dragRelease(t, m, x+5, y)
	if cmd == nil {
		t.Fatal("releasing a prompt text drag produced no copy command")
	}
	show, ok := cmd().(popup.ShowMsg)
	if !ok {
		t.Fatalf("copy command produced %T, want popup.ShowMsg", cmd())
	}
	if !strings.Contains(show.Message, "hello") && !strings.Contains(show.Message, "Copy failed") {
		t.Fatalf("popup = %q, want copied text or copy failure", show.Message)
	}
	if m.Prompt.TextDragActive() {
		t.Fatal("prompt drag state survived the mouse release")
	}
	if m.Prompt.Value() != "hello prompt text" {
		t.Fatalf("drag changed the prompt value to %q", m.Prompt.Value())
	}
}

func TestClickWithoutDragKeepsExistingMessageSelection(t *testing.T) {
	m := newMouseCopyModel(t)
	x, y := clickMessageBand(m)

	m = dragClick(t, m, x, y)
	if !m.MessageArea.InCopyMode() {
		t.Fatal("left click no longer enters whole-message copy mode")
	}
	m, cmd := dragRelease(t, m, x, y)
	if cmd != nil {
		t.Fatal("click without motion copied text")
	}
	if !m.MessageArea.InCopyMode() {
		t.Fatal("click without motion cancelled whole-message selection")
	}
	if from, to, ok := m.MessageArea.Selection(); !ok || from != 0 || to != 0 {
		t.Fatalf("click selection = %d..%d (ok=%v), want 0..0", from, to, ok)
	}
}

func TestPromptClickWithoutDragOnlyFocuses(t *testing.T) {
	m := newMouseCopyModel(t)
	m.Prompt.SetValue("hello prompt text")
	m.applyPanelSize()
	x, y := 5, m.promptTop()+1

	m = dragClick(t, m, x, y)
	if !m.Prompt.Focused() {
		t.Fatal("prompt click did not focus the textarea")
	}
	m, cmd := dragRelease(t, m, x, y)
	if cmd != nil {
		t.Fatal("prompt click without motion copied text")
	}
	if m.Prompt.TextDragActive() {
		t.Fatal("prompt drag stayed active after a plain click")
	}
	if m.Prompt.Value() != "hello prompt text" {
		t.Fatalf("prompt click changed the value to %q", m.Prompt.Value())
	}
}

func TestWheelScrollStillWorksWithTextDrag(t *testing.T) {
	m := newMouseCopyModel(t)
	for i := 0; i < 40; i++ {
		m.MessageArea.AppendMessage(fmt.Sprintf("message %d", i), false)
	}
	m.applyPanelSize()

	before := m.MessageArea.View()
	updated, cmd := m.handleMouseWheelMsg(tea.MouseWheelMsg{
		Button: tea.MouseWheelUp,
		X:      10,
		Y:      m.panelTop() + 2,
	})
	got := updated.(model)
	if cmd != nil {
		t.Fatal("wheel scrolling produced a command")
	}
	if got.MessageArea.TextDragActive() {
		t.Fatal("wheel scrolling started a text drag")
	}
	if got.MessageArea.View() == before {
		t.Fatal("wheel scrolling no longer scrolls the message area")
	}
}

func TestMouseDragLeavesPanelResizeAndArrowAlone(t *testing.T) {
	m := newMouseCopyModel(t)
	m.todoPanelOpen = true
	m.applyPanelSize()

	// Clicking the left-edge handle away from the arrow arms resizing, not
	// a text drag.
	x := m.arrowCol()
	y := m.panelTop() + 1
	if y == m.arrowRow() {
		y++
	}
	m = dragClick(t, m, x, y)
	if !m.panelDragActive {
		t.Fatal("left-edge click no longer starts panel resizing")
	}
	if m.MessageArea.TextDragActive() {
		t.Fatal("left-edge resize click also armed a text drag")
	}

	width := m.todoPanelWidth
	m = dragMotion(t, m, x+5, y)
	if m.todoPanelWidth == width {
		t.Fatal("panel motion no longer resizes the todo panel")
	}
	m, _ = dragRelease(t, m, x+5, y)
	if m.panelDragActive {
		t.Fatal("mouse release did not stop panel resizing")
	}
	if m.MessageArea.TextDragActive() {
		t.Fatal("panel resize left a text drag armed")
	}

	// The toggle arrow still opens/closes the panel.
	m.todoPanelOpen = false
	m.applyPanelSize()
	arrowX, arrowY := m.arrowCol(), m.arrowRow()
	m = dragClick(t, m, arrowX, arrowY)
	if !m.todoPanelOpen {
		t.Fatal("arrow click no longer opens the todo panel")
	}
	if m.MessageArea.TextDragActive() {
		t.Fatal("arrow click also armed a message text drag")
	}
}
