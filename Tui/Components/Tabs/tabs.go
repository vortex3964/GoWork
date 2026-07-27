package tabs

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

type tab struct {
	Name string
}

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

func (m *Model) Next() {
	if m.ActiveIdx < len(m.Tabs)-1 {
		m.ActiveIdx++
	}
}

func (m *Model) Prev() {
	if m.ActiveIdx > 0 {
		m.ActiveIdx--
	}
}

// clickRect + hit-testing: lets main.go forward a mouse click's X
// coordinate and have the tab strip figure out which tab (if any) was
// hit, without main.go needing to know tab widths itself.
type clickRect struct {
	x0, x1 int
	idx    int
}

func (m Model) rects() []clickRect {
	rects := make([]clickRect, 0, len(m.Tabs))
	x := 0
	for idx, t := range m.Tabs {
		w := lipgloss.Width(styleFor(idx == m.ActiveIdx).Render(t.Name))
		rects = append(rects, clickRect{x0: x, x1: x + w, idx: idx})
		x += w
	}
	return rects
}

// HandleClick returns true (and updates ActiveIdx) if the given X
// coordinate landed on a tab. Y is not checked here main.go is
// expected to only forward clicks whose Y already matches the top-bar
// row, since that's the only row tabs occupies.
func (m *Model) HandleClick(x int) bool {
	for _, r := range m.rects() {
		if x >= r.x0 && x < r.x1 {
			m.ActiveIdx = r.idx
			return true
		}
	}
	return false
}

func styleFor(active bool) lipgloss.Style {
	if active {
		return style.TabStyle.Border(style.ActiveTabBorder)
	}
	return style.TabStyle.Border(style.TabBorder)
}

// Update now only exists to satisfy the same shape every other
// sub-model uses; it deliberately does nothing with tea.KeyMsg. Mouse
// clicks are routed via HandleClick from main.go instead of tea.MouseMsg
// directly, so tabs doesn't need to know its own screen position.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	return m, nil
}

var padStyle = lipgloss.NewStyle().Foreground(style.TabAccent)

func (m Model) View() string {
	rendered := make([]string, len(m.Tabs))
	for idx, t := range m.Tabs {
		rendered[idx] = styleFor(idx == m.ActiveIdx).Render(t.Name)
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Bottom, rendered...)

	if m.width > 0 {
		w := lipgloss.Width(tabs)
		if pad := m.width - w; pad > 0 {
			lines := strings.Split(tabs, "\n")
			for i, line := range lines {
				if i == len(lines)-1 {
					lines[i] = line + padStyle.Render(strings.Repeat("─", pad))
				} else {
					lines[i] = line + strings.Repeat(" ", pad)
				}
			}
			tabs = strings.Join(lines, "\n")
		}
	}

	return tabs
}
