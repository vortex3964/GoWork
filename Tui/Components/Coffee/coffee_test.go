package coffee

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestFrameIsAnimatedAndTUISafe(t *testing.T) {
	first := Frame(0)
	later := Frame(1500 * time.Millisecond)

	if first == "" || later == "" {
		t.Fatal("Frame returned an empty animation")
	}
	if first == later {
		t.Fatal("frames at different times are identical")
	}
	if strings.Count(first, "\n") != rows-1 {
		t.Fatalf("frame has %d lines, want %d", strings.Count(first, "\n")+1, rows)
	}
	if !strings.Contains(first, "⣿") {
		t.Fatalf("frame lost the mug art:\n%s", first)
	}

	for _, bad := range []string{
		"\x1b[2J",     // clear screen
		"\x1b[H",      // jump home
		"\x1b[?25l",   // hide cursor
		"\x1b[?1049h", // alternate screen
		"\x1b[?1049l",
	} {
		if strings.Contains(first, bad) || strings.Contains(later, bad) {
			t.Errorf("frame contains terminal control sequence %q", bad)
		}
	}
	if !strings.HasSuffix(first, "\x1b[0m") {
		t.Error("frame does not end with a style reset")
	}
}

func TestOverlayPreservesBackgroundDimensions(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.Start(Config{})

	bg := lipgloss.NewStyle().Width(80).Height(24).Render("host screen")
	out := m.Overlay(bg)

	if w := lipgloss.Width(out); w != 80 {
		t.Fatalf("overlay width = %d, want 80", w)
	}
	if h := lipgloss.Height(out); h != 24 {
		t.Fatalf("overlay height = %d, want 24", h)
	}
	if !strings.Contains(out, "⣿") {
		t.Fatal("overlay is missing the mug")
	}

	m.Stop()
	if got := m.Overlay(bg); got != bg {
		t.Fatal("hidden overlay changed the background")
	}
}

func TestOverlayClipsToSmallTerminal(t *testing.T) {
	m := New()
	m.SetSize(20, 8)
	m.StartDefault()

	bg := lipgloss.NewStyle().Width(20).Height(8).Render(strings.Repeat(".", 20))
	out := m.Overlay(bg)
	if w, h := lipgloss.Width(out), lipgloss.Height(out); w != 20 || h != 8 {
		t.Fatalf("small overlay = %dx%d, want 20x8", w, h)
	}
}

func TestStartTicksAndFinishes(t *testing.T) {
	m := New()
	cmd := m.Start(Config{Duration: 100 * time.Millisecond, FPS: 10})
	if cmd == nil {
		t.Fatal("Start returned no tick command")
	}
	if !m.IsVisible() {
		t.Fatal("Start did not show the animation")
	}
	if m.frames != 1 || m.fps != 10 {
		t.Fatalf("config = %d frames at %d fps, want 1 at 10", m.frames, m.fps)
	}

	m, next := m.Update(tickMsg{gen: m.gen})
	if next != nil {
		t.Fatal("finished animation scheduled another tick")
	}
	if m.IsVisible() {
		t.Fatal("animation still visible after its last frame")
	}

	// Stale ticks from an older run must be ignored.
	m.StartDefault()
	gen := m.gen
	m.Stop()
	m, next = m.Update(tickMsg{gen: gen})
	if m.IsVisible() || next != nil {
		t.Fatal("stale tick revived a stopped animation")
	}
}

func TestInterruptKeysStop(t *testing.T) {
	cases := []tea.KeyPressMsg{
		{Code: tea.KeyEsc},
		{Code: 'c', Mod: tea.ModCtrl},
		{Code: 'q', Text: "q"},
	}
	for _, key := range cases {
		m := New()
		m.StartDefault()
		m, cmd := m.Update(key)
		if cmd != nil {
			t.Fatalf("%q returned a command", key.String())
		}
		if m.IsVisible() {
			t.Fatalf("%q did not stop the animation", key.String())
		}
	}
}

func TestConfigDefaultsAndClamp(t *testing.T) {
	c := Config{}.normalized()
	if c.Duration != defaultDuration || c.FPS != defaultFPS {
		t.Fatalf("zero config = %+v, want defaults", c)
	}
	c = Config{Duration: time.Second, FPS: 1000}.normalized()
	if c.FPS != maxFPS {
		t.Fatalf("high fps clamped to %d, want %d", c.FPS, maxFPS)
	}
}
