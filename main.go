package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/joho/godotenv"

	changeslist "GoWork/Tui/Components/ChangesList"
	"GoWork/Tui/Components/MessageArea"
	popup "GoWork/Tui/Components/PopUp"
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

type uiMode int

const (
	modeIdle uiMode = iota
	modePrompt
	modeProviderSelect
	modeChangeHandling
	modeRecall
)

type model struct {
	tabs         tabs.Model
	stats        stats.Model
	skills       skills.Model
	prompt       promptbar.Model
	message_area messagearea.Model
	spinner      spinner.Model

	// which UI mode we're in (idle, prompt entry, provider select, ...)
	mode uiMode

	// size of the window
	winWidth  int
	winHeight int

	//ai related
	model   providers.Provider
	context []providers.Message
	aiThink bool

	// provider/model picker (ctrl+p)
	providerSelect providerselect.Model

	// track the message were on for recall mode
	historyIdx int

	//status line data to be displayed
	status statusLine

	// our logo
	logoLines []string

	// popup notification 
	popUp popup.Model

	// changes list overlay (ctrl+l), replaces message_area in place
	// when open; the prompt bar underneath is untouched either way
	changesList changeslist.Model
}

func loadLogo() []string {
	data, err := os.ReadFile("logo/logo.txt")
	if err != nil {
		return nil
	}
	// Trim a single trailing newline so we don't count a phantom blank
	// line when centering vertically.
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func initialModel(provider providers.Provider, modelID string, providerName string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		tabs:         tabs.New("code", "skills", "stats"),
		prompt:       promptbar.New(),
		message_area: messagearea.New(),
		mode:         modeIdle,
		spinner:      sp,
		context:      []providers.Message{},
		model:        provider,
		aiThink:      false,
		status:       newStatusLine(providerName, modelID),
		logoLines:    loadLogo(),
		popUp:        popup.New(),
		changesList:  changeslist.New(changeslist.NewWatchList()),
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

// submitPrompt sends whatever's currently in the prompt bar as a user
// message (if non-empty), resets the prompt and history state, and kicks
// off the AI generation. 
func (m *model) submitPrompt() []tea.Cmd {
	var cmds []tea.Cmd
	val := m.prompt.Value()
	if val == "" {
		return cmds
	}
	if m.model == nil {
		m.message_area.AppendMessage(">**ERROR** No provider selected press ctrl+p to pick one.", false)
		m.prompt.Reset()
		m.historyIdx = 0
		m.mode = modeIdle
		m.prompt.Blur()
		return cmds
	}
	m.aiThink = true
	m.message_area.AppendMessage(val, true)
	m.context = append(m.context, providers.Message{Role: "user", Content: val})
	m.prompt.Reset()
	m.historyIdx = 0
	cmds = append(cmds, generateCmd(m.model, val, m.context))
	cmds = append(cmds, m.spinner.Tick)
	return cmds
}

func popupCopyMessage(val string) string {
	oneLine := strings.ReplaceAll(strings.TrimSpace(val), "\n", " ")
	const maxLen = 40
	if len(oneLine) > maxLen {
		oneLine = oneLine[:maxLen] + "…"
	}
	if oneLine == "" {
		return "Copied to clipboard"
	}
	return "Copied: " + oneLine
}

func (m *model) openProviderSelect() tea.Cmd {
	m.mode = modeProviderSelect
	m.prompt.Blur()
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
		m.changesList.SetSize(m.winWidth, msgAreaHeight)
		m.popUp.SetSize(m.winWidth, m.winHeight)
		if m.mode == modeProviderSelect {
			m.providerSelect.SetSize(m.winWidth, m.winHeight)
		}
		return m, nil

	case providerselect.SelectedMsg:
		m.mode = modeIdle
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
		m.mode = modeIdle
		return m, nil

	case tea.KeyPressMsg:
		msg_str := msg.String()

		if msg_str == "ctrl+c" {
			if m.mode == modeProviderSelect {
				var cmd tea.Cmd
				m.providerSelect, cmd = m.providerSelect.Update(msg)
				return m, cmd
			}
			return m, tea.Quit
		}

		if msg_str == "ctrl+p" {
			if m.mode == modeProviderSelect {
				return m, nil
			}
			return m, m.openProviderSelect()
		}

		if msg_str == "ctrl+a" {
			val := m.prompt.Value()
			if val != "" {
				return m, func() tea.Msg {
					if err := clipboard.WriteAll(val); err != nil {
						return popup.ShowMsg{Message: "Copy failed: " + err.Error()}
					}
					return popup.ShowMsg{Message: popupCopyMessage(val)}
				}
			}
			return m, nil
		}

		if m.mode == modeProviderSelect {
			var cmd tea.Cmd
			m.providerSelect, cmd = m.providerSelect.Update(msg)
			return m, cmd
		}

		if msg_str == "ctrl+l" {
			if m.mode == modeChangeHandling {
				m.changesList.Close()
				m.mode = modePrompt
				return m, m.prompt.Focus()
			}
			m.prompt.Blur()
			m.mode = modeChangeHandling
			return m, m.changesList.Toggle()
		}

		if m.mode == modeChangeHandling {
			switch msg_str {
			case "esc", "tab", "enter":
				m.changesList.Close()
				m.mode = modePrompt
				return m, m.prompt.Focus()
			case "up":
				m.changesList.CursorUp()
				return m, nil
			case "down":
				m.changesList.CursorDown()
				return m, nil
			default:
				var cmd tea.Cmd
				m.changesList, cmd = m.changesList.Update(msg)
				return m, cmd
			}
		}

		if m.mode == modeRecall {
			switch msg_str {
			case "esc":
				m.prompt.Blur()
				m.mode = modeIdle
				m.historyIdx = 0
				return m, nil
			case "up":
				if val := m.message_area.LastUserMessage(m.historyIdx + 1); val != "" {
					m.historyIdx++
					m.prompt.SetValue(val)
				}
				return m, nil
			case "down":
				if m.historyIdx > 1 {
					m.historyIdx--
					m.prompt.SetValue(m.message_area.LastUserMessage(m.historyIdx))
				} else {
					m.historyIdx = 0
					m.prompt.Reset()
					m.mode = modePrompt
				}
				return m, nil
			case "left", "right":
				var cmd tea.Cmd
				m.prompt, cmd = m.prompt.Update(msg)
				return m, cmd
			case "enter":
				m.mode = modePrompt
				cmds = m.submitPrompt()
				return m, tea.Batch(cmds...)
			default:
				m.mode = modePrompt
				m.historyIdx = 0
				var cmd tea.Cmd
				m.prompt, cmd = m.prompt.Update(msg)
				return m, cmd
			}
		}

		if m.mode == modePrompt {
			switch msg_str {
			case "esc":
				m.prompt.Blur()
				m.mode = modeIdle
				m.historyIdx = 0
				return m, nil
			case "shift+enter", "ctrl+j":
				m.prompt.InsertNewline()
				return m, nil
			case "up":
				// Only recall history when the prompt is empty; otherwise
				// let the textarea handle cursor movement as usual.
				if m.prompt.IsEmpty() {
					if val := m.message_area.LastUserMessage(1); val != "" {
						m.historyIdx = 1
						m.prompt.SetValue(val)
						m.mode = modeRecall
					}
					return m, nil
				}
				var cmd tea.Cmd
				m.prompt, cmd = m.prompt.Update(msg)
				return m, cmd
			case "ctrl+u":
				m.prompt.Reset()
				m.historyIdx = 0
				return m, nil
			case "enter":
				cmds = m.submitPrompt()
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
				m.mode = modePrompt
				return m, m.prompt.Focus()
			}
		}
	case tea.MouseClickMsg:
		if m.mode == modeProviderSelect {
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
				m.historyIdx = 0
				m.mode = modePrompt
				return m, m.prompt.Focus()
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.mode == modeProviderSelect {
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
			case m.mode == modeChangeHandling && msg.Y >= topBarHeight && msg.Y < promptTop:
				// The wheel is reserved for the file-explorer pane
				// while the changes list is open the results list
				// is navigated with the arrow keys instead.
				switch msg.Button {
				case tea.MouseWheelUp:
					m.changesList.ExplorerScrollUp()
				case tea.MouseWheelDown:
					m.changesList.ExplorerScrollDown()
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

	case popup.ShowMsg:
		var cmd tea.Cmd
		m.popUp, cmd = m.popUp.Update(msg)
		cmds = append(cmds, cmd)
	}

	if m.mode == modeProviderSelect {
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
	var popUpCmd tea.Cmd
	m.popUp, popUpCmd = m.popUp.Update(msg)
	return m, tea.Batch(tabsCmd, promptCmd, msgAreaCmd, popUpCmd)
}

func (m model) emptyStateHeight() int {
	h := m.winHeight - topBarHeight - spinnerHeight - statusLineHeight - promptbar.Height
	h -= 2
	if h < 0 {
		h = 0
	}
	return h
}

func (m model) renderLogo() string {
	availH := m.emptyStateHeight()
	if len(m.logoLines) == 0 {
		return lipgloss.Place(m.winWidth, availH, lipgloss.Center, lipgloss.Center, "")
	}

	logoWidth := 0
	for _, line := range m.logoLines {
		if w := lipgloss.Width(line); w > logoWidth {
			logoWidth = w
		}
	}

	block := strings.Join(m.logoLines, "\n")

	return lipgloss.Place(
		m.winWidth, availH,
		lipgloss.Center, lipgloss.Center,
		lipgloss.NewStyle().Width(logoWidth).Render(block),
	)
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
		if m.mode == modeChangeHandling {
			content += m.changesList.View()
		} else if m.message_area.Size() == 0 {
			content += m.renderLogo()
		} else {
			content += m.message_area.View()
		}
		content += "\n"
		if m.aiThink {
			content += lipgloss.NewStyle().Margin(0, 3).Foreground(style.Info).Render(m.spinner.View() + " thinking....")
		}
		content += "\n"
		content += m.prompt.View() + "\n"
		content += renderStatusLine(m)
	}

	if m.mode == modeProviderSelect {
		content = m.providerSelect.Overlay(content)
	}

	if m.popUp.IsVisible() {
		content = m.popUp.Overlay(content)
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
