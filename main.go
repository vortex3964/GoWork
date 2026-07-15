package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"GoWork/Tui/Components/MessageArea"
	"GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
)

const topBarHeight = 1

// spinnerHeight reserves a blank row between the message area and the
// prompt bar — this is where a "thinking" spinner will eventually render.
const spinnerHeight = 1

type model struct {
	tabs         tabs.Model
	stats        stats.Model
	skills       skills.Model
	prompt       promptbar.Model
	message_area messagearea.Model

	// prompt mode
	prompt_mode bool

	// size of the window
	winWidth  int
	winHeight int
}

func initialModel() model {
	return model{
		tabs:         tabs.New("code", "skills", "stats"),
		prompt:       promptbar.New(),
		message_area: messagearea.New(),
		prompt_mode:  false, // dont start in prompt mode
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

// promptTop is the row the prompt bar starts on, since it's pinned to the
// bottom of the window on the code tab rather than sitting under the tabs.
func (m model) promptTop() int {
	return m.winHeight - promptbar.Height
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

		// message area fills everything between the tabs and the prompt
		// bar, minus the reserved spinner row.
		msgAreaHeight := m.winHeight - topBarHeight - spinnerHeight - promptbar.Height
		if msgAreaHeight < 0 {
			msgAreaHeight = 0
		}
		m.message_area.SetSize(m.winWidth, msgAreaHeight)
		return m, nil
	case tea.KeyPressMsg:
		msg_str := msg.String()
		
		if msg_str == "ctrl+c"{
			return m , tea.Quit
		}

		if m.prompt_mode {
			switch msg_str {
			case "esc":
				m.prompt.Blur()
				m.prompt_mode = false
				return m, nil
			case "shift+enter", "ctrl+j":
				m.prompt.InsertNewline()
				return m, nil
			case "enter":
				// Submit whatever's in the prompt bar as a user message,
				// then clear it and stay in prompt mode for the next one.
				if val := m.prompt.Value(); val != "" {
					m.message_area.AppendMessage(val, true)
					m.prompt.Reset()
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.prompt, cmd = m.prompt.Update(msg)
			return m, cmd
		} else {
			switch msg_str {
			case "tab":
				m.tabs.Next()
				return m, nil
			case "shift+tab":
				m.tabs.Prev()
				return m, nil
			case "enter":
				m.prompt_mode = true
				return m, m.prompt.Focus()
			}
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			// Tabs occupy row 0 only (topBarHeight == 1). The prompt bar
			// is pinned to the bottom of the screen, so it owns the last
			// promptbar.Height rows instead but only on the "code" tab,
			// since that's the only screen it's rendered on. Once the
			// message area needs its own click handling (e.g. selecting
			// a message), this is where its Y range gets checked too.
			switch {
			case msg.Y < topBarHeight:
				m.tabs.HandleClick(msg.X)
			case m.tabs.Active().Name == "code" && msg.Y >= m.promptTop():
				m.prompt_mode = true
				return m, m.prompt.Focus()
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		// Scrolling either box doesn't require focus — same as scrolling
		// a window you're not "in" in nvim.
		if m.tabs.Active().Name == "code" {
			promptTop := m.promptTop()
			switch {
			case msg.Y >= promptTop:
				switch msg.Button {
				case tea.MouseWheelUp:
					m.prompt.ScrollUp()
				case tea.MouseWheelDown:
					m.prompt.ScrollDown()
				}
			case msg.Y >= topBarHeight:
				switch msg.Button {
				case tea.MouseWheelUp:
					m.message_area.ScrollUp()
				case tea.MouseWheelDown:
					m.message_area.ScrollDown()
				}
			}
		}
		return m, nil
	}
	var tabsCmd tea.Cmd
	m.tabs, tabsCmd = m.tabs.Update(msg)
	// The prompt bar's cursor blink runs on its own message loop
	// (cursor.BlinkMsg) that doesn't match any case above, so it has to
	// be forwarded here too or the cursor blinks once and then freezes.
	// The message area's viewport has the same needs (e.g. its own
	// internal key/mouse handling), so it gets forwarded the same way.
	var promptCmd tea.Cmd
	m.prompt, promptCmd = m.prompt.Update(msg)
	var msgAreaCmd tea.Cmd
	m.message_area, msgAreaCmd = m.message_area.Update(msg)
	return m, tea.Batch(tabsCmd, promptCmd, msgAreaCmd)
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
		content += top + "\n" 
		content += m.message_area.View() + "\n" 
		content += "\n"
		content += m.prompt.View()
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
