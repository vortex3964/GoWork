package commandpalette

import (
	"strings"
	"testing"
)

func testCommands() []Command {
	return []Command{
		{Name: "help", Usage: "/help", Description: "Show help"},
		{Name: "model", Usage: "/model", Description: "Pick a model"},
		{Name: "stats", Usage: "/stats", Description: "Open stats"},
		{Name: "skills", Usage: "/skills", Description: "Open skills"},
		{Name: "context", Usage: "/context [path]", Description: "Add a file"},
		{Name: "coffee", Usage: "/coffee", Description: "Coffee break"},
		{Name: "lsp", Usage: "/lsp", Description: "Language servers"},
	}
}

func names(cmds []Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func TestSyncFiltersByPrefix(t *testing.T) {
	m := New(testCommands()...)

	if m.Visible() {
		t.Fatal("palette starts visible")
	}
	m.Sync("/")
	if !m.Visible() {
		t.Fatal("bare slash did not show all commands")
	}
	if got := len(m.Matches()); got != len(testCommands()) {
		t.Fatalf("bare slash matched %d commands, want %d", got, len(testCommands()))
	}

	m.Sync("/st")
	got := names(m.Matches())
	if len(got) != 1 || got[0] != "stats" {
		t.Fatalf("/st matched %v, want [stats]", got)
	}
	if sel, ok := m.Selected(); !ok || sel.Name != "stats" {
		t.Fatalf("/st selected %v (ok=%v), want stats", sel, ok)
	}

	m.Sync("/C")
	got = names(m.Matches())
	if len(got) != 2 || got[0] != "context" || got[1] != "coffee" {
		t.Fatalf("/C matched %v, want [context coffee]", got)
	}

	m.Sync("/zzz")
	if m.Visible() {
		t.Fatal("no-match query stayed visible")
	}
	if m.Height() != 0 {
		t.Fatalf("no-match height = %d, want 0", m.Height())
	}
}

func TestSyncHidesOnceArgumentsStart(t *testing.T) {
	m := New(testCommands()...)
	m.Sync("/context")
	if !m.Visible() {
		t.Fatal("/context did not show the palette")
	}
	m.Sync("/context docs/file.md")
	if m.Visible() {
		t.Fatal("palette stayed open after an argument")
	}
	if m.Height() != 0 {
		t.Fatalf("argument palette height = %d, want 0", m.Height())
	}
}

func TestMoveWrapsAndClamps(t *testing.T) {
	m := New(testCommands()...)
	m.Sync("/")

	if sel, _ := m.Selected(); sel.Name != "help" {
		t.Fatalf("initial selection = %s, want help", sel.Name)
	}
	m.Move(-1)
	if sel, _ := m.Selected(); sel.Name != "lsp" {
		t.Fatalf("wrapped selection = %s, want lsp", sel.Name)
	}
	m.Move(1)
	if sel, _ := m.Selected(); sel.Name != "help" {
		t.Fatalf("wrapped selection = %s, want help", sel.Name)
	}
	for i := 0; i < len(testCommands())*3; i++ {
		m.Move(1)
	}
	if sel, _ := m.Selected(); sel.Name != "help" {
		t.Fatalf("selection after full cycles = %s, want help", sel.Name)
	}
}

func TestCloseStaysClosedUntilEdit(t *testing.T) {
	m := New(testCommands()...)
	m.Sync("/st")
	if !m.Visible() {
		t.Fatal("palette not visible before close")
	}
	m.Close()
	if m.Visible() {
		t.Fatal("Close did not hide the palette")
	}

	// Same text must stay dismissed.
	m.Sync("/st")
	if m.Visible() {
		t.Fatal("dismissed palette reopened on the same text")
	}

	// Editing the query opens it again.
	m.Sync("/sta")
	if !m.Visible() {
		t.Fatal("edited query did not reopen the palette")
	}
}

func TestViewAndHeightMatch(t *testing.T) {
	m := New(testCommands()...)
	m.SetWidth(60)
	m.Sync("/")
	view := m.View()
	if got := strings.Count(view, "\n") + 1; got != m.Height() {
		t.Fatalf("view has %d lines, Height() = %d", got, m.Height())
	}
	if !strings.Contains(view, "/help") {
		t.Fatalf("view missing /help:\n%s", view)
	}

	m.Sync("/model")
	view = m.View()
	if strings.Contains(view, "/stats") {
		t.Fatalf("filtered view still shows /stats:\n%s", view)
	}
	if !strings.Contains(view, "/model") {
		t.Fatalf("filtered view missing /model:\n%s", view)
	}
}
