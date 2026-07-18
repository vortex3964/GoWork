package main

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"github.com/joho/godotenv"

	"GoWork/Tui/Components/MessageArea"
	"GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"

	"GoWork/providers"
)

const topBarHeight = 1

const spinnerHeight = 1

// to catch errors in case the api call for the ai fails
type aiResponseMsg struct {
	content string
	err     error
}

type aiSelect struct {
	provider string
	key string
	model string
}

type model struct {
	tabs         tabs.Model
	stats        stats.Model
	skills       skills.Model
	prompt       promptbar.Model
	message_area messagearea.Model
	spinner spinner.Model

	// prompt mode
	prompt_mode bool

	// size of the window
	winWidth  int
	winHeight int

	//ai related
	model providers.Provider
	context []providers.Message
	aiThink bool
}

func initialModel(provider providers.Provider) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		tabs:         tabs.New("code", "skills", "stats"),
		prompt:       promptbar.New(),
		message_area: messagearea.New(),
		prompt_mode:  false, // dont start in prompt mode
		spinner: sp,
		context: []providers.Message{},
		model: provider,
		aiThink: false,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

// tea.Cmd that actually calls the AI provider - runs off the main update loop
func generateCmd(p providers.Provider, prompt string, messages []providers.Message) tea.Cmd {
	return func() tea.Msg {
		//TODO: swap context.Background() for something cancelable (e.g. tied
		//to a "stop generating" keybind) once that's wired up.
		result, err := p.Generate(context.Background(), messages)
		if err != nil {
			return aiResponseMsg{err: err}
		}
		return aiResponseMsg{content: result.Content}
	}
}

// promptTop is the row the prompt bar starts on, since it's pinned to the
// bottom of the window on the code tab rather than sitting under the tabs.
func (m model) promptTop() int {
	return m.winHeight - promptbar.Height
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
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
					m.aiThink = true
					m.message_area.AppendMessage(val, true)
					m.context = append(m.context, providers.Message{Role:"user",Content: val})
					m.prompt.Reset()
					cmds = append(cmds, generateCmd(m.model, val , m.context))
					cmds = append(cmds, m.spinner.Tick)
				}
				return m, tea.Batch(cmds...)
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
	case spinner.TickMsg:
		// Only keep re-ticking while we're actually waiting on a response,
		// otherwise the spinner would spin forever in the background.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.aiThink {
			return m, cmd
		}
		return m, nil
	case aiResponseMsg:
		m.aiThink = false
		if msg.err != nil {
			m.message_area.AppendMessage("Error: "+msg.err.Error(), false)
		} else {
			m.message_area.AppendMessage(msg.content, false)
			m.context = append(m.context, providers.Message{Role: "assistant", Content: msg.content})
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
		if m.aiThink {
			content+=m.spinner.View() + " thinking...."
		}
		content += "\n"
		content += m.prompt.View()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // required in v2 for any mouse msg to arrive at all
	return v
}

func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Couldn't locate .env file:", err)
		os.Exit(1)
	}

	apiKey := os.Getenv("API_KEY")

	if apiKey == "" {
		fmt.Println("API_KEY is empty")
		os.Exit(1)
	}

	provider, err := providers.Select_provider("gemini-3.1-flash-lite", apiKey)

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(provider))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
		os.Exit(1)
	}
}
