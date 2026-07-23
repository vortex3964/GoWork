package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"github.com/joho/godotenv"

	"GoWork/Tui/Components/MessageArea"
	"GoWork/Tui/Components/Promptbar"
	providerselect "GoWork/Tui/Components/ProviderSelect"
	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
	"GoWork/Tui/Style"

	"GoWork/providers"
)

const topBarHeight = 1

const spinnerHeight = 1

func get_supported_providers() []string {
	return []string{"google", "anthropic", "groq", "openAi", "local"}
}

func get_supported_providers_local() []string {
	return []string{"ollama", "llamaCpp", "lmStudio"}
}

// to catch errors in case the api call for the ai fails
type aiResponseMsg struct {
	content string
	usage   providers.Usage
	err     error
}

type modelInfoMsg struct {
	info providers.ModelInfo
	err  error
}

type model struct {
	tabs         tabs.Model
	stats        stats.Model
	skills       skills.Model
	prompt       promptbar.Model
	message_area messagearea.Model
	spinner      spinner.Model

	// prompt mode
	prompt_mode bool

	// size of the window
	winWidth  int
	winHeight int

	//ai related
	model   providers.Provider
	context []providers.Message
	aiThink bool

	// provider/model picker (ctrl+p)
	selectingProvider bool
	providerSelect    providerselect.Model

	//status line data to be displayed
	status statusLine
}

func initialModel(provider providers.Provider, modelID string, providerName string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		tabs:         tabs.New("code", "skills", "stats"),
		prompt:       promptbar.New(),
		message_area: messagearea.New(),
		prompt_mode:  false, // dont start in prompt mode
		spinner:      sp,
		context:      []providers.Message{},
		model:        provider,
		aiThink:      false,
		status:       newStatusLine(providerName, modelID),
	}
}

func (m model) Init() tea.Cmd {
	if m.model == nil {
		return nil
	}
	return fetchModelInfoCmd(m.model, m.status.modelID)
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
		return aiResponseMsg{content: result.Content, usage: result.Usage}
	}
}

func fetchModelInfoCmd(p providers.Provider, model string) tea.Cmd {
	return func() tea.Msg {
		info, err := p.Info(context.Background(), model)
		return modelInfoMsg{info: info, err: err}
	}
}

func (m model) promptTop() int {
	return m.winHeight - statusLineHeight - promptbar.Height
}

func (m *model) openProviderSelect() tea.Cmd {
	m.selectingProvider = true
	if m.prompt_mode {
		m.prompt.Blur()
		m.prompt_mode = false
	}
	m.providerSelect = providerselect.New(
		get_supported_providers(),
		get_supported_providers_local(),
		os.Getenv("API_KEY"),
	)
	m.providerSelect.SetSize(m.winWidth, m.winHeight)
	return m.providerSelect.Init()
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
		// bar, minus the reserved spinner row and the statusline row.
		msgAreaHeight := m.winHeight - topBarHeight - spinnerHeight - statusLineHeight - promptbar.Height
		if msgAreaHeight < 0 {
			msgAreaHeight = 0
		}
		m.message_area.SetSize(m.winWidth, msgAreaHeight)
		if m.selectingProvider {
			m.providerSelect.SetSize(m.winWidth, m.winHeight)
		}
		return m, nil

	case providerselect.SelectedMsg:
		m.selectingProvider = false
		apiKey := msg.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("API_KEY")
		}
		p, err := providers.Select_provider(msg.Provider, msg.ModelID, apiKey)
		if err != nil {
			m.message_area.AppendMessage("Failed to switch provider: "+err.Error(), false)
			return m, nil
		}
		m.model = p
		m.status.providerName = msg.Provider
		m.status.modelID = msg.ModelID
		m.status.contextWindow = 0
		m.status.lastPromptTokens = 0

		keyToSave := ""
		if msg.WroteNewKey {
			keyToSave = msg.APIKey
			_ = os.Setenv("API_KEY", msg.APIKey)
		}
		if err := saveProviderPrefs(msg.Provider, msg.ModelID, keyToSave); err != nil {
			m.message_area.AppendMessage("Switched provider but failed to save .env: "+err.Error(), false)
		} else {
			_ = os.Setenv("PROVIDER", msg.Provider)
			_ = os.Setenv("model", msg.ModelID)
		}
		m.message_area.AppendMessage(
			fmt.Sprintf("Switched to %s / %s", msg.Provider, msg.ModelID),
			false,
		)
		return m, fetchModelInfoCmd(m.model, m.status.modelID)

	case providerselect.CancelledMsg:
		m.selectingProvider = false
		return m, nil

	case tea.KeyPressMsg:
		msg_str := msg.String()

		if msg_str == "ctrl+c" {
			if m.selectingProvider {
				var cmd tea.Cmd
				m.providerSelect, cmd = m.providerSelect.Update(msg)
				return m, cmd
			}
			return m, tea.Quit
		}

		if msg_str == "ctrl+p" && !m.prompt_mode {
			if m.selectingProvider {
				return m, nil
			}
			return m, m.openProviderSelect()
		}

		if m.selectingProvider {
			var cmd tea.Cmd
			m.providerSelect, cmd = m.providerSelect.Update(msg)
			return m, cmd
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
					if m.model == nil {
						m.message_area.AppendMessage(">**ERROR** No provider selected press ctrl+p to pick one.", false)
						m.prompt.Reset()
						m.prompt_mode = false
						m.prompt.Blur()
						//m.prompt.Blur()
						return m , nil
					}
					m.aiThink = true
					m.message_area.AppendMessage(val, true)
					m.context = append(m.context, providers.Message{Role: "user", Content: val})
					m.prompt.Reset()
					cmds = append(cmds, generateCmd(m.model, val, m.context))
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
		if m.selectingProvider {
			return m, nil
		}
		if msg.Button == tea.MouseLeft {
			// Tabs occupy row 0 only (topBarHeight == 1). The prompt bar
			// sits pinned just above the statusline at the bottom of the
			// screen, so it owns exactly promptbar.Height rows starting
			// at promptTop() - anything below that is the statusline,
			// which isn't clickable. Only on the "code" tab, since
			// that's the only screen either is rendered on. Once the
			// message area needs its own click handling (e.g. selecting
			// a message), this is where its Y range gets checked too.
			switch {
			case msg.Y < topBarHeight:
				m.tabs.HandleClick(msg.X)
			case m.tabs.Active().Name == "code" && msg.Y >= m.promptTop() && msg.Y < m.promptTop()+promptbar.Height:
				m.prompt_mode = true
				return m, m.prompt.Focus()
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.selectingProvider {
			var cmd tea.Cmd
			m.providerSelect, cmd = m.providerSelect.Update(msg)
			return m, cmd
		}
		// Scrolling either box doesn't require focus — same as scrolling
		// a window you're not "in" in nvim.
		if m.tabs.Active().Name == "code" {
			promptTop := m.promptTop()
			switch {
			case msg.Y >= promptTop && msg.Y < promptTop+promptbar.Height:
				switch msg.Button {
				case tea.MouseWheelUp:
					m.prompt.ScrollUp()
				case tea.MouseWheelDown:
					m.prompt.ScrollDown()
				}
			case msg.Y >= topBarHeight && msg.Y < promptTop:
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
			var perr *providers.ProviderError
			if errors.As(msg.err, &perr) {
				switch perr.Kind {
				case providers.ErrRateLimited:
					m.message_area.AppendMessage("Rate limited - try again in a moment.", false)
				case providers.ErrAuthFailed:
					m.message_area.AppendMessage("Auth failed - check your API key.", false)
				case providers.ErrContextExceeded:
					m.message_area.AppendMessage("Context window exceeded - try trimming history.", false)
				default:
					m.message_area.AppendMessage("Error: "+perr.Error(), false)
				}
			} else {
				m.message_area.AppendMessage("Error: "+msg.err.Error(), false)
			}
		} else {
			m.message_area.AppendMessage(msg.content, false)
			m.context = append(m.context, providers.Message{Role: "assistant", Content: msg.content})
			m.status.sessionTokens += msg.usage.TotalTokens
			m.status.lastPromptTokens = msg.usage.PromptTokens
		}
		return m, nil
	case modelInfoMsg:
		if msg.err == nil {
			m.status.contextWindow = msg.info.ContextWindow
		}
		return m, nil
	}

	if m.selectingProvider {
		var cmd tea.Cmd
		m.providerSelect, cmd = m.providerSelect.Update(msg)
		return m, cmd
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
			content += m.spinner.View() + " thinking...."
		}
		content += "\n"
		content += m.prompt.View() + "\n"
		content += renderStatusLine(m)
	}

	if m.selectingProvider {
		content = m.providerSelect.Overlay(content)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // required in v2 for any mouse msg to arrive at all
	v.BackgroundColor = style.Background
	return v
}

func envOr(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Couldn't locate .env file:", err)
		os.Exit(1)
	}

	providerName := envOr("PROVIDER")
	if providerName == "" {
		providerName = "None"
	}

	modelName := envOr("model", "MODEL")
	if modelName == "" {
		modelName = "None"
	}

	apiKey := os.Getenv("API_KEY")

	var provider providers.Provider
	if providerName != "None" {
		p, err := providers.Select_provider(providerName, modelName, apiKey)
		if err != nil {
			// Don't kill the app - just start without a provider and
			// let the user pick one with ctrl+p.
			fmt.Println("Warning: couldn't initialize provider:", err)
			providerName = "None"
			modelName = "None"
		} else {
			provider = p
		}
	}

	p := tea.NewProgram(initialModel(provider, modelName, providerName))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
		os.Exit(1)
	}
}
