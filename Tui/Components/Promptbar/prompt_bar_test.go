package promptbar_test

import (
	"testing"

	"GoWork/Tui/Components/CommandPalette"
	"GoWork/Tui/Components/Promptbar"
)

func testCommands() []commandpalette.Command {
	return []commandpalette.Command{
		{Name: "help", Usage: "/help", Description: "Show help"},
		{Name: "stats", Usage: "/stats", Description: "Open stats"},
		{Name: "skills", Usage: "/skills", Description: "Open skills"},
	}
}

func TestPaletteOpensForSlashAndAcceptsSelection(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetCommands(testCommands())

	baseHeight := m.Height()
	m.SetValue("/st")
	if !m.PaletteVisible() {
		t.Fatal("slash command did not open the palette")
	}
	if m.Height() <= baseHeight {
		t.Fatalf("palette did not add height: base=%d now=%d", baseHeight, m.Height())
	}

	sel, ok := m.PaletteSelected()
	if !ok || sel.Name != "stats" {
		t.Fatalf("selected %v (ok=%v), want stats", sel, ok)
	}

	completed, ok := m.AcceptPalette()
	if !ok || completed != "/stats " {
		t.Fatalf("AcceptPalette = %q (ok=%v), want %q", completed, ok, "/stats ")
	}
	m.SetValue(completed)
	if m.PaletteVisible() {
		t.Fatal("palette stayed open after completing the command")
	}
	if m.Height() != baseHeight {
		t.Fatalf("palette height not released: base=%d now=%d", baseHeight, m.Height())
	}
}

func TestPaletteHidesWithArgumentsAndEsc(t *testing.T) {
	m := promptbar.New()
	m.SetCommands(testCommands())

	m.SetValue("/stats")
	if !m.PaletteVisible() {
		t.Fatal("palette should be visible for /stats")
	}
	m.ClosePalette()
	if m.PaletteVisible() {
		t.Fatal("ClosePalette did not hide the palette")
	}

	m.SetValue("/stats now")
	if m.PaletteVisible() {
		t.Fatal("palette stayed visible after an argument")
	}

	m.SetValue("hello")
	if m.PaletteVisible() {
		t.Fatal("palette visible for a non-command prompt")
	}
}
