// Package skillform is the two-field modal that turns prompt-bar text into a
// skill: the user types the skill's name and description, Enter submits when
// BOTH are filled, and anything else just closes the dialog leaving the
// prompt bar untouched. Only a fulfilled SubmitMsg is ever emitted, so the
// caller can't create a skill from a half-filled form.
package skillform

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// SubmitMsg fires exactly once per opened dialog, only when the user filled
// both fields and pressed Enter. The caller still owns validation (slugify,
// overwrite questions, writing the file).
type SubmitMsg struct {
	Name        string
	Description string
}

// Model is the centered modal: two labeled text fields, tab/shift+tab to
// move between them, Enter to submit, Esc to cancel.
type Model struct {
	visible    bool
	name, desc textinput.Model
	active     int // 0 = name field, 1 = description field
	termWidth  int
	termHeight int
}

func New() Model {
	name := textinput.New()
	name.Placeholder = "e.g. review-cleanup"
	name.Prompt = ""
	name.CharLimit = 64

	desc := textinput.New()
	desc.Placeholder = "what this skill is for"
	desc.Prompt = ""
	desc.CharLimit = 140

	name.Blur()
	desc.Blur()
	return Model{name: name, desc: desc, active: -1}
}

func (m *Model) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
	w := m.dialogWidth() - 10
	if w < 20 {
		w = 20
	}
	m.name.SetWidth(w)
	m.desc.SetWidth(w)
}

func (m Model) Visible() bool { return m.visible }

// Open resets the form and focuses the name field.
func (m *Model) Open() {
	m.name.SetValue("")
	m.desc.SetValue("")
	m.active = 0
	m.name.Focus()
	m.desc.Blur()
	m.visible = true
}

func (m *Model) Close() {
	m.visible = false
	m.active = -1
	m.name.Blur()
	m.desc.Blur()
}

// dialogWidth/Height follow ProviderSelect's resize logic: default size,
// clamped down to the terminal minus a small margin.
func (m Model) dialogWidth() int {
	w := 72
	if m.termWidth > 0 && m.termWidth-4 < w {
		w = max(34, m.termWidth-4)
	}
	return w
}

func (m Model) dialogHeight() int {
	h := 12
	if m.termHeight > 0 && m.termHeight-2 < h {
		h = max(8, m.termHeight-2)
	}
	return h
}

func (m *Model) field(i int) *textinput.Model {
	if i == 1 {
		return &m.desc
	}
	return &m.name
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.visible {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			m.Close()
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.name.Value())
			desc := strings.TrimSpace(m.desc.Value())
			if name == "" || desc == "" {
				// Moving through the fields: Enter on the name field jumps
				// to the description (and vice versa) instead of closing
				// the dialog; only a fully filled form submits.
				if name == "" {
					m.active = 0
				} else {
					m.active = 1
				}
				return m, m.refocus()
			}
			m.Close()
			return m, func() tea.Msg {
				return SubmitMsg{Name: name, Description: desc}
			}
		case "tab", "down":
			m.active = (m.active + 1) % 2
			return m, m.refocus()
		case "shift+tab", "up":
			m.active = (m.active - 1 + 2) % 2
			return m, m.refocus()
		}
	}

	// Forward every other key (including text) to the active field.
	var cmd tea.Cmd
	active := m.active
	if active == 1 {
		m.desc, cmd = m.desc.Update(msg)
	} else if active == 0 {
		m.name, cmd = m.name.Update(msg)
	}
	return m, cmd
}

func (m *Model) refocus() tea.Cmd {
	if m.active == 1 {
		m.desc.Focus()
		m.name.Blur()
	} else {
		m.name.Focus()
		m.desc.Blur()
	}
	return nil
}

func (m Model) View() string {
	if !m.visible {
		return ""
	}

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Info).
		Render("New skill")

	sub := lipgloss.NewStyle().
		Foreground(style.Muted).
		Render("the prompt bar text becomes the skill's content")

	row := func(label string, active bool, input textinput.Model) string {
		lblStyle := lipgloss.NewStyle().Bold(true)
		if active {
			lblStyle = lblStyle.Foreground(style.Info)
		} else {
			lblStyle = lblStyle.Foreground(style.Muted)
		}
		return lblStyle.Render(label) + "  " + input.View()
	}

	fields := row("name", m.active == 0, m.name) + "\n" +
		row("description", m.active == 1, m.desc)

	help := lipgloss.NewStyle().
		Foreground(style.Muted).
		Render("enter fills fields, then creates · tab next · esc cancel")

	content := title + "\n" + sub + "\n\n" + fields + "\n\n" + help

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Background(style.Background).
		Padding(1, 2).
		Width(m.dialogWidth()).
		Height(m.dialogHeight()).
		Render(content)
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
