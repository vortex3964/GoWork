package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
)

const topBarHeight = 1

type model struct {
	tabs   tabs.Model
	stats  stats.Model
	skills skills.Model
	prompt promptbar.Model
	// prompt mode
	prompt_mode bool
	// size of the window
	winWidth  int
	winHeight int
}

func initialModel() model {
	return model{
		tabs:        tabs.New("code", "skills", "stats"),
		prompt:      promptbar.New(),
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
		m.prompt.SetWidth(m.winWidth)
		contentHeight := m.winHeight - topBarHeight
		if contentHeight < 0 {
			contentHeight = 0
		}
		m.stats.SetSize(m.winWidth, contentHeight)
		m.skills.SetSize(m.winWidth, contentHeight)
		return m, nil
	case tea.KeyPressMsg:
		if m.prompt_mode {
			if msg.String() == "esc" {
				m.prompt.Blur()
				m.prompt_mode = false
				return m, nil
			}
			var cmd tea.Cmd
			m.prompt, cmd = m.prompt.Update(msg)
			return m, cmd
		} else {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "tab":
				m.tabs.Next()
				return m, nil
			case "shift+tab":
				m.tabs.Prev()
				return m, nil
			case "enter":
				m.prompt_mode=true
				return m,m.prompt.Focus()
			}
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			// Tabs occupy row 0 only (topBarHeight == 1). Below that,
			// the prompt box owns the next promptbar.Height rows — but
			// only on the "code" tab, since that's the only screen it's
			// rendered on. Once the viewport and status bar exist, this
			// is where their own Y ranges get checked too, each
			// forwarding the click only if msg.Y falls in its range.
			switch {
			case msg.Y < topBarHeight:
				m.tabs.HandleClick(msg.X)
			case m.tabs.Active().Name == "code" && msg.Y < topBarHeight+promptbar.Height:
				m.prompt_mode = true
				return m, m.prompt.Focus()
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		// Scrolling the prompt box doesn't require focus — same as
		// scrolling a window you're not "in" in nvim.
		if m.tabs.Active().Name == "code" && msg.Y < topBarHeight+promptbar.Height {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.prompt.ScrollUp()
			case tea.MouseWheelDown:
				m.prompt.ScrollDown()
			}
		}
		return m, nil
	}
	var tabsCmd tea.Cmd
	m.tabs, tabsCmd = m.tabs.Update(msg)
	// The prompt bar's cursor blink runs on its own message loop
	// (cursor.BlinkMsg) that doesn't match any case above, so it has to
	// be forwarded here too or the cursor blinks once and then freezes.
	var promptCmd tea.Cmd
	m.prompt, promptCmd = m.prompt.Update(msg)
	return m, tea.Batch(tabsCmd, promptCmd)
}

func (m model) View() tea.View {
	top := m.tabs.View()
	var content string
	switch m.tabs.Active().Name {
	case "stats":
		content = top + "\n" + m.stats.View()
	case "skills":
		content = top + "\n" + m.skills.View()
	default:
		//this is the main screen
		content = top + "\n" + m.prompt.View()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // required in v2 for any mouse msg to arrive at all
	return v
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
