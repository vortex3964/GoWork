package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"GoWork/Tui/Components/Tabs"
)

type model struct {
	tabs      tabs.Model
	winWidth  int
	winHeight int
}

func initialModel() model {
	return model{
		tabs: tabs.New("code", "skills", "stats"),
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
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.tabs, cmd = m.tabs.Update(msg)
	return m, cmd
}

func (m model) View() string {
	return m.tabs.View()
}

func main() {
	if _, err := tea.NewProgram(initialModel(), tea.WithAltScreen()).Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
