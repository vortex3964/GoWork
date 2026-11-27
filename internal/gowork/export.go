package gowork

import (
	tea "charm.land/bubbletea/v2"

	"GoWork/providers"
	"GoWork/tools"
	mcptool "GoWork/tools/MCPTool"
)

// Model is the application model exposed for tests in package test.
type Model = model

// UIMode is the UI mode type exposed for tests in package test.
type UIMode = uiMode

// UI modes exposed for tests in package test.
const (
	ModeIdle           UIMode = modeIdle
	ModePrompt         UIMode = modePrompt
	ModeProviderSelect UIMode = modeProviderSelect
	ModeChangeHandling UIMode = modeChangeHandling
	ModeRecall         UIMode = modeRecall
	ModeQuestionnaire  UIMode = modeQuestionnaire
	ModePermission     UIMode = modePermission
	ModeStats          UIMode = modeStats
	ModeSkills         UIMode = modeSkills
	ModeTools          UIMode = modeTools
)

// CLIOptions is the parsed command line exposed for tests in package test.
type CLIOptions = cliOptions

// ParseArgs parses the arguments after the program name.
func ParseArgs(args []string) (CLIOptions, error) {
	return parseArgs(args)
}

// IsTurnInterrupt reports whether a keypress should stop the in-flight turn.
func IsTurnInterrupt(key string, turnRunning bool) bool {
	return isTurnInterrupt(key, turnRunning)
}

// PlanModeActive reports whether the app is in read-only plan mode.
func (m model) PlanModeActive() bool {
	return m.planMode
}

// StatusLineText renders the current statusline so tests can assert on its
// mode indicators.
func (m model) StatusLineText() string {
	return renderStatusLine(m)
}

// AppDataRoot resolves the executable's folder, the fallback location used
// when the durable user config directory is unavailable.
func AppDataRoot(projectRoot string) string {
	return appDataRoot(projectRoot)
}

// BuiltInTools exposes the built-in tool set for integration tests. Pass nil
// to omit the MCP management tools.
func BuiltInTools(mcpRuntime mcptool.Runtime) []tools.AgentTool {
	return initTools(mcpRuntime)
}

// BuiltInToolDefs exposes the provider definitions for the built-in tool set.
func BuiltInToolDefs(mcpRuntime mcptool.Runtime) []providers.ToolDef {
	return toolDefsFor(initTools(mcpRuntime))
}

// SystemPromptPath is the repository-relative path of the system prompt.
const SystemPromptPath = sys_prompt_path

// EndTurn resets all agentic-loop state and returns the app to idle.
func (m *model) EndTurn() {
	m.endTurn()
}

// ResumeSession restores a previously saved session's messages and skills.
func (m *model) ResumeSession(id int64) {
	m.resumeSession(id)
}

// RunCompaction executes the compaction command synchronously and returns the
// message the update loop would receive. Exposed for tests in package test.
func RunCompaction(p providers.Provider, messages []providers.Message, window int, model string, todoState ...string) tea.Msg {
	return compactCmd(p, messages, window, model, todoState...)()
}

// StreamEnd builds the message a finished (or failed) streaming generation
// delivers to Update. Exposed for tests in package test.
func StreamEnd(result providers.GenerateResult, err error) tea.Msg {
	return streamEndMsg{result: result, err: err}
}

// RepairContext exposes the dangling-tool-call repair for tests.
func RepairContext(messages []providers.Message) ([]providers.Message, bool) {
	return repairContext(messages)
}

// SetMaxSteps overrides the agentic-loop step cap. Exposed for tests.
func (m *model) SetMaxSteps(n int) {
	m.maxSteps = n
}

// SetStepCount overrides the current turn's step counter. Exposed for tests.
func (m *model) SetStepCount(n int) {
	m.stepCount = n
}

// FlushSession runs the save-on-exit path. Exposed for tests.
func (m *model) FlushSession() {
	m.flushSession()
}

// SetContextWindow records the model's known context window. Exposed for
// tests in package test.
func (m *model) SetContextWindow(n int) {
	m.status.contextWindow = n
}

// PromptTokens returns the prompt-token count the statusline and the
// compaction trigger are currently using.
func (m model) PromptTokens() int {
	return m.status.lastPromptTokens
}

// SessionTokens returns the running session token total.
func (m model) SessionTokens() int {
	return m.status.sessionTokens
}

// ShouldCompact reports whether the current context is in the compaction
// zone.
func (m model) ShouldCompact() bool {
	return m.shouldCompact()
}
