package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
)

const topBarHeight = 1

type model struct {
	tabs   tabs.Model
	stats  stats.Model
	skills skills.Model

	// prompt mode
	prompt_mode bool

	// size of the window
	winWidth  int
	winHeight int
}

func initialModel() model {
	return model{
		tabs: tabs.New("code", "skills", "stats"),
		prompt_mode: false, // dont start in prompt mode
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
		if m.prompt_mode{
			if msg.String() == "esc"{
				m.prompt_mode = false
			}
		}else {
			switch msg.String() {
			case "ctrl+c":
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
