package tabs

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"GoWork/Tui/Style"
)

type tab struct {
	Name string
}

var gapStyle = lipgloss.NewStyle().
	Border(lipgloss.Border{Bottom: "─"}, false, false, true, false).
	BorderForeground(style.Border)

type Model struct {
	Tabs      []tab
	ActiveIdx int
	width     int
}

func New(names ...string) Model {
	tabs := make([]tab, len(names))
	for i, n := range names {
		tabs[i] = tab{Name: n}
	}
	return Model{Tabs: tabs}
}

func (m *Model) SetSize(width int) {
	m.width = width
}

func (m Model) Active() tab {
	return m.Tabs[m.ActiveIdx]
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			if m.ActiveIdx < len(m.Tabs)-1 {
				m.ActiveIdx++
			}
		case "shift+tab":
			if m.ActiveIdx > 0 {
				m.ActiveIdx--
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	rendered := make([]string, len(m.Tabs))
	for idx, tab := range m.Tabs {
		if idx == m.ActiveIdx {
			rendered[idx] = style.TabStyle.Border(style.ActiveTabBorder).Render(tab.Name)
		} else {
			rendered[idx] = style.TabStyle.Border(style.TabBorder).Render(tab.Name)
		}
	}

	row := lipgloss.JoinHorizontal(lipgloss.Bottom, rendered...)

	gapWidth := m.width - lipgloss.Width(row)
	if gapWidth < 0 {
		gapWidth = 0
	}
	gap := gapStyle.Render(strings.Repeat(" ", gapWidth))

	return lipgloss.JoinHorizontal(lipgloss.Bottom, row, gap)
}
