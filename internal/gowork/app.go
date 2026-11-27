package gowork

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	changeslist "GoWork/Tui/Components/ChangesList"
	coffee "GoWork/Tui/Components/Coffee"
	dialog "GoWork/Tui/Components/Dialog"
	"GoWork/Tui/Components/MessageArea"
	permisionbox "GoWork/Tui/Components/PermisionBox"
	popup "GoWork/Tui/Components/PopUp"
	"GoWork/Tui/Components/Promptbar"
	providerselect "GoWork/Tui/Components/ProviderSelect"
	"GoWork/Tui/Components/Questionnaire"
	"GoWork/Tui/Components/SkillForm"
	"GoWork/Tui/Components/SkillsTab"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"
	toolstab "GoWork/Tui/Components/ToolsTab"

	db "GoWork/Db"
	"GoWork/internal/appdir"

	//import all the tool packages
	"GoWork/tools"
	"GoWork/tools/BashTool"
	"GoWork/tools/CreateFileTool"
	"GoWork/tools/DeleteFileTool"
	"GoWork/tools/EditFileTool"
	"GoWork/tools/FileSearchTool"
	"GoWork/tools/FilesInfoTool"
	"GoWork/tools/GrepSearchTool"
	mcptool "GoWork/tools/MCPTool"
	"GoWork/tools/MoveFileTool"
	"GoWork/tools/QuestionnaireTool"
	"GoWork/tools/ReadFileTool"
	"GoWork/tools/SkillsTool"
	"GoWork/tools/ToDoTool"
	"GoWork/tools/WebFetchTool"
	"GoWork/tools/WebSearchTool"
	"GoWork/tools/WriteFileTool"

	"GoWork/providers"
)

const topBarHeight = 1

const spinnerHeight = 1

// maxToolsPerTurn caps how many tool calls are executed from a single model
// reply. Beyond keeping the parallel batch sane, it stops a runaway model
// that keeps re-emitting the same calls from flooding the loop.
const maxToolsPerTurn = 10

type uiMode int

const (
	modeIdle uiMode = iota
	modePrompt
	modeProviderSelect
	modeChangeHandling
	modeRecall
	modeQuestionnaire
	modePermission
	modeStats
	modeSkills
	modeTools
)

type Mode struct {
	mode  uiMode
	label string
	color string
}

func GetModes() []Mode {
	return []Mode{
		modeIdle:           {mode: modeIdle, label: "IDLE", color: "#B3B1AD"},
		modePrompt:         {mode: modePrompt, label: "PROMPT", color: "#26ED79"},
		modeProviderSelect: {mode: modeProviderSelect, label: "PROVIDER", color: "#59C2FF"},
		modeChangeHandling: {mode: modeChangeHandling, label: "CHANGES", color: "#7C1EC9"},
		modeRecall:         {mode: modeRecall, label: "RECALL", color: "#95E6CB"},
		modeQuestionnaire:  {mode: modeQuestionnaire, label: "QUESTIONNAIRE", color: "#d10672"},
		modePermission:     {mode: modePermission, label: "PERMISSION", color: "#e2eb98"},
		modeStats:          {mode: modeStats, label: "STATS", color: "#FFB454"},
		modeSkills:         {mode: modeSkills, label: "SKILLS", color: "#F5E6B8"},
		modeTools:          {mode: modeTools, label: "TOOLS", color: "#95E6CB"},
	}
}

type model struct {
	Tabs        tabs.Model
	Stats       stats.Model
	Skills      skills.Model
	Tools       toolstab.Model
	Coffee      coffee.Model
	Prompt      promptbar.Model
	MessageArea messagearea.Model
	spinner     spinner.Model
	ChangesList changeslist.Model

	// root is the absolute project root; the skills manager and the
	// dispatcher resolve everything (skills discovery, writes) against it.
	root string

	// skillForm is the name/description modal that turns prompt-bar text
	// into a new skill.
	skillForm skillform.Model

	// which UI mode we're in (idle, prompt entry, provider select, ...)
	Mode uiMode

	// planMode is the session-wide read-only switch (ctrl+shift+p). It is
	// orthogonal to Mode: the prompt stays usable, but the dispatcher refuses
	// every mutating tool call until the switch is turned off again.
	planMode bool

	// size of the window
	winWidth  int
	winHeight int

	//ai related
	model   *tools.ProviderHolder
	Context []providers.Message
	AiThink *bool

	//tool call
	tool_dispatcher *tools.Dispatcher

	// agentic loop state: the tool calls we're still waiting on. Tools in the
	// current batch run in parallel; pendingResults (one slot per call) is nil
	// until that slot reports back, and the loop only returns to the model
	// once every slot is filled. There is no turn cap: the loop keeps going
	// across batches until the model answers without tool calls or the user
	// interrupts it.
	pendingTools   []pendingTool
	pendingResults []*tools.ToolResult

	// streaming state: while a generation is live we pump deltas off streamCh
	// into the last assistant message instead of waiting for the whole reply.
	streamCh    chan tea.Msg
	liveActive  bool
	liveContent string
	liveMsgIdx  int

	// interrupt state: cancel aborts whichever unit of work is in flight
	// (the streamed generation or the tool batch - they share one context
	// per turn), and stopRequested remembers that the user pressed ctrl+i so
	// the loop doesn't silently keep going when a provider ignores the
	// cancel. ctrl+i is delivered as tab (same byte, 0x09).
	cancel        context.CancelFunc
	stopRequested bool

	// provider/model picker (ctrl+p)
	providerSelect providerselect.Model

	// localServers owns the llama.cpp / LM Studio processes started from the
	// picker. It is killed on app exit.
	localServers *localServerManager

	// track the message were on for recall mode
	historyIdx int

	//status line data to be displayed
	status statusLine

	// infoGen guards the asynchronous model-info lookup: every provider/model
	// selection bumps it, and a response carrying an older generation (or a
	// different model id) is stale and must be ignored.
	infoGen uint64

	// our logo
	logoLines []string

	// error popup notification
	popUp popup.Model

	// Todo components
	modal           dialog.Model
	todoPanelOpen   bool
	todoPanelWidth  int
	panelDragActive bool
	panelDragStartX int
	panelDragWidth  int

	// questionnaire shown in place of the prompt bar while the turn waits
	questionnaire     questionnaire.Model
	questionnaireSlot int

	// permission gate: when a requested tool needs approval the batch holds
	// still and the permisionbox opens over everything. permissionSlot is the
	// pendingTools index being asked about (-1 when none).
	// permission gate: the batch holds still while a KindNotAllowed tool waits
	// for the user's decision. permissionSlot is the pendingTools index being
	// asked about (-1 when none).
	permissionBox     permisionbox.Model
	permissionSlot    int
	permissionDecided []bool
	AutoAcceptTools   bool
	// pendingNotes carries the user's typed reason per tool call so it
	// reaches the AI with that tool's result either way.
	pendingNotes []string
	// batchCtx keeps the tool batch's context so it can launch once the
	// user resolves a pending permission request.
	batchCtx context.Context

	// Db is the local usage/session store. Nil when the database couldn't be
	// opened - the app keeps working, it just records nothing.
	Db *db.Store

	// LSP is the injected language-server manager used by /lsp. The app wires
	// it to the real LSP configuration at startup; tests can substitute a
	// fake, and a nil value falls back to the no-op manager.
	LSP LSPManager

	// toolRuntime owns the live MCP clients and LSP bridge; baseTools is the
	// built-in tool set the registry is rebuilt from when a server is added,
	// removed, or toggled. watchList is kept so the rebuilt dispatcher uses
	// the same file watcher.
	toolRuntime *toolRuntime
	baseTools   []tools.AgentTool
	watchList   *changeslist.WatchList

	promptHeight int
}

func get_supported_providers() []string {
	return []string{"google", "anthropic", "groq", "openAi", "opencode-go", "local"}
}

func get_supported_providers_local() []string {
	return []string{"ollama", "llamaCpp", "lmStudio"}
}

// initTools constructs every built-in tool. The install/load tools are bound
// to the app's single MCP runtime; passing nil leaves the MCP management tools
// out instead of creating a second client pool.
func initTools(mcpRuntime mcptool.Runtime) []tools.AgentTool {
	base := []tools.AgentTool{
		bashtool.New(),
		createfiletool.New(),
		deletefiletool.New(),
		editfiletool.New(),
		filesearchtool.New(),
		fileinfo.New(),
		grepsearchtool.New(),
		movefiletool.New(),
		questionnairetool.New(),
		readfiletool.New(),
		skillstool.New(),
		todotool.New(),
		webfetchtool.New(),
		websearchtool.New(),
		writefiletool.New(),
	}
	return append(base, installTools(mcpRuntime)...)
}

func initialModel(root string, provider providers.Provider, modelID string, providerName string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	think := false

	// The skills manager is a singleton the skill tool and the skills tab
	// both read, so wire it before the tools compute their descriptions.
	// Skills live in the durable per-user config directory, NOT next to the
	// executable or in the project the user is working in. setupSkillManager
	// migrates the old layout and seeds the bundled bootstrap skill once.
	skillstool.SetManager(setupSkillManager())

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

	// Start the runtime extensions before computing the tool registry so MCP
	// tools and the LSP bridge are available to the model from the first
	// turn. Configuration lives in the durable user config directory, not
	// next to the executable. Unavailable servers are recorded for the Tools
	// tab, not fatal.
	appData, err := appdir.ConfigDir()
	if err != nil {
		log.Printf("user config directory unavailable, using the executable directory: %v", err)
		appData = appDataRoot(root)
	}
	rt := newToolRuntime(appData, root)
	rt.Load()

	baseTools := initTools(rt)
	t := make([]tools.AgentTool, 0, len(baseTools)+16)
	t = append(t, baseTools...)
	t = append(t, rt.Tools()...)

	providers.InitToolsDef(toolDefsFor(t))

	dispatcher, err := tools.InitDispacher(root, wl, holder.Get, t...)
	if err != nil {
		log.Printf("failed to init dispatcher: %v", err)
	}
	// The runtime registers MCP bridges as servers connect; point it at the
	// live dispatcher so an install made during a turn is immediately usable.
	rt.SetDispatcher(dispatcher)

	p := promptbar.New()
	p.SetCommands(slashCommands())
	p.Focus()

	changesList := changeslist.New(wl)
	// Show paths relative to the project root instead of absolute.
	changesList.SetRoot(root)

	// Open the local usage/session database and start this app run's session
	// record (one app run = one session).
	var store *db.Store
	s, err := db.Open(filepath.Join(root, db.DefaultPath))
	if err != nil {
		log.Printf("failed to open database: %v; usage stats disabled", err)
	} else {
		store = s
		if err := store.StartSession(modelID, providerName); err != nil {
			log.Printf("failed to start session: %v", err)
		}
	}

	m := model{
		Tabs:              tabs.New("code", "skills", "tools", "stats"),
		Prompt:            p,
		MessageArea:       messagearea.New(),
		Mode:              modePrompt,
		spinner:           sp,
		Context:           []providers.Message{},
		model:             holder,
		AiThink:           &think,
		status:            newStatusLine(root, providerName, modelID),
		logoLines:         loadLogo(),
		popUp:             popup.New(),
		modal:             dialog.New(),
		ChangesList:       changesList,
		tool_dispatcher:   dispatcher,
		questionnaire:     questionnaire.New(nil),
		questionnaireSlot: -1,
		permissionBox:     permisionbox.New("", "", "", permisionbox.Impact{}),
		permissionSlot:    -1,
		Db:                store,
		LSP:               rt,
		root:              root,
		promptHeight:      p.Height(),
		Skills:            skills.New(),
		Stats:             stats.New(),
		Tools:             toolstab.New(),
		Coffee:            coffee.New(),
		skillForm:         skillform.New(),
		localServers:      newLocalServerManager(),
		toolRuntime:       rt,
		baseTools:         baseTools,
		watchList:         wl,
	}
	// Prime the Tools tab with the startup snapshot so it is accurate even
	// before the first tab entry (and for /lsp listing errors).
	m.refreshTools()
	return m
}

// setPlanMode switches read-only plan mode on or off and keeps the dispatcher
// in sync. Plan mode is session-wide and independent of the active tab or
// prompt focus: read tools keep working, mutating tools return a refusal the
// model can read. The message area gets a short explanation on every change so
// the state is discoverable in-app.
func (m *model) setPlanMode(on bool) {
	if m.planMode == on {
		return
	}
	m.planMode = on
	if m.tool_dispatcher != nil {
		m.tool_dispatcher.SetPlanMode(on)
	}
	if on {
		m.MessageArea.AppendMessage(
			">**PLAN MODE ON** Read-only: file writes, edits, moves, deletes, bash and web_fetch are blocked. Read-only tools still work. Press ctrl+shift+p to leave plan mode (or esc when the prompt is not focused).",
			false,
		)
		return
	}
	m.MessageArea.AppendMessage(
		">**INFO** Plan mode off - tools may modify the workspace again.",
		false,
	)
}

// togglePlanMode flips read-only plan mode.
func (m *model) togglePlanMode() {
	m.setPlanMode(!m.planMode)
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.model != nil {
		if p := m.model.Get(); p != nil {
			cmds = append(cmds, fetchModelInfoCmd(p, m.status.modelID, m.infoGen))
		}
	}
	cmds = append(cmds, m.ChangesList.WatchCmd())
	if m.status.providerName != "" && m.status.modelID != "" {
		cmds = append(cmds, toolSupportWarningCmd(m.status.modelID, m.status.providerName))
	}
	return tea.Batch(cmds...)
}

func envOr(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// abs returns the absolute value of n. Go's math.Abs only works on floats,
// so ints get this tiny helper.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
