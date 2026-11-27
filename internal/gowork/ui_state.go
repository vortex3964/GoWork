package gowork

import (
	"log"
	"strings"

	tea "charm.land/bubbletea/v2"

	"GoWork/Tui/Components/Questionnaire"
	"GoWork/Tui/Components/SkillsTab"
	toolstab "GoWork/Tui/Components/ToolsTab"

	"GoWork/providers"
	"GoWork/tools/SkillsTool"
)

func (m model) panelWidth() int {
	w := m.todoPanelWidth
	if w <= 0 {
		w = int(float64(m.winWidth) * 0.4)
	}
	if w < 20 {
		w = 20
	}
	if w > m.winWidth/2 {
		w = m.winWidth / 2
	}
	return w
}

func (m model) msgAreaWidth() int {
	if !m.todoPanelOpen {
		return m.winWidth
	}
	w := m.winWidth - m.panelWidth()
	if w < 0 {
		w = 0
	}
	return w
}

func (m model) messageBandHeight() int {
	h := m.promptTop() - m.panelTop()
	if h < 0 {
		return 0
	}
	return h
}

// panelTop is the first row of the message band on screen.
func (m model) panelTop() int {
	return m.Tabs.Height() + 1
}

// arrowRow is the vertical center of the message band, where the toggle
// arrow is drawn.
func (m model) arrowRow() int {
	return m.panelTop() + m.messageBandHeight()/2
}

func (m model) arrowCol() int {
	if m.todoPanelOpen {
		col := m.msgAreaWidth()
		if col > m.winWidth-1 {
			col = m.winWidth - 1
		}
		return col
	}
	if m.winWidth <= 0 {
		return 0
	}
	return m.winWidth - 1
}

// bottomBarHeight is the height of whatever sits between the band and the
// statusline: the prompt bar normally (its height grows up to 6 rows while
// typing), the taller questionnaire while one shows
func (m model) bottomBarHeight() int {
	if m.Mode == modeQuestionnaire {
		return questionnaire.Height
	}
	return m.Prompt.Height()
}

func (m *model) applyPanelSize() {
	msgAreaHeight := m.winHeight - topBarHeight - spinnerHeight - statusLineHeight - m.bottomBarHeight()
	if msgAreaHeight < 0 {
		msgAreaHeight = 0
	}
	m.MessageArea.SetSize(m.msgAreaWidth(), msgAreaHeight)
	m.Skills.SetSize(m.msgAreaWidth(), msgAreaHeight)
	m.Tools.SetSize(m.msgAreaWidth(), msgAreaHeight)
}

// syncPromptHeight re-layouts the band when the prompt bar's height changed
// outside the normal Update path (Reset/SetValue/InsertNewline touch the bar
// directly), so a shrunken bar frees band space and a grown one never
// overlaps the message area.
func (m *model) syncPromptHeight() {
	if h := m.Prompt.Height(); h != m.promptHeight {
		m.promptHeight = h
		m.applyPanelSize()
	}
}

func (m *model) toggleTodoPanel() {
	m.todoPanelOpen = !m.todoPanelOpen
	m.applyPanelSize()
}

// syncModeToTab keeps the active mode in step with the active tab: the stats
// tab runs in its own stats mode (statusline pinned, prompt bar hidden), the
// skills tab pins both bars (the pane shares the message-area band), any
// other tab returns to the normal prompt layout.
func (m *model) syncModeToTab() {
	if m.Tabs.Active().Name != "tools" {
		// Leaving Tools never leaves a detached add form waiting to
		// reappear when the user comes back.
		m.Tools.CloseForm()
	}
	if m.Tabs.Active().Name == "stats" {
		m.Mode = modeStats
		m.Prompt.Blur()
		m.refreshStats()
	} else if m.Tabs.Active().Name == "skills" {
		m.Mode = modeSkills
		m.Prompt.Blur()
		m.refreshSkills()
	} else if m.Tabs.Active().Name == "tools" {
		m.Mode = modeTools
		m.Prompt.Blur()
		m.refreshTools()
	} else if m.Mode == modeStats || m.Mode == modeSkills || m.Mode == modeTools {
		m.Mode = modePrompt
		m.Prompt.Focus()
	}
}

// toggleToolsTab jumps to the Tools tab, or back to the code tab when Tools is
// already active. Used by ctrl+t from every layout.
func (m *model) toggleToolsTab() {
	if m.Tabs.Active().Name == "tools" {
		m.Tabs.ActiveIdx = 0
		m.syncModeToTab()
		return
	}
	for i, tab := range m.Tabs.Tabs {
		if tab.Name == "tools" {
			m.Tabs.ActiveIdx = i
			m.syncModeToTab()
			return
		}
	}
}

// refreshStats reloads the dashboard data. Called when entering the stats tab
// and after every recorded generation (only while the tab is live to keep it
// cheap).
func (m *model) refreshStats() {
	if m.Db == nil {
		return
	}
	rows, err := m.Db.Stats()
	if err != nil {
		log.Printf("failed to load stats: %v", err)
		return
	}
	daily, err := m.Db.DailyStats()
	if err != nil {
		log.Printf("failed to load daily stats: %v", err)
		daily = nil
	}
	m.Stats.SetData(rows, daily)
}

// refreshSkills rebuilds the skills tab's discovery snapshot: every skill
// currently on disk (from the manager's registry), with each entry's Loaded
// flag coming from what the session has loaded so far.
func (m *model) refreshSkills() {
	mgr := skillstool.GetManager()
	if mgr == nil {
		m.Skills.SetData(nil)
		return
	}
	discovered := mgr.Discover()
	entries := make([]skills.Entry, 0, len(discovered))
	for _, e := range discovered {
		if e.Name == "" || strings.HasPrefix(e.Name, ".") {
			continue
		}
		entries = append(entries, skills.Entry{
			Name:        e.Name,
			Description: e.Description,
			Path:        e.Path,
			Loaded:      mgr.IsLoaded(e.Name),
		})
	}
	m.Skills.SetData(entries)
}

// refreshTools rebuilds the Tools tab snapshot: discovered/loaded skills plus
// the runtime's live MCP and LSP state. Called on tab entry and after every
// tool-tab change.
func (m *model) refreshTools() {
	var data toolstab.Data
	if mgr := skillstool.GetManager(); mgr != nil {
		for _, e := range mgr.Discover() {
			if e.Name == "" || strings.HasPrefix(e.Name, ".") {
				continue
			}
			data.Skills = append(data.Skills, toolstab.Skill{
				Name:        e.Name,
				Description: e.Description,
				Path:        e.Path,
				Loaded:      mgr.IsLoaded(e.Name),
			})
		}
	}
	if m.toolRuntime != nil {
		data.MCP = m.toolRuntime.MCPServers()
		data.LSP = m.toolRuntime.LSPServers()
		data.MCPConfigPath, data.LSPConfigPath = m.toolRuntime.ConfigPaths()
		data.MCPError, data.LSPError = m.toolRuntime.Errors()
	}
	m.Tools.SetData(data)
}

// syncSkillToolDef re-registers the skill tool's definition with the
// providers registry so the agent sees the current skill set every turn.
func (m *model) syncSkillToolDef() {
	providers.UpdateToolDef(skillstool.ToolName, skillstool.ToolDef())
}

// syncSkillsState runs everything that needs to happen after the session's
// loaded set changes (load/unload/create): refresh the tab, re-register the
// tool definitions for the next agent turn, and persist the loaded names to
// the session record.
func (m *model) syncSkillsState() {
	m.refreshSkills()
	m.refreshTools()
	m.syncSkillToolDef()
	if m.Db != nil {
		mgr := skillstool.GetManager()
		if mgr != nil {
			m.Db.SetSessionSkills(mgr.LoadedNames())
		}
	}
}

func (m model) promptTop() int {
	return m.winHeight - statusLineHeight - m.bottomBarHeight()
}

// restoreModeForActiveTab leaves an overlay or auxiliary mode and returns to
// the layout of the active tab. The provider picker overlays any tab and the
// changes list can outlive a tab switch, so forcing modePrompt unconditionally
// would leave stats/skills showing a PROMPT statusline while their pane still
// owns the keys.
func (m *model) restoreModeForActiveTab() tea.Cmd {
	m.Mode = modePrompt
	m.syncModeToTab()
	if m.Mode == modePrompt {
		return m.Prompt.Focus()
	}
	return nil
}

// canOpenChangesList reports whether ctrl+l may enter changes mode. The
// changes list only renders on the code tab, and an open questionnaire must
// keep every key so its batch slot can still be answered.
func canOpenChangesList(mode uiMode, tabName string) bool {
	return mode != modeQuestionnaire && tabName == "code"
}
