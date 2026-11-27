package gowork

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newSlashPaletteModel builds a focused code-tab model with the real command
// registry so the palette key flow is exercised the same way the app uses it.
func newSlashPaletteModel(t *testing.T) model {
	t.Helper()
	m := newSlashTestModel(t)
	m.Prompt.Focus()
	m.promptHeight = m.Prompt.Height()
	return m
}

// typePaletteText feeds printable keys through the normal Update path, which
// is what opens the palette when the user starts a prompt with "/".
func typePaletteText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(model)
	}
	return m
}

func pressPaletteKey(t *testing.T, m model, key tea.KeyPressMsg) model {
	t.Helper()
	updated, _ := m.Update(key)
	return updated.(model)
}

func TestSlashPaletteOpensOnSlashWithFullRegistry(t *testing.T) {
	m := newSlashPaletteModel(t)

	m = typePaletteText(t, m, "/")
	if got := m.Prompt.Value(); got != "/" {
		t.Fatalf("prompt after typing slash = %q, want %q", got, "/")
	}
	if !m.Prompt.PaletteVisible() {
		t.Fatal("typing / did not open the command palette")
	}
	if sel, ok := m.Prompt.PaletteSelected(); !ok || sel.Name != "help" {
		t.Fatalf("initial palette selection = %v (ok=%v), want help", sel, ok)
	}
	if !m.Prompt.Focused() || m.Mode != modePrompt {
		t.Fatalf("opening the palette changed the prompt state: focused=%v mode=%v", m.Prompt.Focused(), m.Mode)
	}

	// The whole registry should be reachable from a bare slash. The current
	// registry fits in one page, so every usage line is rendered above the
	// prompt.
	view := m.Prompt.View()
	for _, c := range slashCommands() {
		usage := c.Usage
		if usage == "" {
			usage = "/" + c.Name
		}
		if !strings.Contains(view, usage) {
			t.Fatalf("palette view is missing %s:\n%s", usage, view)
		}
	}
}

func TestSlashPaletteFiltersAndNavigation(t *testing.T) {
	m := newSlashPaletteModel(t)
	m = typePaletteText(t, m, "/")

	// Down/up move the highlight without touching the prompt text.
	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if sel, _ := m.Prompt.PaletteSelected(); sel.Name != "model" {
		t.Fatalf("selection after down = %s, want model", sel.Name)
	}
	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if sel, _ := m.Prompt.PaletteSelected(); sel.Name != "help" {
		t.Fatalf("selection after up = %s, want help", sel.Name)
	}
	if got := m.Prompt.Value(); got != "/" {
		t.Fatalf("navigation changed the prompt to %q", got)
	}

	// Typing a prefix filters the list and resets the highlight to the first
	// match; the palette keeps the prompt text intact.
	m = typePaletteText(t, m, "st")
	if got := m.Prompt.Value(); got != "/st" {
		t.Fatalf("prompt after filtering = %q, want /st", got)
	}
	if sel, ok := m.Prompt.PaletteSelected(); !ok || sel.Name != "stats" {
		t.Fatalf("filtered selection = %v (ok=%v), want stats", sel, ok)
	}
	view := m.Prompt.View()
	if !strings.Contains(view, "/stats") || !strings.Contains(view, "Open the usage dashboard") {
		t.Fatalf("filtered palette row missing usage/description:\n%s", view)
	}
	if strings.Contains(view, "/model") {
		t.Fatalf("filtered palette still shows /model:\n%s", view)
	}

	// A query with no matches closes the palette but leaves the typed text
	// for the user to correct.
	m = typePaletteText(t, m, "zz")
	if m.Prompt.PaletteVisible() {
		t.Fatal("palette stayed visible for a query with no matches")
	}
	if got := m.Prompt.Value(); got != "/stzz" {
		t.Fatalf("prompt after no-match query = %q, want /stzz", got)
	}
}

func TestSlashPaletteEnterRunsHighlightedCommand(t *testing.T) {
	m := newSlashPaletteModel(t)
	m = typePaletteText(t, m, "/st")
	if sel, ok := m.Prompt.PaletteSelected(); !ok || sel.Name != "stats" {
		t.Fatalf("filtered selection = %v (ok=%v), want stats", sel, ok)
	}

	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Mode != modeStats {
		t.Fatalf("mode after palette enter = %v, want stats", m.Mode)
	}
	if got := m.Tabs.Active().Name; got != "stats" {
		t.Fatalf("active tab after palette enter = %q, want stats", got)
	}
	if got := m.Prompt.Value(); got != "" {
		t.Fatalf("prompt after running the command = %q, want empty", got)
	}
	if m.Prompt.PaletteVisible() {
		t.Fatal("palette stayed visible after running the command")
	}
	if len(m.Context) != 0 {
		t.Fatalf("palette command leaked into model context: %+v", m.Context)
	}
}

func TestSlashPaletteEnterRunsWhileTurnIsRunning(t *testing.T) {
	m := newSlashPaletteModel(t)
	m = typePaletteText(t, m, "/st")
	m.cancel = func() {}
	*m.AiThink = true

	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Mode != modeStats {
		t.Fatalf("mode after palette enter during a turn = %v, want stats", m.Mode)
	}
	if got := m.Tabs.Active().Name; got != "stats" {
		t.Fatalf("palette enter during a turn did not run the command: tab=%q", got)
	}
	if len(m.Context) != 0 {
		t.Fatalf("palette command leaked into model context: %+v", m.Context)
	}
}

func TestSlashPaletteTabCompletesWithoutRunning(t *testing.T) {
	m := newSlashPaletteModel(t)
	m = typePaletteText(t, m, "/st")

	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.Prompt.Value(); got != "/stats " {
		t.Fatalf("prompt after tab = %q, want %q", got, "/stats ")
	}
	if m.Mode != modePrompt {
		t.Fatalf("mode after tab = %v, want prompt (command must not run)", m.Mode)
	}
	if got := m.Tabs.Active().Name; got != "code" {
		t.Fatalf("active tab after tab = %q, want code (command must not run)", got)
	}
	if m.Prompt.PaletteVisible() {
		t.Fatal("palette stayed open after completing the command")
	}
	if !m.Prompt.Focused() {
		t.Fatal("tab completion moved focus away from the prompt")
	}
	if len(m.Context) != 0 {
		t.Fatalf("tab completion leaked into model context: %+v", m.Context)
	}
}

func TestSlashPaletteEscDismissesWithoutClearingPrompt(t *testing.T) {
	m := newSlashPaletteModel(t)
	m = typePaletteText(t, m, "/st")
	if !m.Prompt.PaletteVisible() {
		t.Fatal("palette did not open for /st")
	}
	// Simulate the layout having taken the expanded palette height.
	m.promptHeight = m.Prompt.Height()

	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.Prompt.PaletteVisible() {
		t.Fatal("esc did not dismiss the palette")
	}
	if got := m.Prompt.Value(); got != "/st" {
		t.Fatalf("esc cleared the prompt: got %q, want /st", got)
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("esc changed the prompt state: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}
	if m.promptHeight != m.Prompt.Height() {
		t.Fatalf("layout height after esc = %d, prompt height = %d", m.promptHeight, m.Prompt.Height())
	}

	// The same text stays dismissed until it is edited, then the palette
	// opens again with the new query.
	m = pressPaletteKey(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if got := m.Prompt.Value(); got != "/sta" {
		t.Fatalf("prompt after editing dismissed palette = %q, want /sta", got)
	}
	if !m.Prompt.PaletteVisible() {
		t.Fatal("editing the prompt did not reopen the palette")
	}
	if sel, ok := m.Prompt.PaletteSelected(); !ok || sel.Name != "stats" {
		t.Fatalf("reopened selection = %v (ok=%v), want stats", sel, ok)
	}
}
