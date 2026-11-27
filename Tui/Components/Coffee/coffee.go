package coffee

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	defaultDuration = 5 * time.Second
	defaultFPS      = 24
	maxFPS          = 60
)

// Config controls one animation run. Zero values pick the defaults.
type Config struct {
	Duration time.Duration
	FPS      int
}

func (c Config) normalized() Config {
	if c.Duration <= 0 {
		c.Duration = defaultDuration
	}
	if c.FPS <= 0 {
		c.FPS = defaultFPS
	}
	if c.FPS > maxFPS {
		c.FPS = maxFPS
	}
	return c
}

// tickMsg advances the animation. The generation counter makes ticks from a
// stopped/replaced animation harmless.
type tickMsg struct{ gen int }

// Model is the /coffee overlay state. It owns only visibility, timing and
// the frame counter; rendering lives in frame.go.
type Model struct {
	visible bool
	frame   int
	frames  int
	fps     int

	gen int

	termWidth  int
	termHeight int
}

// New returns a hidden coffee model. The command layer calls Start when the
// /coffee command is implemented.
func New() Model { return Model{} }

// IsVisible reports whether the animation should be drawn.
func (m Model) IsVisible() bool { return m.visible }

// SetSize records the host terminal size so Overlay can center itself.
func (m *Model) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
}

// Start begins the animation, replacing any run in progress, and returns the
// first frame tick. Duration/FPS come from cfg; zero fields use defaults.
func (m *Model) Start(cfg Config) tea.Cmd {
	cfg = cfg.normalized()
	m.visible = true
	m.frame = 0
	m.fps = cfg.FPS
	m.frames = int(cfg.Duration.Seconds() * float64(cfg.FPS))
	if m.frames < 1 {
		m.frames = 1
	}
	m.gen++
	return m.tick(m.gen)
}

// StartDefault starts the standard five-second, 24 fps animation.
func (m *Model) StartDefault() tea.Cmd { return m.Start(Config{}) }

// Stop hides the animation immediately and invalidates pending ticks.
func (m *Model) Stop() {
	m.visible = false
	m.gen++
}

// FrameIndex returns the current animation frame number (0-based).
func (m Model) FrameIndex() int { return m.frame }

func (m *Model) tick(gen int) tea.Cmd {
	interval := time.Second / time.Duration(m.fps)
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return tickMsg{gen: gen}
	})
}

// Update advances ticks and handles the interrupt keys. Esc, ctrl+c and q
// close the animation without touching the host screen or cursor.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if !m.visible || msg.gen != m.gen {
			return m, nil
		}
		m.frame++
		if m.frame >= m.frames {
			m.visible = false
			return m, nil
		}
		return m, m.tick(msg.gen)
	case tea.KeyPressMsg:
		if !m.visible {
			return m, nil
		}
		switch msg.String() {
		case "esc", "ctrl+c", "q":
			m.Stop()
			return m, nil
		}
	}
	return m, nil
}

// View renders the current frame; it is empty while hidden.
func (m Model) View() string {
	if !m.visible {
		return ""
	}
	t := time.Duration(0)
	if m.fps > 0 {
		t = time.Duration(float64(m.frame) / float64(m.fps) * float64(time.Second))
	}
	return Frame(t)
}

// Overlay composites the animation centered over bg. Background content and
// its dimensions are preserved; the animation is clipped if the terminal is
// smaller than the mug block.
func (m Model) Overlay(bg string) string {
	if !m.visible {
		return bg
	}
	w, h := m.termWidth, m.termHeight
	if w <= 0 {
		w = lipgloss.Width(bg)
	}
	if h <= 0 {
		h = lipgloss.Height(bg)
	}
	if w <= 0 || h <= 0 {
		return bg
	}

	dialog := lipgloss.NewStyle().MaxWidth(w).MaxHeight(h).Render(m.View())
	dw := lipgloss.Width(dialog)
	dh := lipgloss.Height(dialog)

	x := (w - dw) / 2
	y := (h - dh) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	base := lipgloss.NewLayer(bg).X(0).Y(0).Z(0)
	float := lipgloss.NewLayer(dialog).X(x).Y(y).Z(1)
	out := lipgloss.NewCompositor(base, float).Render()
	// The compositor drops trailing blank cells; pad the result back out so
	// the overlay always keeps the host frame's exact dimensions.
	if lipgloss.Width(out) != w || lipgloss.Height(out) != h {
		out = lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, out)
	}
	return out
}
