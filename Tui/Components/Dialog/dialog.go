// Package dialog is a generic center-screen modal with a button row, usable as
// a warning box or confirmation prompt. One instance embeds in the main model
// and is driven with ShowMsg. While visible it swallows keypresses: Enter
// confirms the focused button, Escape cancels. An optional OnSelect callback
// turns a confirmed button into a tea.Msg.
package dialog

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

const (
	defaultTitle  = "Notice"
	defaultButton = "OK"
)

// ShowMsg opens the dialog (or replaces whatever is currently shown).
type ShowMsg struct {
	Title    string // box title, defaults to "Notice"
	Message  string
	Buttons  []string                    // defaults to ["OK"]; the selection is confirmed on Enter
	OnSelect func(button string) tea.Msg // optional: turns the confirmed button into a result message
}

// Model is the generic modal's state.
type Model struct {
	visible    bool
	title      string
	message    string
	buttons    []string
	onSelect   func(string) tea.Msg
	selected   int
	termWidth  int
	termHeight int
}

func New() Model {
	return Model{selected: 0}
}

func (m Model) IsVisible() bool { return m.visible }

func (m *Model) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ShowMsg:
		m.visible = true
		m.title = msg.Title
		m.message = msg.Message
		m.buttons = msg.Buttons
		m.onSelect = msg.OnSelect
		if len(m.buttons) == 0 {
			m.buttons = []string{defaultButton}
		}
		m.selected = 0
		return m, nil

	case tea.KeyPressMsg:
		if !m.visible {
			return m, nil
		}
		n := len(m.buttons)
		if n == 0 {
			return m, nil
		}
		switch msg.String() {
		case "enter":
			return m.dismiss(m.selected)
		case "esc":
			return m.dismiss(-1)
		case "left":
			m.selected = (m.selected - 1 + n) % n
			return m, nil
		case "right", "tab", "shift+tab":
			m.selected = (m.selected + 1) % n
			return m, nil
		}
	}
	return m, nil
}

// dismiss hides the modal and, if the caller wired OnSelect and the user
// confirmed a real button (>= 0), turns that into a tea.Cmd producing msg.
func (m Model) dismiss(selected int) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if cb := m.onSelect; cb != nil && selected >= 0 && selected < len(m.buttons) {
		button := m.buttons[selected]
		cmd = func() tea.Msg { return cb(button) }
	}
	m.visible = false
	m.onSelect = nil
	return m, cmd
}

func (m Model) View() string {
	if !m.visible {
		return ""
	}

	title := lipgloss.NewStyle().
		Foreground(style.Danger).
		Bold(true).
		Render(m.title)

	body := lipgloss.NewStyle().
		Foreground(style.Text).
		PaddingTop(1).
		PaddingBottom(1).
		MaxWidth(m.maxWidth()).
		Render(m.message)

	buttons := m.renderButtons()

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Danger).
		Padding(1, 2).
		Width(m.maxWidth()).
		Render(title + "\n" + body + "\n" + buttons)
}

func (m Model) maxWidth() int {
	w := m.termWidth
	if w > 0 {
		maxWidth := w / 2
		// Keep a sane minimum so small terminals don't collapse the modal.
		if maxWidth < 40 {
			maxWidth = 40
		}
		return maxWidth
	}
	// Fall back to the longest line in the message.
	longest := 0
	for _, line := range strings.Split(m.message, "\n") {
		if l := len(line); l > longest {
			longest = l
		}
	}
	return longest
}

func (m Model) renderButtons() string {
	var sb strings.Builder
	for i, b := range m.buttons {
		st := lipgloss.NewStyle().Padding(0, 2)
		if i == m.selected {
			st = st.
				Background(style.Highlight).
				Foreground(style.Danger).
				Bold(true)
		} else {
			st = st.Foreground(style.Danger)
		}
		sb.WriteString("[" + st.Render(b) + "] ")
	}
	return sb.String()
}

func (m Model) Overlay(bg string) string {
	if !m.visible {
		return bg
	}

	dialogView := m.View()
	w := m.termWidth
	h := m.termHeight
	if w <= 0 {
		w = lipgloss.Width(bg)
	}
	if h <= 0 {
		h = lipgloss.Height(bg)
	}
	dw := lipgloss.Width(dialogView)
	dh := lipgloss.Height(dialogView)
	x := (w - dw) / 2
	y := (h - dh) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	base := lipgloss.NewLayer(bg).X(0).Y(0).Z(0)
	floating := lipgloss.NewLayer(dialogView).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, floating).Render()
}
