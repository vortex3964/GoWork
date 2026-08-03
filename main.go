package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/joho/godotenv"

	changeslist "GoWork/Tui/Components/ChangesList"
	dialog "GoWork/Tui/Components/Dialog"
	"GoWork/Tui/Components/MessageArea"
	popup "GoWork/Tui/Components/PopUp"
	"GoWork/Tui/Components/Promptbar"
	providerselect "GoWork/Tui/Components/ProviderSelect"
	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
	"GoWork/Tui/Style"

	//import all the tool packages
	"GoWork/tools"
	"GoWork/tools/CreateFileTool"
	"GoWork/tools/DeleteFileTool"
	"GoWork/tools/EditFileTool"
	"GoWork/tools/FilesInfoTool"
	"GoWork/tools/GrepFileTool"
	"GoWork/tools/MoveFileTool"
	"GoWork/tools/ReadFileTool"
	"GoWork/tools/WebFetchTool"
	"GoWork/tools/WebSearchTool"
	"GoWork/tools/WriteFileTool"

	"GoWork/providers"
)

const topBarHeight = 1

const spinnerHeight = 1

const sys_prompt_path = "prompts/system_prompt.md"

// inject_vars_in_sys_prompt reads prompts/system_prompt.md, substitutes the
// ${...} environment placeholders, and returns the rendered prompt.
// root is the already-resolved project root (only its last dir is used).
func inject_vars_in_sys_prompt(root string) (string, error) {
	data, err := os.ReadFile(sys_prompt_path)
	if err != nil {
		return "", err
	}

	prompt := string(data)

	projectRoot := ""
	if root != "" {
		projectRoot = filepath.Base(root)
	}
	prompt = strings.ReplaceAll(prompt, "${PROJECT_ROOT}", projectRoot)

	prompt = strings.ReplaceAll(prompt, "${PLATFORM}", shellOut("uname", "-s"))
	if shellOut("git", "rev-parse", "--is-inside-work-tree") == "true" {
		prompt = strings.ReplaceAll(prompt, "${IS_GIT_REPO}", "yes")
	} else {
		prompt = strings.ReplaceAll(prompt, "${IS_GIT_REPO}", "no")
	}

	return prompt, nil
}

// shellOut runs a command and returns its trimmed stdout, or "" on failure.
func shellOut(arg ...string) string {
	out, err := exec.Command(arg[0], arg[1:]...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func get_supported_providers() []string {
	return []string{"google", "anthropic", "groq", "openAi", "local"}
}

func get_supported_providers_local() []string {
	return []string{"ollama", "llamaCpp", "lmStudio"}
}

// to catch errors in case the api call for the ai fails
type aiResponseMsg struct {
	content    string
	toolCalls  []providers.ToolCall
	stopReason string
	usage      providers.Usage
	err        error
}

// toolResultMsg reports a single finished tool call back to the update loop.
// index points at the pending tool it belongs to (into model.pendingTools).
type toolResultMsg struct {
	index  int
	result tools.ToolResult
}

type modelInfoMsg struct {
	info providers.ModelInfo
	err  error
}

// pendingTool is one assistant-requested tool call waiting to run, paired
// with the message-area index its status line is rendered at.
type pendingTool struct {
	call       providers.ToolCall
	messageIdx int
}

// defaultMaxAgentSteps is used when MAX_AGENT_STEPS isn't set (or parses to
// zero). It guards the agentic loop against runaway tool-call chains (a model
// stuck re-calling the same tool, etc.) - same role as opencode's "steps" /
// MAX_STEPS_PROMPT guardrail.
const defaultMaxAgentSteps = 40

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
	changesList  changeslist.Model

	// which UI mode we're in (idle, prompt entry, provider select, ...)
	mode uiMode

	// size of the window
	winWidth  int
	winHeight int

	//ai related
	model   *tools.ProviderHolder
	context []providers.Message
	aiThink *bool

	//tool call
	tool_dispatcher *tools.Dispatcher

	// agentic loop state: the tool calls we're still waiting on (run
	// one at a time) and how many tool-turns this user request has taken.
	pendingTools []pendingTool
	stepCount    int
	// maxSteps is the per-prompt cap on tool turns before the loop forces a
	// stop; configured via MAX_AGENT_STEPS and defaulted in initialModel.
	maxSteps int

	// provider/model picker (ctrl+p)
	providerSelect providerselect.Model

	// track the message were on for recall mode
	historyIdx int

	//status line data to be displayed
	status statusLine

	// our logo
	logoLines []string

	// error popup notification
	popUp popup.Model

	// generic centered modal (tool-support warnings, future confirmations)
	modal dialog.Model
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

// call new on all of our tools
func initTools() []tools.AgentTool {
	return []tools.AgentTool{
		createfiletool.New(),
		deletefiletool.New(),
		editfiletool.New(),
		fileinfo.New(),
		grepfiletool.New(),
		movefiletool.New(),
		readfiletool.New(),
		webfetchtool.New(),
		websearchtool.New(),
		writefiletool.New(),
	}
}

func initialModel(root string, provider providers.Provider, modelID string, providerName string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	think := false

	wl, err := changeslist.NewWatchList(&think)
	if err != nil {
		log.Printf("failed to create file watcher: %v; file watching disabled", err)
		wl = &changeslist.WatchList{
			WatchedFiles: make(map[string]struct{}),
			Changeslist:  make(map[string]changeslist.ChangeList),
			Watcher:      nil,
			WatchedDirs:  make(map[string]struct{}),
		}
		wl.SetThink(&think)
	}

	holder := &tools.ProviderHolder{}
	holder.Set(provider)

	t := initTools()

	arr := make([]providers.ToolDef, len(t))

	for i, tool := range t {
		arr[i] = providers.ToolDef{Name: tool.Name(), Description: tool.Description(), InputSchema: tool.InputSchema()}
	}

	providers.InitToolsDef(arr)

	dispatcher, err := tools.InitDispacher(root, wl, holder.Get, t...)
	if err != nil {
		log.Printf("failed to init dispatcher: %v", err)
	}

	p := promptbar.New()
	p.Focus()

	return model{
		tabs:            tabs.New("code", "skills", "stats"),
		prompt:          p,
		message_area:    messagearea.New(),
		mode:            modePrompt,
		spinner:         sp,
		context:         []providers.Message{},
		model:           holder,
		aiThink:         &think,
		maxSteps:        maxAgentStepsFromEnv(),
		status:          newStatusLine(root, providerName, modelID),
		logoLines:       loadLogo(),
		popUp:           popup.New(),
		modal:           dialog.New(),
		changesList:     changeslist.New(wl),
		tool_dispatcher: dispatcher,
	}
}

// maxAgentStepsFromEnv reads MAX_AGENT_STEPS (a positive int) and falls back
// to defaultMaxAgentSteps when unset, empty, or invalid.
func maxAgentStepsFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("MAX_AGENT_STEPS"))
	if raw == "" {
		return defaultMaxAgentSteps
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		log.Printf("ignoring invalid MAX_AGENT_STEPS=%q, using %d", raw, defaultMaxAgentSteps)
		return defaultMaxAgentSteps
	}
	return n
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.model != nil {
		if p := m.model.Get(); p != nil {
			cmds = append(cmds, fetchModelInfoCmd(p, m.status.modelID))
		}
	}
	cmds = append(cmds, m.changesList.WatchCmd())
	if m.status.providerName != "" && m.status.modelID != "" {
		cmds = append(cmds, toolSupportWarningCmd(m.status.modelID, m.status.providerName))
	}
	return tea.Batch(cmds...)
}

// tea.Cmd that actually calls the AI provider - runs off the main update loop
func generateCmd(p providers.Provider, messages []providers.Message) tea.Cmd {
	return func() tea.Msg {
		//TODO: swap context.Background() for something cancelable (e.g. tied
		//to a "stop generating" keybind) once that's wired up.
		result, err := p.Generate(context.Background(), messages)
		if err != nil {
			return aiResponseMsg{err: err}
		}
		return aiResponseMsg{
			content:    result.Content,
			toolCalls:  result.ToolCalls,
			stopReason: result.StopReason,
			usage:      result.Usage,
		}
	}
}

// contextForModel returns the message history we actually send to the
// provider on this turn. Full history is kept in m.context for future turns;
// this bounds it to the model's context window (when known) so the original
// user request is never the thing that gets dropped when the window fills.
func (m *model) contextForModel() []providers.Message {
	return providers.TrimContext(m.context, m.status.contextWindow)
}

// runToolCmd executes a single tool call off the update loop. Tool calls run
// strictly sequentially (the WatchList backing them isn't thread-safe), so
// the next one is only kicked off once this one reports back. Panics inside
// a tool are recovered and surfaced as an error result instead of killing
// the whole app (same idea as opencode's runToolSafely).
func runToolCmd(d *tools.Dispatcher, call providers.ToolCall, index int) tea.Cmd {
	return func() tea.Msg {
		var res tools.ToolResult
		func() {
			defer func() {
				if r := recover(); r != nil {
					res = tools.Errf("tool %s panicked: %v", call.Tool_name, r)
				}
			}()
			res = d.Dispach(context.Background(), tools.ToolUse{Name: call.Tool_name, Input: call.Input})
		}()
		return toolResultMsg{index: index, result: res}
	}
}

// toolOutputForContext bounds how much of a tool's output gets written back
// into context so a single huge result can't blow the window (opencode caps
// tool output at 50KB for the same reason).
func toolOutputForContext(content string) string {
	const maxChars = 24 * 1024
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "\n…[tool output truncated - too long to keep in context]"
}

func fetchModelInfoCmd(p providers.Provider, model string) tea.Cmd {
	return func() tea.Msg {
		info, err := p.Info(context.Background(), model)
		return modelInfoMsg{info: info, err: err}
	}
}

// toolSupportWarningCmd opens the centered modal when the selected model can't
// call tools. No-op if the provider/model is "None" (i.e. nothing was picked).
func toolSupportWarningCmd(modelName, providerName string) tea.Cmd {
	if providerName == "" || modelName == "" ||
		strings.EqualFold(providerName, "None") || strings.EqualFold(modelName, "None") {
		return nil
	}
	if providers.ModelSupportsTools(providerName, modelName) {
		return nil
	}
	return func() tea.Msg {
		return dialog.ShowMsg{
			Title:   "Tool calling unsupported",
			Message: fmt.Sprintf("%s doesn't support tool calling.", modelName),
			Buttons: []string{"OK"},
		}
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
	// Block submitting a second request while one is still running (the
	// agentic loop can hold aiThink true across many tool turns, so this
	// guards against launching concurrent generations).
	if *m.aiThink {
		m.message_area.AppendMessage(">**INFO** Still working - wait for the current turn to finish.", false)
		return cmds
	}
	var p providers.Provider
	if m.model != nil {
		p = m.model.Get()
	}
	if p == nil {
		m.message_area.AppendMessage(">**ERROR** No provider selected press ctrl+p to pick one.", false)
		m.prompt.Reset()
		m.historyIdx = 0
		m.mode = modeIdle
		m.prompt.Blur()
		return cmds
	}
	*m.aiThink = true
	m.changesList.PauseWatching()
	m.message_area.AppendMessage(val, true)
	m.context = append(m.context, providers.Message{Role: "user", Content: val})
	m.prompt.Reset()
	m.historyIdx = 0
	m.stepCount = 0
	m.pendingTools = m.pendingTools[:0]
	cmds = append(cmds, generateCmd(p, m.contextForModel()))
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
		m.modal.SetSize(m.winWidth, m.winHeight)
		if m.mode == modeProviderSelect {
			m.providerSelect.SetSize(m.winWidth, m.winHeight)
		}
		return m, nil

	case providerselect.SelectedMsg:
		m.mode = modePrompt
		m.prompt.Focus()
		// The picker may have been open while a turn was running (it only
		// blocks ctrl+p, not the picker's Enter). Double-check aiThink so
		// we never swap providers under an in-flight agentic loop.
		if *m.aiThink {
			m.message_area.AppendMessage(">**INFO** Provider not switched: a turn is still running.", false)
			return m, nil
		}
		apiKey := msg.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("API_KEY")
		}
		p, err := providers.Select_provider(msg.Provider, msg.ModelID, apiKey)
		if err != nil {
			m.message_area.AppendMessage("Failed to switch provider: "+err.Error(), false)
			return m, nil
		}
		if m.model == nil {
			m.model = &tools.ProviderHolder{}
		}
		m.model.Set(p)
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
		warnCmd := toolSupportWarningCmd(msg.ModelID, msg.Provider)
		return m, tea.Batch(fetchModelInfoCmd(p, m.status.modelID), warnCmd)

	case providerselect.CancelledMsg:
		m.mode = modePrompt
		m.prompt.Focus()
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
			// Don't let the picker open (or the provider swap while it's
			// open) mid-agentic-loop: the in-flight turn re-reads the
			// provider when it loops back to the model, so switching here
			// would send the rest of the run to a different provider with
			// context shaped for another one.
			if *m.aiThink {
				m.message_area.AppendMessage(">**INFO** Wait for the current turn to finish before switching providers.", false)
				return m, nil
			}
			return m, m.openProviderSelect()
		}

		// A visible modal swallows all keys so the user has to dismiss it
		// (Enter confirms its focused button, Escape cancels).
		if m.modal.IsVisible() {
			var cmd tea.Cmd
			m.modal, cmd = m.modal.Update(msg)
			return m, cmd
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
			return m, tea.Batch(m.changesList.Toggle(), m.changesList.WatchCmd())
		}

		// Tab navigation works in every mode: ctrl+tab / tab = next,
		// ctrl+shift+tab / shift+tab = previous.
		switch msg_str {
		case "ctrl+tab", "tab":
			m.tabs.Next()
			return m, nil
		case "ctrl+shift+tab", "shift+tab":
			m.tabs.Prev()
			return m, nil
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
			case "ctrl+a":
				m.changesList.AcceptSelected()
				return m, nil
			case "ctrl+r":
				m.changesList.RejectSelected()
				return m, nil
			case "ctrl+f":
				m.changesList.AcceptAll()
				return m, nil
			case "ctrl+d":
				m.changesList.RejectAll()
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
			case "ctrl+a":
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
			// The tab strip renders taller than one line (its border adds top
			// and bottom rows), so let clicks anywhere on it switch tabs. The
			// prompt bar sits pinned just above the statusline at the bottom
			// of the screen, so it owns exactly promptbar.Height rows starting
			// at promptTop() - anything below that is the statusline, which
			// isn't clickable. Only on the "code" tab, since that's the only
			// screen either is rendered on. Once the message area needs its
			// own click handling (e.g. selecting a message), this is where
			// its Y range gets checked too.
			switch {
			case msg.Y < m.tabs.Height():
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
		if *m.aiThink {
			return m, cmd
		}
		return m, nil
	case changeslist.WatcherEventMsg:
		if m.changesList.Open() && !*m.aiThink {
			if m.changesList.HandleWatcherEvent(msg.FilePath) {
				m.changesList.RebuildRows()
			}
		}
		return m, m.changesList.WatchCmd()
	case aiResponseMsg:
		m.changesList.RefreshDiffs()
		if msg.err != nil {
			*m.aiThink = false
			m.stepCount = 0
			m.pendingTools = m.pendingTools[:0]
			m.prompt.Focus()
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
			if m.changesList.Open() {
				return m, m.changesList.WatchCmd()
			}
			return m, nil
		}

		m.status.sessionTokens += msg.usage.TotalTokens
		m.status.lastPromptTokens = msg.usage.PromptTokens

		// Some models (often small local ones) print a tool call as a JSON
		// blob in their text instead of emitting a native tool_calls block.
		// If the model reported no tool calls but its text parses into a
		// known tool call, treat it as one anyway.
		toolCalls := msg.toolCalls
		content := msg.content
		if len(toolCalls) == 0 && content != "" {
			if parsed, clean := providers.ParseTextToolCalls(content); len(parsed) > 0 {
				toolCalls = parsed
				content = clean
			}
		}

		if len(toolCalls) > 0 {
			// The assistant asked for tools: run them, feed the results
			// back into context, then call the model again. aiThink stays
			// true so the spinner keeps running and submitPrompt won't
			// launch a concurrent generation; the prompt bar itself stays
			// focused and usable.
			if content != "" {
				m.message_area.AppendMessage(content, false)
			}
			m.context = append(m.context, providers.Message{
				Role: "assistant", Content: content, ToolCalls: toolCalls,
			})

			// Max-steps guardrail (opencode's MAX_STEPS_PROMPT analog) so a
			// model stuck re-calling tools can't loop forever.
			m.stepCount++
			if m.stepCount > m.maxSteps {
				*m.aiThink = false
				m.prompt.Focus()
				m.message_area.AppendMessage(
					">**ERROR** Reached the maximum number of agent turns. If you want, ask me to continue with the remaining steps.",
					false,
				)
				m.stepCount = 0
				m.pendingTools = m.pendingTools[:0]
				return m, nil
			}

			// Render each requested call as a "running" row, then start
			// executing them one at a time. The prompt bar stays focused
			// and usable while tools run.
			*m.aiThink = true
			m.pendingTools = m.pendingTools[:0]
			for _, tc := range toolCalls {
				idx := m.message_area.AppendTool(tc.Tool_name, messagearea.SummarizeToolInput(tc.Input))
				m.pendingTools = append(m.pendingTools, pendingTool{call: tc, messageIdx: idx})
			}
			return m, runToolCmd(m.tool_dispatcher, m.pendingTools[0].call, 0)
		}

		// Final answer - no more tool calls to run.
		*m.aiThink = false
		if content != "" {
			m.message_area.AppendMessage(content, false)
			m.context = append(m.context, providers.Message{Role: "assistant", Content: content})
		}
		m.stepCount = 0
		m.pendingTools = m.pendingTools[:0]
		m.prompt.Focus()
		if m.changesList.Open() {
			return m, m.changesList.WatchCmd()
		}
		return m, nil
	case toolResultMsg:
		if msg.index < 0 || msg.index >= len(m.pendingTools) {
			return m, nil
		}
		pt := m.pendingTools[msg.index]

		if msg.result.IsError {
			m.message_area.UpdateToolMessage(pt.messageIdx, messagearea.ToolError, msg.result.Content)
		} else {
			m.message_area.UpdateToolMessage(pt.messageIdx, messagearea.ToolDone, msg.result.Content)
		}

		// Feed the tool's output back to the model, tied to its call id.
		m.context = append(m.context, providers.Message{
			Role: "tool", ToolCallID: pt.call.Tool_call_id, Content: toolOutputForContext(msg.result.Content),
		})
		m.changesList.RefreshDiffs()

		// Kick off the next tool in this batch, if any. Strictly sequential.
		next := msg.index + 1
		if next < len(m.pendingTools) {
			return m, runToolCmd(m.tool_dispatcher, m.pendingTools[next].call, next)
		}

		// Every tool in the batch ran - loop back to the model.
		m.pendingTools = m.pendingTools[:0]
		var p providers.Provider
		if m.model != nil {
			p = m.model.Get()
		}
		if p == nil {
			*m.aiThink = false
			m.stepCount = 0
			m.message_area.AppendMessage(">**ERROR** No provider selected press ctrl+p to pick one.", false)
			return m, nil
		}
		*m.aiThink = true
		return m, generateCmd(p, m.contextForModel())
	case modelInfoMsg:
		if msg.err == nil {
			m.status.contextWindow = msg.info.ContextWindow
		}
		return m, nil

	case popup.ShowMsg:
		var cmd tea.Cmd
		m.popUp, cmd = m.popUp.Update(msg)
		cmds = append(cmds, cmd)

	case dialog.ShowMsg:
		var cmd tea.Cmd
		m.modal, cmd = m.modal.Update(msg)
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
		if *m.aiThink {
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

	if m.modal.IsVisible() {
		content = m.modal.Overlay(content)
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
		// A missing .env isn't fatal: every provider can still be picked
		// interactively with ctrl+p (and local providers need no key at
		// all). Just warn and carry on with whatever the real environment
		// exports.
		fmt.Println("Warning: couldn't locate .env file:", err)
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

	root, err := os.Getwd()
	if err != nil {
		fmt.Println("Warning: couldn't determine project root:", err)
		root = ""
	}

	sys, err := inject_vars_in_sys_prompt(root)
	if err != nil {
		fmt.Println("Warning: couldn't load system prompt:", err)
	} else {
		providers.InitSystemPrompt(sys)
	}

	p := tea.NewProgram(initialModel(root, provider, modelName, providerName))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
		os.Exit(1)
	}
}
