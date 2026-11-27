package gowork

import (
	"fmt"
	"log"
	"strings"

	tea "charm.land/bubbletea/v2"

	toolstab "GoWork/Tui/Components/ToolsTab"
	"GoWork/providers"
	"GoWork/tools"
	"GoWork/tools/SkillsTool"
)

// handleToolsMessage applies one Tools-tab intent. Every branch refreshes the
// tab snapshot and rebuilds the dispatcher/provider registry when the tool set
// changed, so the model sees added/removed MCP and LSP tools on its next turn.
//
// Mutations are refused while a turn is running: the dispatcher and provider
// registry are read by streaming/tool goroutines, and swapping them mid-batch
// would race with in-flight calls.
func (m model) handleToolsMessage(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case toolstab.ToggleMsg:
		m.handleToolToggle(msg)
		return m, nil, true
	case toolstab.RemoveMsg:
		m.handleToolRemove(msg)
		return m, nil, true
	case toolstab.AddMCPMsg:
		m.handleAddMCP(msg)
		return m, nil, true
	case toolstab.AddLSPMsg:
		m.handleAddLSP(msg)
		return m, nil, true
	case toolstab.CreateSkillMsg:
		return m, m.openSkillCreator(), true
	}
	return m, nil, false
}

func (m *model) toolsBusy() bool {
	if !m.commandBusy() {
		return false
	}
	m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before changing tools.", false)
	return true
}

func (m *model) handleToolToggle(msg toolstab.ToggleMsg) {
	if m.toolsBusy() {
		return
	}
	switch msg.Section {
	case toolstab.SectionSkills:
		mgr := skillstool.GetManager()
		if mgr == nil {
			return
		}
		if msg.Enabled {
			if !mgr.Load(msg.Name) {
				m.MessageArea.AppendMessage(">**ERROR** No such skill on disk: "+msg.Name, false)
				return
			}
		} else {
			mgr.Unload(msg.Name)
		}
		m.syncSkillsState()
	case toolstab.SectionMCP:
		if m.toolRuntime == nil {
			m.MessageArea.AppendMessage(">**ERROR** MCP support is unavailable.", false)
			return
		}
		if err := m.toolRuntime.SetMCPEnabled(msg.Name, msg.Enabled); err != nil {
			m.MessageArea.AppendMessage(">**ERROR** MCP server "+msg.Name+": "+err.Error(), false)
		}
		m.afterToolChange()
	case toolstab.SectionLSP:
		if m.toolRuntime == nil {
			m.MessageArea.AppendMessage(">**ERROR** LSP support is unavailable.", false)
			return
		}
		if err := m.toolRuntime.SetLSPEnabled(msg.Name, msg.Enabled); err != nil {
			m.MessageArea.AppendMessage(">**ERROR** LSP server "+msg.Name+": "+err.Error(), false)
		}
		m.afterToolChange()
	}
}

func (m *model) handleToolRemove(msg toolstab.RemoveMsg) {
	if m.toolsBusy() {
		return
	}
	if m.toolRuntime == nil {
		m.MessageArea.AppendMessage(">**ERROR** Tool configuration is unavailable.", false)
		return
	}
	var err error
	switch msg.Section {
	case toolstab.SectionMCP:
		err = m.toolRuntime.RemoveMCP(msg.Name)
	case toolstab.SectionLSP:
		err = m.toolRuntime.RemoveLSP(msg.Name)
	default:
		return
	}
	if err != nil {
		m.MessageArea.AppendMessage(">**ERROR** Could not remove "+msg.Name+": "+err.Error(), false)
	} else {
		m.MessageArea.AppendMessage(">**INFO** Removed "+msg.Name+" from the tool configuration.", false)
	}
	m.afterToolChange()
}

func (m *model) handleAddMCP(msg toolstab.AddMCPMsg) {
	if m.toolsBusy() {
		return
	}
	if m.toolRuntime == nil {
		m.MessageArea.AppendMessage(">**ERROR** MCP support is unavailable.", false)
		return
	}
	if err := m.toolRuntime.AddMCP(msg); err != nil {
		m.MessageArea.AppendMessage(">**ERROR** MCP server "+msg.Name+": "+err.Error(), false)
	} else {
		m.MessageArea.AppendMessage(fmt.Sprintf(">**INFO** Added MCP server %q.", msg.Name), false)
	}
	m.afterToolChange()
}

func (m *model) handleAddLSP(msg toolstab.AddLSPMsg) {
	if m.toolsBusy() {
		return
	}
	if m.toolRuntime == nil {
		m.MessageArea.AppendMessage(">**ERROR** LSP support is unavailable.", false)
		return
	}
	if err := m.toolRuntime.AddLSP(msg); err != nil {
		m.MessageArea.AppendMessage(">**ERROR** LSP server "+msg.Name+": "+err.Error(), false)
	} else {
		m.MessageArea.AppendMessage(fmt.Sprintf(">**INFO** Added language server %q.", msg.Name), false)
	}
	m.afterToolChange()
}

// openSkillCreator reuses the Skills tab's modal: the prompt bar's current
// text becomes the new skill's body, so the flow stays identical.
func (m *model) openSkillCreator() tea.Cmd {
	if strings.TrimSpace(m.Prompt.Value()) == "" {
		m.MessageArea.AppendMessage(
			">**INFO** Type the skill body in the prompt bar, then press enter to name it.",
			false,
		)
		return m.Prompt.Focus()
	}
	m.skillForm.Open()
	m.Prompt.Blur()
	return nil
}

// afterToolChange re-reads the tab snapshot and rebuilds the tool registry so
// the model-visible tool list matches the live clients.
func (m *model) afterToolChange() {
	m.refreshTools()
	if err := m.rebuildToolRegistry(); err != nil {
		m.MessageArea.AppendMessage(">**ERROR** Could not refresh the tool registry: "+err.Error(), false)
	}
}

// rebuildToolRegistry re-registers the built-in tools plus whatever the
// runtime currently provides with both the dispatcher and the provider tool
// definitions. Plan mode carries over to the rebuilt dispatcher.
func (m *model) rebuildToolRegistry() error {
	base := m.baseTools
	if base == nil {
		base = initTools(m.toolRuntime)
		m.baseTools = base
	}
	all := make([]tools.AgentTool, 0, len(base)+16)
	all = append(all, base...)
	if m.toolRuntime != nil {
		all = append(all, m.toolRuntime.Tools()...)
	}

	defs := toolDefsFor(all)

	getProvider := func() providers.Provider { return nil }
	if m.model != nil {
		getProvider = m.model.Get
	}
	dispatcher, err := tools.InitDispacher(m.root, m.watchList, getProvider, all...)
	if err != nil {
		return err
	}
	dispatcher.SetPlanMode(m.planMode)
	m.tool_dispatcher = dispatcher
	if m.toolRuntime != nil {
		m.toolRuntime.SetDispatcher(dispatcher)
	}

	providers.InitToolsDef(defs)
	// InitToolsDef resets the skill tool's dynamic description; put the
	// session's loaded-skills list back immediately.
	m.syncSkillToolDef()
	return nil
}

// closeToolClients stops every MCP client and language server. Called from the
// save-on-exit filter.
func (m *model) closeToolClients() {
	if m.toolRuntime == nil {
		return
	}
	if err := m.toolRuntime.Close(); err != nil {
		log.Printf("failed to stop MCP/LSP clients: %v", err)
	}
}
