// Package toolstab is the Tools tab: one place to see and manage everything
// that extends the agent at runtime. It lists the session's skills, the MCP
// servers from the app's MCP configuration, and the language servers from the
// LSP configuration. The same layout reports whether each entry is active and
// basic connection/count details, plus the last error for anything that could
// not be started.
//
// The tab is intentionally a view + intent emitter: it owns navigation and the
// small add forms, but the app applies every change (loading a skill, editing
// and saving a config, starting/stopping a client) and feeds a fresh snapshot
// back through SetData. That keeps config persistence and process ownership in
// one place.
package toolstab

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// Section identifies one of the tab's three lists.
type Section int

const (
	SectionSkills Section = iota
	SectionMCP
	SectionLSP

	sectionCount = 3
)

// String returns the section's display name.
func (s Section) String() string {
	switch s {
	case SectionMCP:
		return "MCP servers"
	case SectionLSP:
		return "LSP servers"
	default:
		return "Skills"
	}
}

// Skill is one discovered skill and its session load state.
type Skill struct {
	Name        string
	Description string
	Path        string
	Loaded      bool
}

// MCPServer is one configured MCP server and its live state. Transport is the
// effective "stdio"/"http"/"sse" transport, Connected reports whether a client
// is currently attached, Tools is the discovered tool count, and Error carries
// a startup/list failure (empty when healthy).
type MCPServer struct {
	Name      string
	Transport string
	Enabled   bool
	Connected bool
	Tools     int
	Error     string
}

// LSPServer is one configured language server. Languages is the configured
// language-ID list; Enabled reports whether the bridge will start it.
type LSPServer struct {
	Name      string
	Languages []string
	Enabled   bool
	Error     string
}

// Data is a full snapshot rendered by the tab. ConfigPath/Error fields expose
// where persistence goes and any load/save problem so the user can fix it.
type Data struct {
	Skills []Skill
	MCP    []MCPServer
	LSP    []LSPServer

	MCPConfigPath string
	LSPConfigPath string
	MCPError      string
	LSPError      string
}

// ToggleMsg asks the app to load/unload a skill or enable/disable a server.
// Enabled is the desired state (for a skill it means "loaded").
type ToggleMsg struct {
	Section Section
	Name    string
	Enabled bool
}

// RemoveMsg asks the app to delete a configured MCP or LSP server.
type RemoveMsg struct {
	Section Section
	Name    string
}

// AddMCPMsg carries the values of the MCP add form. A URL endpoint fills URL;
// otherwise Command/Args are parsed from the command line field.
type AddMCPMsg struct {
	Name    string
	Command string
	Args    []string
	URL     string
}

// AddLSPMsg carries the values of the LSP add form. Command may include
// arguments; the form splits them into Command + Args like the MCP form.
type AddLSPMsg struct {
	Name      string
	Command   string
	Args      []string
	Languages []string
}

// CreateSkillMsg asks the app to open the existing skill-creation form. The
// prompt bar's text becomes the new skill body, exactly like the Skills tab.
type CreateSkillMsg struct{}

const (
	padSide = 3
	botPad  = 1

	selCol  = 1
	dotCol  = 2
	nameCol = 22
)

type formKind int

const (
	formNone formKind = iota
	formMCP
	formLSP

	formMCPFields = 2 // name, command/url
	formLSPFields = 3 // name, command, languages
)

// Model is the tab's view state.
type Model struct {
	width  int
	height int

	data    Data
	section Section
	cursors [sectionCount]int

	form        formKind
	formActive  int
	formErr     string
	inputsReady bool
	mcpName     textinput.Model
	mcpEnd      textinput.Model
	lspName     textinput.Model
	lspCommand  textinput.Model
	lspLangs    textinput.Model

	termWidth  int
	termHeight int
}

func newTextInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = ""
	ti.CharLimit = 160
	ti.SetWidth(50)
	return ti
}

// ensureInputs initializes the form fields for callers that use the zero Model
// (tests, embedded uses) instead of New.
func (m *Model) ensureInputs() {
	if m.inputsReady {
		return
	}
	m.mcpName = newTextInput("e.g. files")
	m.mcpEnd = newTextInput("command (npx -y pkg) or https://host/mcp")
	m.lspName = newTextInput("e.g. gopls")
	m.lspCommand = newTextInput("gopls or /usr/bin/gopls")
	m.lspLangs = newTextInput("go, mod")
	m.inputsReady = true
}

// New returns an empty Tools tab.
func New() Model {
	m := Model{formActive: -1}
	m.ensureInputs()
	return m
}

// SetData replaces the rendered snapshot. The active section and cursor are
// preserved (clamped to the new lengths).
func (m *Model) SetData(data Data) {
	m.data = cloneData(data)
	m.clampCursor()
}

func cloneData(data Data) Data {
	out := data
	out.Skills = append([]Skill(nil), data.Skills...)
	out.MCP = make([]MCPServer, len(data.MCP))
	copy(out.MCP, data.MCP)
	out.LSP = make([]LSPServer, len(data.LSP))
	for i, server := range data.LSP {
		out.LSP[i] = server
		out.LSP[i].Languages = append([]string(nil), server.Languages...)
	}
	return out
}

// SetSize clamps the pane to the band below the tabs.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.width < 1 {
		m.width = 1
	}
	if m.height < 1 {
		m.height = 1
	}
	m.clampCursor()
}

// SetTermSize records the full terminal size used to center the add form.
func (m *Model) SetTermSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
	m.resizeFormInputs()
}

func (m *Model) resizeFormInputs() {
	if !m.inputsReady {
		return
	}
	w := m.dialogWidth() - 22
	if w < 10 {
		w = 10
	}
	for _, field := range []*textinput.Model{&m.mcpName, &m.mcpEnd, &m.lspName, &m.lspCommand, &m.lspLangs} {
		field.SetWidth(w)
	}
}

// SetSection focuses one of the three lists.
func (m *Model) SetSection(section Section) {
	if section < 0 || section >= sectionCount {
		return
	}
	m.section = section
	m.clampCursor()
}

// Section returns the currently focused list.
func (m Model) Section() Section { return m.section }

// MoveCursor moves the selected row within the active section.
func (m *Model) MoveCursor(delta int) {
	n := m.sectionLen(m.section)
	if n == 0 {
		m.cursors[m.section] = 0
		return
	}
	idx := m.cursors[m.section] + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	m.cursors[m.section] = idx
}

// HandleMouseWheel scrolls the active section's selection.
func (m *Model) HandleMouseWheel(up bool) {
	if up {
		m.MoveCursor(-1)
	} else {
		m.MoveCursor(1)
	}
}

// CanExitToolsMode reports whether Esc may leave the whole tab (the add form
// must be dismissed first).
func (m Model) CanExitToolsMode() bool { return m.form == formNone }

// FormVisible reports whether an add form is open.
func (m Model) FormVisible() bool { return m.form != formNone }

// OpenMCPForm opens the add-server form for MCP.
func (m *Model) OpenMCPForm() {
	m.ensureInputs()
	m.form = formMCP
	m.formActive = 0
	m.formErr = ""
	m.mcpName.SetValue("")
	m.mcpEnd.SetValue("")
	m.mcpName.Focus()
	m.mcpEnd.Blur()
	m.resizeFormInputs()
}

// OpenLSPForm opens the add-server form for LSP.
func (m *Model) OpenLSPForm() {
	m.ensureInputs()
	m.form = formLSP
	m.formActive = 0
	m.formErr = ""
	m.lspName.SetValue("")
	m.lspCommand.SetValue("")
	m.lspLangs.SetValue("")
	m.lspName.Focus()
	m.lspCommand.Blur()
	m.lspLangs.Blur()
	m.resizeFormInputs()
}

// CloseForm dismisses the add form without changing anything.
func (m *Model) CloseForm() {
	m.form = formNone
	m.formActive = -1
	m.formErr = ""
	m.mcpName.Blur()
	m.mcpEnd.Blur()
	m.lspName.Blur()
	m.lspCommand.Blur()
	m.lspLangs.Blur()
}

// Selection describes the highlighted entry.
type Selection struct {
	Section Section
	Index   int
	Name    string
	Enabled bool
}

// Selected returns the highlighted entry, if the active section has one.
func (m Model) Selected() (Selection, bool) {
	idx := m.cursors[m.section]
	switch m.section {
	case SectionSkills:
		if idx < 0 || idx >= len(m.data.Skills) {
			return Selection{}, false
		}
		s := m.data.Skills[idx]
		return Selection{Section: m.section, Index: idx, Name: s.Name, Enabled: s.Loaded}, true
	case SectionMCP:
		if idx < 0 || idx >= len(m.data.MCP) {
			return Selection{}, false
		}
		s := m.data.MCP[idx]
		return Selection{Section: m.section, Index: idx, Name: s.Name, Enabled: s.Enabled}, true
	case SectionLSP:
		if idx < 0 || idx >= len(m.data.LSP) {
			return Selection{}, false
		}
		s := m.data.LSP[idx]
		return Selection{Section: m.section, Index: idx, Name: s.Name, Enabled: s.Enabled}, true
	}
	return Selection{}, false
}

func (m *Model) sectionLen(section Section) int {
	switch section {
	case SectionSkills:
		return len(m.data.Skills)
	case SectionMCP:
		return len(m.data.MCP)
	case SectionLSP:
		return len(m.data.LSP)
	default:
		return 0
	}
}

func (m *Model) clampCursor() {
	for section := Section(0); section < sectionCount; section++ {
		idx := m.cursors[section]
		if n := m.sectionLen(section); n == 0 {
			idx = 0
		} else if idx >= n {
			idx = n - 1
		}
		if idx < 0 {
			idx = 0
		}
		m.cursors[section] = idx
	}
}

// Update handles list navigation and the add forms. Every mutating key returns
// a tea.Cmd whose message the app applies.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if m.form != formNone {
			var cmd tea.Cmd
			if field := m.formField(m.formActive); field != nil {
				*field, cmd = field.Update(msg)
			}
			return m, cmd
		}
		return m, nil
	}
	if m.form != formNone {
		return m.updateForm(key)
	}

	switch key.String() {
	case "up", "k":
		m.MoveCursor(-1)
	case "down", "j":
		m.MoveCursor(1)
	case "left", "h":
		if m.section > 0 {
			m.section--
		}
	case "right", "l":
		if m.section < sectionCount-1 {
			m.section++
		}
	case "1":
		m.section = SectionSkills
	case "2":
		m.section = SectionMCP
	case "3":
		m.section = SectionLSP
	case " ", "space", "enter":
		if sel, ok := m.Selected(); ok {
			return m, func() tea.Msg {
				return ToggleMsg{Section: sel.Section, Name: sel.Name, Enabled: !sel.Enabled}
			}
		}
	case "p":
		if sel, ok := m.Selected(); ok && sel.Section == SectionSkills && !sel.Enabled {
			return m, func() tea.Msg {
				return ToggleMsg{Section: sel.Section, Name: sel.Name, Enabled: true}
			}
		}
	case "x":
		if sel, ok := m.Selected(); ok && sel.Section == SectionSkills && sel.Enabled {
			return m, func() tea.Msg {
				return ToggleMsg{Section: sel.Section, Name: sel.Name, Enabled: false}
			}
		}
	case "a":
		switch m.section {
		case SectionMCP:
			m.OpenMCPForm()
		case SectionLSP:
			m.OpenLSPForm()
		case SectionSkills:
			return m, func() tea.Msg { return CreateSkillMsg{} }
		}
	case "c":
		return m, func() tea.Msg { return CreateSkillMsg{} }
	case "d":
		if sel, ok := m.Selected(); ok && sel.Section != SectionSkills {
			return m, func() tea.Msg {
				return RemoveMsg{Section: sel.Section, Name: sel.Name}
			}
		}
	}
	return m, nil
}

func (m *Model) updateForm(key tea.KeyPressMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.CloseForm()
		return *m, nil
	case "tab", "down":
		m.focusFormField((m.formActive + 1) % m.formFieldCount())
		return *m, nil
	case "shift+tab", "up":
		m.focusFormField((m.formActive - 1 + m.formFieldCount()) % m.formFieldCount())
		return *m, nil
	case "enter":
		if m.formComplete() {
			return m.submitForm()
		}
		m.formErr = "fill every field first"
		m.focusFormField((m.formActive + 1) % m.formFieldCount())
		return *m, nil
	}

	var cmd tea.Cmd
	field := m.formField(m.formActive)
	if field != nil {
		*field, cmd = field.Update(key)
	}
	return *m, cmd
}

func (m *Model) formFieldCount() int {
	if m.form == formLSP {
		return formLSPFields
	}
	return formMCPFields
}

func (m *Model) formField(i int) *textinput.Model {
	if m.form == formLSP {
		switch i {
		case 0:
			return &m.lspName
		case 1:
			return &m.lspCommand
		case 2:
			return &m.lspLangs
		}
		return nil
	}
	switch i {
	case 0:
		return &m.mcpName
	case 1:
		return &m.mcpEnd
	}
	return nil
}

func (m *Model) focusFormField(i int) {
	if i < 0 {
		i = 0
	}
	if m.form != formNone && i >= m.formFieldCount() {
		i = m.formFieldCount() - 1
	}
	m.formActive = i
	for idx := 0; idx < m.formFieldCount(); idx++ {
		if field := m.formField(idx); field != nil {
			if idx == i {
				field.Focus()
			} else {
				field.Blur()
			}
		}
	}
}

func (m Model) formComplete() bool {
	switch m.form {
	case formMCP:
		if strings.TrimSpace(m.mcpName.Value()) == "" {
			return false
		}
		command, _, url := parseMCPEndpoint(m.mcpEnd.Value())
		return command != "" || url != ""
	case formLSP:
		return strings.TrimSpace(m.lspName.Value()) != "" &&
			strings.TrimSpace(m.lspCommand.Value()) != "" &&
			len(splitLanguages(m.lspLangs.Value())) > 0
	default:
		return false
	}
}

func (m *Model) submitForm() (Model, tea.Cmd) {
	switch m.form {
	case formMCP:
		name := strings.TrimSpace(m.mcpName.Value())
		command, args, url := parseMCPEndpoint(m.mcpEnd.Value())
		m.CloseForm()
		return *m, func() tea.Msg {
			return AddMCPMsg{Name: name, Command: command, Args: args, URL: url}
		}
	case formLSP:
		name := strings.TrimSpace(m.lspName.Value())
		command, args := parseCommandLine(m.lspCommand.Value())
		langs := splitLanguages(m.lspLangs.Value())
		m.CloseForm()
		return *m, func() tea.Msg {
			return AddLSPMsg{Name: name, Command: command, Args: args, Languages: langs}
		}
	}
	m.CloseForm()
	return *m, nil
}

// parseMCPEndpoint turns the add form's single endpoint field into either a
// URL or a command plus args.
func parseMCPEndpoint(raw string) (command string, args []string, url string) {
	endpoint := strings.TrimSpace(raw)
	lower := strings.ToLower(endpoint)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", nil, endpoint
	}
	fields := strings.Fields(endpoint)
	if len(fields) == 0 {
		return "", nil, ""
	}
	return fields[0], fields[1:], ""
}

// parseCommandLine splits a command field such as "gopls -remote=auto" into
// the executable and its arguments.
func parseCommandLine(raw string) (command string, args []string) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

func splitLanguages(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

// --- rendering ------------------------------------------------------------

// View renders the bordered three-section pane.
func (m Model) View() string {
	frameW := m.width - padSide*2
	if frameW < 4 {
		frameW = 4
	}
	frame := drawFrame(frameW, m.height-botPad*2, m.body())

	return lipgloss.NewStyle().
		Margin(0, padSide).
		Width(frameW).
		Render(frame)
}

type bodyRow struct {
	text     string
	section  Section
	item     int
	selected bool
}

func (m Model) body() []string {
	lines := m.height - botPad*2 - 2
	if lines < 1 {
		lines = 1
	}

	header := lipgloss.NewStyle().Foreground(style.Muted).Render(
		"←/→ section · ↑/↓ select · space toggle · a add · d remove · c new skill · i prompt · esc exit")

	rows := m.sectionRows()
	available := lines - 2 // header + footer
	if available < 1 {
		available = 1
	}
	top := 0
	if len(rows) > available {
		selected := 0
		for i, row := range rows {
			if row.selected {
				selected = i
				break
			}
		}
		top = selected - available/2
		if top > len(rows)-available {
			top = len(rows) - available
		}
		if top < 0 {
			top = 0
		}
		rows = rows[top : top+available]
	}

	out := make([]string, 0, lines)
	out = append(out, header)
	for _, row := range rows {
		out = append(out, row.text)
	}
	out = append(out, m.counts())
	for len(out) < lines {
		out = append(out, strings.Repeat(" ", m.contentWidth()))
	}
	if len(out) > lines {
		out = out[:lines]
	}
	return out
}

func (m Model) sectionRows() []bodyRow {
	var rows []bodyRow
	innerW := m.contentWidth()

	rows = append(rows, bodyRow{text: m.sectionHeader(SectionSkills, len(m.data.Skills))})
	if len(m.data.Skills) == 0 {
		rows = append(rows, bodyRow{text: m.emptyRow("no skills discovered - press c to create one")})
	}
	for i, skill := range m.data.Skills {
		rows = append(rows, bodyRow{
			text:     m.skillRow(skill, i == m.cursors[SectionSkills], innerW),
			section:  SectionSkills,
			item:     i,
			selected: m.section == SectionSkills && i == m.cursors[SectionSkills],
		})
	}

	rows = append(rows, bodyRow{text: m.sectionHeader(SectionMCP, len(m.data.MCP))})
	if m.data.MCPError != "" {
		rows = append(rows, bodyRow{text: m.errorRow(m.data.MCPError)})
	}
	if len(m.data.MCP) == 0 {
		rows = append(rows, bodyRow{text: m.emptyRow("no MCP servers configured - press a to add one")})
	}
	for i, server := range m.data.MCP {
		rows = append(rows, bodyRow{
			text:     m.mcpRow(server, i == m.cursors[SectionMCP], innerW),
			section:  SectionMCP,
			item:     i,
			selected: m.section == SectionMCP && i == m.cursors[SectionMCP],
		})
	}

	rows = append(rows, bodyRow{text: m.sectionHeader(SectionLSP, len(m.data.LSP))})
	if m.data.LSPError != "" {
		rows = append(rows, bodyRow{text: m.errorRow(m.data.LSPError)})
	}
	if len(m.data.LSP) == 0 {
		rows = append(rows, bodyRow{text: m.emptyRow("no language servers configured - press a to add one")})
	}
	for i, server := range m.data.LSP {
		rows = append(rows, bodyRow{
			text:     m.lspRow(server, i == m.cursors[SectionLSP], innerW),
			section:  SectionLSP,
			item:     i,
			selected: m.section == SectionLSP && i == m.cursors[SectionLSP],
		})
	}
	return rows
}

func (m Model) sectionHeader(section Section, count int) string {
	label := fmt.Sprintf("%s (%d)", section.String(), count)
	switch section {
	case SectionSkills:
		loaded := 0
		for _, skill := range m.data.Skills {
			if skill.Loaded {
				loaded++
			}
		}
		label = fmt.Sprintf("Skills (%d/%d loaded)", loaded, count)
	case SectionMCP:
		enabled, connected, toolCount := 0, 0, 0
		for _, server := range m.data.MCP {
			if server.Enabled {
				enabled++
			}
			if server.Connected {
				connected++
			}
			toolCount += server.Tools
		}
		label = fmt.Sprintf("MCP servers (%d/%d enabled · %d connected · %d tools)",
			enabled, count, connected, toolCount)
	case SectionLSP:
		enabled := 0
		for _, server := range m.data.LSP {
			if server.Enabled {
				enabled++
			}
		}
		label = fmt.Sprintf("LSP servers (%d/%d enabled)", enabled, count)
	}
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(style.Info)
	if m.section == section {
		headerStyle = headerStyle.Background(style.Highlight)
	}
	return headerStyle.Width(m.contentWidth()).Render(clip(label, 0, m.contentWidth()))
}

func (m Model) emptyRow(text string) string {
	return lipgloss.NewStyle().Foreground(style.Muted).Render(
		clip("  "+text, 0, m.contentWidth()))
}

func (m Model) errorRow(text string) string {
	return lipgloss.NewStyle().Foreground(style.Warning).Render(
		clip("  ! "+text, 0, m.contentWidth()))
}

func (m Model) skillRow(skill Skill, selected bool, innerW int) string {
	dot := "○"
	if skill.Loaded {
		dot = "●"
	}
	return m.itemRow(dot, skill.Name, skill.Description, selected, innerW)
}

func (m Model) mcpRow(server MCPServer, selected bool, innerW int) string {
	dot := "○"
	if server.Enabled {
		dot = "●"
	}
	status := "disabled"
	if server.Enabled {
		status = "enabled"
		if server.Connected {
			status = fmt.Sprintf("connected · %d tool(s)", server.Tools)
		} else {
			status = "not connected"
		}
	}
	if server.Error != "" {
		status += " · " + server.Error
	}
	details := server.Transport
	if details != "" {
		details += " · "
	}
	return m.itemRow(dot, server.Name, details+status, selected, innerW)
}

func (m Model) lspRow(server LSPServer, selected bool, innerW int) string {
	dot := "○"
	if server.Enabled {
		dot = "●"
	}
	status := "disabled"
	if server.Enabled {
		status = "enabled"
	}
	if len(server.Languages) > 0 {
		status = "[" + strings.Join(server.Languages, ", ") + "] · " + status
	}
	if server.Error != "" {
		status += " · " + server.Error
	}
	return m.itemRow(dot, server.Name, status, selected, innerW)
}

func (m Model) itemRow(dot, name, desc string, selected bool, innerW int) string {
	sel := " "
	if selected {
		sel = "▸"
	}
	selStyled := lipgloss.NewStyle().Foreground(style.Info).Width(selCol).Render(sel)
	dotStyled := lipgloss.NewStyle().Width(dotCol).Render(dot)

	clippedName := clip(name, 0, nameCol)
	nameStyle := lipgloss.NewStyle().Width(nameCol).Foreground(style.Text)
	if selected {
		nameStyle = nameStyle.Bold(true)
	}

	descW := innerW - selCol - 1 - dotCol - 1 - nameCol - 1
	if descW < 0 {
		descW = 0
	}
	descStyle := lipgloss.NewStyle().Width(descW).Foreground(style.Muted).Render(clip(desc, 0, descW))

	row := selStyled + " " + dotStyled + " " + nameStyle.Render(clippedName) + " " + descStyle
	if selected {
		row = lipgloss.NewStyle().
			Background(style.Highlight).
			Foreground(style.Text).
			Render(row)
	}
	return row
}

func (m Model) counts() string {
	tools := 0
	for _, server := range m.data.MCP {
		tools += server.Tools
	}
	text := fmt.Sprintf("%d skill(s) · %d MCP server(s) · %d tool(s) · %d LSP server(s)",
		len(m.data.Skills), len(m.data.MCP), tools, len(m.data.LSP))
	if m.data.MCPConfigPath != "" || m.data.LSPConfigPath != "" {
		text += " · config: " + m.data.MCPConfigPath
		if m.data.LSPConfigPath != "" {
			text += ", " + m.data.LSPConfigPath
		}
	}
	return clip(text, 0, m.contentWidth())
}

// View renders just the open add form (or an empty string).
func (m Model) formView() string {
	if m.form == formNone {
		return ""
	}
	title := "Add MCP server"
	sub := "command starts a stdio server; http(s) uses a remote server"
	labels := []string{"name", "command / url"}
	if m.form == formLSP {
		title = "Add language server"
		sub = "languages are comma-separated LSP language IDs"
		labels = []string{"name", "command / args", "languages"}
	}

	rows := make([]string, 0, len(labels))
	for i, label := range labels {
		field := m.formField(i)
		value := ""
		if field != nil {
			value = field.View()
		}
		lblStyle := lipgloss.NewStyle().Bold(true).Foreground(style.Muted)
		if i == m.formActive {
			lblStyle = lblStyle.Foreground(style.Info)
		}
		rows = append(rows, lblStyle.Render(label)+"  "+value)
	}

	help := "enter add · tab next · esc cancel"
	if m.formErr != "" {
		help = m.formErr + " · " + help
	}
	content := lipgloss.NewStyle().Bold(true).Foreground(style.Info).Render(title) + "\n" +
		lipgloss.NewStyle().Foreground(style.Muted).Render(sub) + "\n\n" +
		strings.Join(rows, "\n") + "\n\n" +
		lipgloss.NewStyle().Foreground(style.Muted).Render(help)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Background(style.Background).
		Padding(1, 2).
		Width(m.dialogWidth()).
		Height(m.dialogHeight()).
		Render(content)
}

// Overlay centers the add form over the tab content.
func (m Model) Overlay(bg string) string {
	if m.form == formNone {
		return bg
	}
	dialog := m.formView()
	w := m.termWidth
	h := m.termHeight
	if w <= 0 {
		w = lipgloss.Width(bg)
	}
	if h <= 0 {
		h = lipgloss.Height(bg)
	}
	dw := lipgloss.Width(dialog)
	dh := lipgloss.Height(dialog)
	x := (w - dw) / 2
	y := (h - dh) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	base := lipgloss.NewLayer(bg).X(0).Y(0).Z(0)
	floating := lipgloss.NewLayer(dialog).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, floating).Render()
}

func (m Model) dialogWidth() int {
	w := 72
	if m.termWidth > 0 && m.termWidth-4 < w {
		w = max(34, m.termWidth-4)
	}
	return w
}

func (m Model) dialogHeight() int {
	h := 14
	if m.form == formLSP {
		h = 15
	}
	if m.termHeight > 0 && m.termHeight-2 < h {
		h = max(8, m.termHeight-2)
	}
	return h
}

// contentWidth is the usable width of the pane body after the outer margin and
// the frame's two border columns plus one padding column on either side.
func (m Model) contentWidth() int {
	w := m.width - padSide*2 - 4
	if w < 3 {
		w = 3
	}
	return w
}

// drawFrame renders the full bordered panel (same chrome as the skills and
// stats panes). Body lines are padded/truncated to innerW so ragged content
// can't blow out the frame.
func drawFrame(w, h int, body []string) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	innerW := w - 4
	innerH := h - 2
	rowStyle := lipgloss.NewStyle().MaxWidth(innerW).Width(innerW)
	lines := make([]string, 0, innerH)
	for i := 0; i < innerH; i++ {
		var line string
		if i < len(body) {
			line = body[i]
		}
		lines = append(lines, rowStyle.Render(line))
	}
	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Background(style.Background).
		Padding(0, 1).
		Width(w - 2).
		Height(h - 2).
		Render(content)
}

// clip returns the substring of s whose columns lie in [start, start+maxW),
// padded with trailing spaces. Only plain text (no ANSI styling) may be
// passed: rune slicing escape sequences would corrupt them.
func clip(s string, start, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	runes := []rune(s)
	if start > 0 {
		if start >= len(runes) {
			return strings.Repeat(" ", maxW)
		}
		runes = runes[start:]
	}
	if len(runes) > maxW {
		runes = runes[:maxW]
	}
	pad := maxW - lipgloss.Width(string(runes))
	if pad < 0 {
		pad = 0
	}
	return string(runes) + strings.Repeat(" ", pad)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
