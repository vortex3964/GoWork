// Package skills is a placeholder screen, structurally identical to
// stats. Two near-empty screens instead of one shared "empty screen"
// type on purpose — they'll diverge in content almost immediately, and
// keeping them as separate small files now matches the eventual shape
// (each screen owns its own state/Update/View) rather than requiring a
// later split.
package skills 

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

type Model struct {
	width  int
	height int
}

func New() Model {
	return Model{}
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	return m, nil
}

func (m Model) View() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Foreground(style.Muted).
		Render("skills nothing here yet")
}
