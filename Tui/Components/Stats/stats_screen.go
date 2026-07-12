// Package stats is a placeholder screen. It exists so the "stats" tab
// is functional (navigable, correctly sized, renders something) before
// any real stats content is designed. Fill in Model fields and View()
// later without needing to touch main.go's routing.
package stats

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
		Render("stats nothing here yet")
}
