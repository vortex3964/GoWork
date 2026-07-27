package popup

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

const (
	showDuration = 2 * time.Second

	// defaultMessage is used when a ShowMsg arrives with an empty Message,
	// so callers that just want the classic "copied" toast don't have to
	// repeat the string everywhere.
	defaultMessage = "Copied to clipboard"

	accentBar = "▌"
)

type ShowMsg struct {
	Message string
}

type dismissMsg struct {
	gen int
}

type Model struct {
	visible    bool
	message    string
	gen        int
	termWidth  int
	termHeight int
}

func New() Model {
	return Model{}
}

func (m Model) IsVisible() bool {
	return m.visible
}

func (m *Model) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ShowMsg:
		m.visible = true
		m.message = msg.Message
		if m.message == "" {
			m.message = defaultMessage
		}
		m.gen++
		gen := m.gen
		return m, tea.Tick(showDuration, func(t time.Time) tea.Msg {
			return dismissMsg{gen: gen}
		})
	case dismissMsg:
		// Ignore timers left over from a popup that's since been replaced
		// by a newer one - only the most recent ShowMsg's timer may
		// dismiss.
		if msg.gen == m.gen {
			m.visible = false
		}
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if !m.visible {
		return ""
	}

	accent := lipgloss.NewStyle().
		Foreground(style.Success).
		Background(style.Highlight).
		Render(accentBar)

	text := lipgloss.NewStyle().
		Foreground(style.Success).
		Background(style.Highlight).
		Bold(true).
		Padding(0, 1, 0, 1).
		Render(m.message)

	return lipgloss.NewStyle().
		Background(style.Highlight).
		Render(accent + text)
}

func (m Model) Overlay(bg string) string {
	if !m.visible {
		return bg
	}

	dialog := m.View()
	dw := lipgloss.Width(dialog)

	w := m.termWidth
	h := m.termHeight
	if w <= 0 {
		w = lipgloss.Width(bg)
	}
	if h <= 0 {
		h = lipgloss.Height(bg)
	}

	x := w - dw - 1
	y := 1
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	base := lipgloss.NewLayer(bg).X(0).Y(0).Z(0)
	float := lipgloss.NewLayer(dialog).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, float).Render()
}
