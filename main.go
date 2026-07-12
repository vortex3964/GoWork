package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
	"GoWork/Focus"
)

// topBarHeight is the row the tab strip occupies. Kept as a named
// constant (not a magic 1) so the layout math below reads as a budget,
// matching the row-budget approach used for the rest of the screen.
const topBarHeight = 1

type model struct {
	tabs   tabs.Model
	stats  stats.Model
	skills skills.Model

	focus focus.Focus

	winWidth  int
	winHeight int
}

func initialModel() model {
	return model{
		tabs: tabs.New("code", "skills", "stats"),
		// Everything starts focused on the viewport/chat area, not the
		// prompt, so arrow keys and future scroll keys work immediately
		// without the user having to Tab away from an empty input first.
		// (The prompt box isn't built yet, so this mostly matters once
		// it lands — flagging the default here rather than leaving it
		// implicit.)
		focus: focus.Viewport,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.winWidth = msg.Width
		m.winHeight = msg.Height
		m.tabs.SetSize(m.winWidth)

		contentHeight := m.winHeight - topBarHeight
		if contentHeight < 0 {
			contentHeight = 0
		}
		m.stats.SetSize(m.winWidth, contentHeight)
		m.skills.SetSize(m.winWidth, contentHeight)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		// Global keybinds are only live when focus is NOT on the
		// prompt. The prompt box doesn't exist yet, but this branch is
		// written now so adding it later is a matter of forwarding to
		// its Update, not restructuring this switch.
		if m.focus != focus.Prompt {
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "tab":
				m.tabs.Next()
				return m, nil
			case "shift+tab":
				m.tabs.Prev()
				return m, nil
			}
		}

	case tea.MouseMsg:
		if msg.Type == tea.MouseLeft {
			// Tabs occupy row 0 only (topBarHeight == 1). Anything
			// below that Y is not a tab click — once the viewport,
			// prompt box, and status bar exist, this is where their
			// own Y ranges get checked too, each forwarding the click
			// only if msg.Y falls in its row range.
			if msg.Y < topBarHeight {
				m.tabs.HandleClick(msg.X)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.tabs, cmd = m.tabs.Update(msg)
	return m, cmd
}

func (m model) View() string {
	top := m.tabs.View()

	switch m.tabs.Active().Name {
	case "stats":
		return top + "\n" + m.stats.View()
	case "skills":
		return top + "\n" + m.skills.View()
	default:
		// "code" (or anything else) — the main chat screen isn't built
		// yet, so it falls through to a blank content area for now,
		// sized the same way stats/skills are.
		return top
	}
}

func main() {
	p := tea.NewProgram(
		initialModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // required in v1 for any tea.MouseMsg to arrive at all
	)
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
