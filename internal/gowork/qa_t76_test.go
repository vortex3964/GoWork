package gowork

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	db "GoWork/Db"
	messagearea "GoWork/Tui/Components/MessageArea"
	permisionbox "GoWork/Tui/Components/PermisionBox"
	promptbar "GoWork/Tui/Components/Promptbar"
	questionnaire "GoWork/Tui/Components/Questionnaire"
	skills "GoWork/Tui/Components/SkillsTab"
	"GoWork/Tui/Components/Stats"
	tabs "GoWork/Tui/Components/Tabs"

	"GoWork/providers"
	"GoWork/tools"
	questionnairetool "GoWork/tools/QuestionnaireTool"
)

func newQA76Model(t *testing.T) model {
	t.Helper()
	think := false
	m := model{
		Tabs:        tabs.New("code", "skills", "tools", "stats"),
		Prompt:      promptbar.New(),
		MessageArea: messagearea.New(),
		Skills:      skills.New(),
		Stats:       stats.New(),
		Mode:        modePrompt,
		winWidth:    120,
		winHeight:   40,
		AiThink:     &think,
		root:        t.TempDir(),
	}
	m.Prompt.SetWidth(120)
	m.Prompt.SetCommands(slashCommands())
	m.Prompt.Focus()
	m.Stats.SetSize(120, 40)
	m.applyPanelSize()
	return m
}

// --- M3: dialog exits respect the active tab --------------------------------

func TestResolvePermissionRestoresStatsTab(t *testing.T) {
	m := newQA76Model(t)
	m.Tabs.ActiveIdx = 3
	m.syncModeToTab()
	if m.Mode != modeStats {
		t.Fatalf("setup mode = %v, want stats", m.Mode)
	}

	m.pendingTools = []pendingTool{{call: providers.ToolCall{Tool_call_id: "c1", Tool_name: "bash"}}}
	m.pendingResults = make([]*tools.ToolResult, 1)
	m.pendingNotes = make([]string, 1)
	m.permissionDecided = make([]bool, 1)
	m.permissionSlot = 0

	m.resolvePermission(permisionbox.DecisionMsg{Type: permisionbox.ActionReject, Reason: "no"})

	if m.Mode != modeStats {
		t.Fatalf("mode after permission = %v, want stats", m.Mode)
	}
	if m.Prompt.Focused() {
		t.Fatal("permission exit focused the hidden stats-tab prompt")
	}
}

func TestAnswerQuestionnaireRestoresSkillsTab(t *testing.T) {
	m := newQA76Model(t)
	m.Tabs.ActiveIdx = 1
	m.syncModeToTab()
	if m.Mode != modeSkills {
		t.Fatalf("setup mode = %v, want skills", m.Mode)
	}

	m.pendingTools = []pendingTool{{call: providers.ToolCall{Tool_call_id: "c1", Tool_name: questionnairetool.ToolName}}}
	m.pendingResults = make([]*tools.ToolResult, 1)
	m.questionnaireSlot = 0

	m.answerQuestionnaire("yes")
	if m.Mode != modeSkills {
		t.Fatalf("mode after questionnaire = %v, want skills", m.Mode)
	}
	if m.Prompt.Focused() {
		t.Fatal("questionnaire exit focused the skills-tab prompt")
	}
}

func TestRestoreModeKeepsQuestionnaireVisible(t *testing.T) {
	m := newQA76Model(t)
	m.Mode = modeQuestionnaire
	m.Tabs.ActiveIdx = 3
	if cmd := m.restoreModeForActiveTab(); cmd != nil {
		t.Fatal("restore while a questionnaire is open returned a prompt command")
	}
	if m.Mode != modeQuestionnaire {
		t.Fatalf("mode = %v, want questionnaire", m.Mode)
	}
}

func TestQuestionnaireActivationShowsCodeTab(t *testing.T) {
	questionnaire.SetActive([]questionnaire.Question{{Question: "pick one", Answers: []string{"a", "b"}}})
	t.Cleanup(questionnaire.Clear)

	m := newQA76Model(t)
	m.Tabs.ActiveIdx = 3
	m.syncModeToTab()
	m.pendingTools = []pendingTool{{call: providers.ToolCall{Tool_call_id: "c1", Tool_name: questionnairetool.ToolName}}}
	m.pendingResults = make([]*tools.ToolResult, 1)
	m.questionnaireSlot = -1

	mdl, _ := m.Update(toolResultMsg{index: 0, result: tools.Ok("{}")})
	m = mdl.(model)
	if m.Mode != modeQuestionnaire {
		t.Fatalf("mode = %v, want questionnaire", m.Mode)
	}
	if got := m.Tabs.Active().Name; got != "code" {
		t.Fatalf("active tab = %q, want code so the questionnaire is visible", got)
	}
}

// --- M4: palette tab vs running-turn interrupt ------------------------------

func TestPaletteTabCompletesInsteadOfInterrupting(t *testing.T) {
	m := newQA76Model(t)
	m.Prompt.Focus()
	m.Prompt.SetValue("/")
	cancelled := false
	m.cancel = func() { cancelled = true }
	*m.AiThink = true

	mdl, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = mdl.(model)

	if cancelled {
		t.Fatal("tab completed the palette but also cancelled the running turn")
	}
	if m.stopRequested {
		t.Fatal("palette completion set stopRequested")
	}
	if got := m.Prompt.Value(); got != "/help " {
		t.Fatalf("prompt after palette completion = %q, want %q", got, "/help ")
	}
	if m.Prompt.PaletteVisible() {
		t.Fatal("palette stayed open after accepting a completion")
	}
}

func TestCtrlIStillInterruptsWithPaletteOpen(t *testing.T) {
	m := newQA76Model(t)
	m.Prompt.Focus()
	m.Prompt.SetValue("/")
	cancelled := false
	m.cancel = func() { cancelled = true }
	*m.AiThink = true

	mdl, _ := m.Update(ctrlKey('i'))
	m = mdl.(model)
	if !cancelled {
		t.Fatal("ctrl+i did not interrupt the running turn while the palette was open")
	}
	if !m.stopRequested {
		t.Fatal("ctrl+i interruption did not set stopRequested")
	}
}

func TestTabStillInterruptsWithoutPalette(t *testing.T) {
	m := newQA76Model(t)
	cancelled := false
	m.cancel = func() { cancelled = true }
	*m.AiThink = true

	mdl, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	_ = mdl.(model)
	if !cancelled {
		t.Fatal("tab did not interrupt a running turn when the palette was closed")
	}
}

// --- M5: stats refresh and statusline clamp ---------------------------------

func TestStatsRefreshAfterGenerationWhileTabVisible(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.StartSession("qa-model", "qa-provider"); err != nil {
		t.Fatalf("start session: %v", err)
	}

	m := newQA76Model(t)
	m.Db = store
	m.Tabs.ActiveIdx = 3
	m.syncModeToTab()
	m.status.modelID = "qa-model"
	m.status.providerName = "qa-provider"

	mdl, _ := m.Update(streamEndMsg{result: providers.GenerateResult{
		Content: "generated answer",
		Usage:   providers.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}})
	m = mdl.(model)

	if m.status.sessionTokens != 10 {
		t.Fatalf("session tokens = %d, want 10", m.status.sessionTokens)
	}
	if view := m.Stats.View(); !strings.Contains(view, "qa-model") {
		t.Fatalf("stats tab was not refreshed after the generation:\n%s", view)
	}
}

func TestStatusLineClampsToTerminalWidth(t *testing.T) {
	m := model{
		winWidth: 24,
		status: statusLine{
			modelID:          strings.Repeat("model", 8),
			providerName:     strings.Repeat("provider", 8),
			projectRoot:      "project",
			contextWindow:    1000,
			lastPromptTokens: 999,
			sessionTokens:    123456,
		},
	}

	out := renderStatusLine(m)
	if w := lipgloss.Width(out); w > m.winWidth {
		t.Fatalf("statusline width = %d, want <= %d:\n%s", w, m.winWidth, out)
	}
}

// --- M6: stale model-info responses -----------------------------------------

type qaInfoProvider struct {
	info      providers.ModelInfo
	err       error
	lastModel string
}

func (p *qaInfoProvider) Generate(context.Context, []providers.Message) (providers.GenerateResult, error) {
	return providers.GenerateResult{}, nil
}

func (p *qaInfoProvider) GenerateStream(context.Context, []providers.Message, providers.StreamFunc) (providers.GenerateResult, error) {
	return providers.GenerateResult{}, nil
}

func (p *qaInfoProvider) Info(_ context.Context, model string) (providers.ModelInfo, error) {
	p.lastModel = model
	return p.info, p.err
}

func (p *qaInfoProvider) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}

func TestModelInfoCarriesGenerationAndModel(t *testing.T) {
	p := &qaInfoProvider{info: providers.ModelInfo{ContextWindow: 42}}
	msg, ok := fetchModelInfoCmd(p, "qa-model", 7)().(modelInfoMsg)
	if !ok {
		t.Fatal("model info command did not produce a modelInfoMsg")
	}
	if msg.gen != 7 || msg.model != "qa-model" {
		t.Fatalf("model info message = gen %d model %q, want gen 7 model qa-model", msg.gen, msg.model)
	}
	if p.lastModel != "qa-model" {
		t.Fatalf("provider info called for %q, want qa-model", p.lastModel)
	}
	if msg.info.ContextWindow != 42 {
		t.Fatalf("context window = %d, want 42", msg.info.ContextWindow)
	}
}

func TestStaleModelInfoIsIgnored(t *testing.T) {
	m := newQA76Model(t)
	m.status.modelID = "new-model"
	m.infoGen = 5

	mdl, _ := m.Update(modelInfoMsg{gen: 4, model: "new-model", info: providers.ModelInfo{ContextWindow: 111}})
	m = mdl.(model)
	if m.status.contextWindow != 0 {
		t.Fatalf("stale generation was applied: context window = %d", m.status.contextWindow)
	}

	mdl, _ = m.Update(modelInfoMsg{gen: 5, model: "old-model", info: providers.ModelInfo{ContextWindow: 222}})
	m = mdl.(model)
	if m.status.contextWindow != 0 {
		t.Fatalf("stale model response was applied: context window = %d", m.status.contextWindow)
	}

	mdl, _ = m.Update(modelInfoMsg{gen: 5, model: "new-model", info: providers.ModelInfo{ContextWindow: 333}})
	m = mdl.(model)
	if m.status.contextWindow != 333 {
		t.Fatalf("current response was ignored: context window = %d, want 333", m.status.contextWindow)
	}
}

// --- M7: /context stays inside the project ----------------------------------

func TestContextCommandRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(inside, []byte("inside content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}

	m := newSlashTestModel(t)
	m.root = root

	for _, arg := range []string{outside, "../secret.txt", "leak.txt"} {
		m.Context = nil
		m.runSlashCommand("/context " + arg)
		if len(m.Context) != 0 {
			t.Fatalf("%q escaped the project root: context = %+v", arg, m.Context)
		}
		if view := m.MessageArea.View(); !strings.Contains(view, "Could not read") {
			t.Fatalf("%q did not report an error:\n%s", arg, view)
		}
	}

	m.Context = nil
	m.runSlashCommand("/context notes.txt")
	if len(m.Context) != 1 || !strings.Contains(m.Context[0].Content, "inside content") {
		t.Fatalf("in-project file was not added: %+v", m.Context)
	}
}

// --- M6: /save is a non-sealing flush --------------------------------------

func TestSaveFlushesWithoutSealingOrDuplicating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "save.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.StartSession("qa-model", "qa-provider"); err != nil {
		t.Fatalf("start session: %v", err)
	}

	m := newQA76Model(t)
	m.Db = store
	m.Context = []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	m.status.sessionTokens = 100

	m.runSlashCommand("/save")
	if m.status.sessionTokens != 100 {
		t.Fatalf("/save reset session tokens to %d, want 100", m.status.sessionTokens)
	}

	ls, err := store.LoadSession(1)
	if err != nil {
		t.Fatalf("load session after /save: %v", err)
	}
	if ls == nil {
		t.Fatal("/save did not persist the session")
	}
	if !ls.EndedAt.IsZero() {
		t.Fatalf("/save sealed the session at %v", ls.EndedAt)
	}
	if ls.TotalTokens != 100 || len(ls.Messages) != 2 {
		t.Fatalf("after /save: tokens=%d messages=%d, want 100/2", ls.TotalTokens, len(ls.Messages))
	}

	// More work after the checkpoint must land in the same session, and the
	// exit save must not append a second copy of the earlier history.
	m.Context = append(m.Context, providers.Message{Role: "user", Content: "again"})
	m.status.sessionTokens = 150
	m.FlushSession()

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()

	ls, err = reopened.LoadSession(1)
	if err != nil {
		t.Fatalf("load finalized session: %v", err)
	}
	if ls == nil {
		t.Fatal("final session was not saved")
	}
	if ls.EndedAt.IsZero() {
		t.Fatal("final save did not seal the session")
	}
	if ls.TotalTokens != 150 {
		t.Fatalf("final total tokens = %d, want 150", ls.TotalTokens)
	}
	if len(ls.Messages) != 3 {
		t.Fatalf("final message count = %d, want 3 (history duplicated?): %+v", len(ls.Messages), ls.Messages)
	}
	if next, _ := reopened.LoadSession(2); next != nil {
		t.Fatalf("/save started an implicit second session: %+v", next)
	}
}

func TestNewStillStartsExplicitSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.StartSession("qa-model", "qa-provider"); err != nil {
		t.Fatalf("start session: %v", err)
	}

	m := newQA76Model(t)
	m.Db = store
	m.Context = []providers.Message{{Role: "user", Content: "first"}}
	m.status.sessionTokens = 20

	m.runSlashCommand("/new")
	if len(m.Context) != 0 {
		t.Fatalf("/new did not clear context: %+v", m.Context)
	}
	if m.status.sessionTokens != 0 {
		t.Fatalf("/new left session tokens at %d", m.status.sessionTokens)
	}
	m.FlushSession()

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()

	if first, _ := reopened.LoadSession(1); first == nil || len(first.Messages) != 1 {
		t.Fatalf("first session not sealed with its history: %+v", first)
	}
	if second, _ := reopened.LoadSession(2); second == nil {
		t.Fatal("/new did not start a new session")
	}
}
