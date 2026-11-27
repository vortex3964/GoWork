package gowork

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

// AppDataRoot is where GoWork keeps its own data (the executable's folder).
func AppDataRoot(projectRoot string) string {
	return appDataRoot(projectRoot)
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
