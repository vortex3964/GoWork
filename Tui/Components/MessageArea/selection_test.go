package messagearea_test

import (
	"strings"
	"testing"

	messagearea "GoWork/Tui/Components/MessageArea"
)

func buildCopyModel(t *testing.T) messagearea.Model {
	t.Helper()
	m := messagearea.New()
	m.SetSize(100, 30)
	m.AppendMessage("first user text", true)
	idx := m.AppendTool("read_file", "main.go")
	m.UpdateToolMessage(idx, messagearea.ToolDone, "package main\nfunc main() {}")
	m.AppendMessage("last assistant text", false)
	return m
}

func TestBeginCopyStartsOnNewestAndSelectsText(t *testing.T) {
	m := buildCopyModel(t)

	if m.InCopyMode() {
		t.Fatal("copy mode should start off")
	}
	if !m.BeginCopy() {
		t.Fatal("BeginCopy with messages returned false")
	}
	if !m.InCopyMode() {
		t.Fatal("BeginCopy did not enter copy mode")
	}
	from, to, ok := m.Selection()
	if !ok || from != 2 || to != 2 {
		t.Fatalf("initial selection = %d..%d (ok=%v), want 2..2", from, to, ok)
	}

	text, ok := m.SelectedText()
	if !ok || text != "last assistant text" {
		t.Fatalf("SelectedText = %q (ok=%v), want newest message", text, ok)
	}

	// Extend upward over the tool row and then the user message.
	m.MoveSelection(-1, true)
	m.MoveSelection(-1, true)
	from, to, _ = m.Selection()
	if from != 0 || to != 2 {
		t.Fatalf("extended selection = %d..%d, want 0..2", from, to)
	}
	text, ok = m.SelectedText()
	if !ok {
		t.Fatal("SelectedText lost the range")
	}
	for _, want := range []string{"first user text", "read_file main.go", "func main()", "last assistant text"} {
		if !strings.Contains(text, want) {
			t.Errorf("SelectedText missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "first user") > strings.Index(text, "last assistant") {
		t.Error("selected text is not in message order")
	}
	if strings.Contains(text, "\x1b[") {
		t.Error("selected text contains ANSI styling")
	}
}

func TestMoveSelectionCollapsesRange(t *testing.T) {
	m := buildCopyModel(t)
	m.BeginCopy()
	m.MoveSelection(-1, true)
	m.MoveSelection(-1, true)
	if from, to, _ := m.Selection(); from != 0 || to != 2 {
		t.Fatalf("extended selection = %d..%d, want 0..2", from, to)
	}

	// A non-extending move collapses the range around the current focus.
	m.MoveSelection(1, false)
	from, to, _ := m.Selection()
	if from != 1 || to != 1 {
		t.Fatalf("collapsed selection = %d..%d, want 1..1", from, to)
	}

	// Clamp at both ends instead of going out of range.
	for i := 0; i < 10; i++ {
		m.MoveSelection(-1, false)
	}
	if from, _, _ := m.Selection(); from != 0 {
		t.Fatalf("selection at top = %d, want 0", from)
	}
	for i := 0; i < 10; i++ {
		m.MoveSelection(1, false)
	}
	if _, to, _ := m.Selection(); to != 2 {
		t.Fatalf("selection at bottom = %d, want 2", to)
	}
}

func TestSelectAllAndEndCopy(t *testing.T) {
	m := buildCopyModel(t)
	m.BeginCopy()
	if !m.SelectAll() {
		t.Fatal("SelectAll returned false")
	}
	if from, to, _ := m.Selection(); from != 0 || to != 2 {
		t.Fatalf("SelectAll selection = %d..%d, want 0..2", from, to)
	}
	m.EndCopy()
	if m.InCopyMode() || m.HasSelection() {
		t.Fatal("EndCopy left copy state behind")
	}
	if _, ok := m.SelectedText(); ok {
		t.Fatal("SelectedText still reports text after EndCopy")
	}
}

func TestMessageLineMappingAndClicks(t *testing.T) {
	m := buildCopyModel(t)

	if got := m.MessageAtLine(0); got != 0 {
		t.Fatalf("line 0 maps to message %d, want 0", got)
	}
	if got := m.MessageAtLine(999); got != 2 {
		t.Fatalf("line past the end maps to %d, want last message", got)
	}

	// Click the first message's header. x is a column inside the viewport
	// (band-relative), y includes the band origin and the viewport border.
	if !m.HandleClick(10, 1, false) {
		t.Fatal("HandleClick missed the first message")
	}
	if from, to, _ := m.Selection(); from != 0 || to != 0 {
		t.Fatalf("click selection = %d..%d, want 0..0", from, to)
	}
	if !m.InCopyMode() {
		t.Fatal("HandleClick did not enter copy mode")
	}

	// Shift-click the last message to extend.
	lastStart := -1
	for line := 1; line < 30; line++ {
		if m.MessageAtLine(line) == 2 {
			lastStart = line
			break
		}
	}
	if lastStart < 0 {
		t.Fatal("could not locate last message line")
	}
	if !m.HandleClick(10, 1+lastStart, true) {
		t.Fatal("shift-click missed the last message")
	}
	if from, to, _ := m.Selection(); from != 0 || to != 2 {
		t.Fatalf("shift-click selection = %d..%d, want 0..2", from, to)
	}

	// A click on the border / outside the content must not change anything.
	if m.HandleClick(0, 0, false) {
		t.Fatal("HandleClick accepted a border click")
	}
}

func TestViewMarksSelection(t *testing.T) {
	m := buildCopyModel(t)
	m.BeginCopy()
	m.MoveSelection(-1, true) // select tool + assistant

	view := stripANSI(m.View())
	if !strings.Contains(view, "▌") {
		t.Fatalf("selected view missing start marker:\n%s", view)
	}
	if !strings.Contains(view, "▐") {
		t.Fatalf("range view missing end marker:\n%s", view)
	}
	if !strings.Contains(view, "last assistant text") {
		t.Fatalf("view lost message content:\n%s", view)
	}

	m.EndCopy()
	if strings.Contains(stripANSI(m.View()), "▌") {
		t.Fatal("marker still rendered after EndCopy")
	}
}
