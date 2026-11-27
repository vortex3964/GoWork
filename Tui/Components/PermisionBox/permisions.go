// Package permisionbox is the popup that asks whether a tool the AI requested
// may run. KindNotAllowed tools pause here until the user picks a decision.
package permisionbox

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

const (
	floatWidth  = 56
	floatHeight = 18

	// Decision colors: the selected button fills with its color, the others
	// keep the color as their outline.
	acceptColor    = "#35d445"
	rejectColor    = "#e00707"
	acceptAllColor = "#7707e0"
)

// Action is what the user chose for the pending tool.
type Action int

const (
	ActionAccept Action = iota
	ActionReject
	ActionAcceptAll
)

// DecisionMsg is emitted when the user resolves the popup. Reason is whatever
// they typed on the optional input line - forwards to the AI either way.
type DecisionMsg struct {
	Type   Action
	Reason string
}

// Impact is the read-only explanation of what a tool call will actually do.
// Summary is short prose about the concrete effect; Preview optionally holds
// bounded, pre-formatted before/after or diff lines. Both are display-only.
type Impact struct {
	Summary string
	Preview string
}

// PermissionBorder is the box-drawing border with string corners.
var permissionBorder = lipgloss.Border{
	Top:         "─",
	Bottom:      "─",
	Left:        "│",
	Right:       "│",
	TopLeft:     "╔",
	TopRight:    "╗",
	BottomLeft:  "╚",
	BottomRight: "╝",
}

type Model struct {
	visible bool

	toolName    string
	description string
	action      string
	impact      Impact

	selected   int
	focusInput bool
	reason     textinput.Model

	termWidth  int
	termHeight int
}

func New(toolName, description, action string, impact Impact) Model {
	ti := textinput.New()
	ti.Prompt = "Reason: "
	ti.Placeholder = "write a note for the AI (optional)"
	ti.CharLimit = 500
	ti.Blur()

	return Model{
		visible:     true,
		toolName:    toolName,
		description: description,
		action:      action,
		impact:      impact,
		reason:      ti,
	}
}

// Hide clears the popup so Overlay passes the background through.
func (m *Model) Hide() {
	m.visible = false
}

func (m Model) Visible() bool { return m.visible }

// SetSize stores the terminal area the popup floats inside.
func (m *Model) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
	m.reason.SetWidth(max(4, m.innerWidth()-10))
}

// dialogWidth / dialogHeight mirror the provider selector's float sizing.
func (m Model) dialogWidth() int {
	w := floatWidth
	if m.termWidth > 0 {
		if maxW := m.termWidth - 2; maxW < w {
			w = maxW
		}
		if w < 16 {
			w = 16
		}
	}
	return w
}

func (m Model) dialogHeight() int {
	h := floatHeight
	if m.termHeight > 0 {
		// The outer border (2 rows) and vertical padding (2 rows) sit outside
		// the content box, so leave room for all four.
		maxH := m.termHeight - 4
		if maxH < 6 {
			maxH = 6
		}
		if h > maxH {
			h = maxH
		}
	}
	return h
}

// innerWidth is the content column inside the border + padding.
func (m Model) innerWidth() int {
	return max(10, m.dialogWidth()-6)
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.visible {
		return m, nil
	}

	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	k := key.String()

	if m.focusInput {
		switch k {
		case "enter":
			m.reason.Blur()
			return m, m.resolve()
		case "esc":
			m.reason.Blur()
			m.selected = 1
			return m, m.resolve()
		case "tab", "up":
			m.reason.Blur()
			m.focusInput = false
		default:
			var cmd tea.Cmd
			m.reason, cmd = m.reason.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch k {
	case "left":
		m.selected = (m.selected + 2) % 3
	case "right":
		m.selected = (m.selected + 1) % 3
	case "tab", "down":
		m.focusInput = true
		m.reason.Focus()
	case "enter":
		return m, m.resolve()
	case "esc":
		m.selected = 1
		return m, m.resolve()
	}
	return m, nil
}

// resolve turns the current selection into a DecisionMsg. A picked reason
// always travels with it.
func (m Model) resolve() tea.Cmd {
	action := ActionAccept
	switch m.selected {
	case 1:
		action = ActionReject
	case 2:
		action = ActionAcceptAll
	}
	reason := strings.TrimSpace(m.reason.Value())
	return func() tea.Msg {
		return DecisionMsg{Type: action, Reason: reason}
	}
}

func (m Model) renderDialog() string {
	inner := m.innerWidth()

	title := lipgloss.NewStyle().
		Foreground(style.Success).
		Bold(true).
		Render(truncateOneLine("PERMISSION REQUESTED", inner))

	helpText := "↔ choose · ↕/tab toggles reason · enter ok · esc reject"
	if inner < 40 {
		helpText = "↔ choose · enter ok · esc reject"
	}

	contentHeight := m.dialogHeight()

	// Short terminals get fewer help/description rows so the impact preview
	// and the decision controls stay visible. They also drop the decorative
	// blank lines to reclaim vertical space.
	compact := contentHeight < 14
	maxHelpLines := 2
	if compact {
		maxHelpLines = 1
	}
	var helpLines []string
	if contentHeight >= 8 {
		helpLines = wrap(helpText, inner)
		if len(helpLines) > maxHelpLines {
			helpLines = helpLines[:maxHelpLines]
			helpLines[len(helpLines)-1] = truncateOneLine(helpLines[len(helpLines)-1]+" …", inner)
		}
		helpStyle := lipgloss.NewStyle().Foreground(style.Muted)
		for i := range helpLines {
			helpLines[i] = helpStyle.Render(helpLines[i])
		}
	}

	// Rows outside the info box: title, help, the separators, buttons and
	// reason. Everything left belongs to the info block (including its own
	// border).
	separatorRows := 3
	if compact {
		separatorRows = 0
	}
	fixedRows := 1 + len(helpLines) + separatorRows + 1 + 1
	maxInfoRows := contentHeight - fixedRows
	if maxInfoRows < 3 {
		maxInfoRows = 3
	}

	info := m.renderInfo(maxInfoRows)
	buttons := m.renderButtons()
	reasonRow := m.renderReason(inner)

	rows := []string{title}
	rows = append(rows, helpLines...)
	if !compact {
		rows = append(rows, "")
	}
	rows = append(rows, info)
	if !compact {
		rows = append(rows, "")
	}
	rows = append(rows, buttons)
	if !compact {
		rows = append(rows, "")
	}
	rows = append(rows, reasonRow)

	return lipgloss.NewStyle().
		Border(permissionBorder).
		BorderForeground(style.Success).
		Foreground(style.Text).
		Background(style.Background).
		Padding(1, 2).
		Width(m.dialogWidth()).
		Height(m.dialogHeight()).
		Render(strings.Join(rows, "\n"))
}

// renderInfo renders the tool name, concrete impact (with an optional bounded
// diff preview), the one-line argument summary and the generic description.
// maxRows caps the whole box (including its border) so the dialog never grows
// past the space budgeted by renderDialog.
func (m Model) renderInfo(maxRows int) string {
	inner := m.innerWidth()
	// The rounded border (2 cols) and horizontal padding (2 cols) sit inside
	// the info box, so text must wrap to the remaining width or lipgloss will
	// wrap it a second time and blow the row budget.
	textWidth := max(4, inner-4)
	contentBudget := maxRows - 2 // rounded border top/bottom
	if contentBudget < 1 {
		contentBudget = 1
	}

	fill := func(label, val string) string {
		maxVal := textWidth - lipgloss.Width(label) - 1
		if maxVal < 1 {
			maxVal = 1
		}
		return lipgloss.NewStyle().
			Foreground(style.Text).
			Width(textWidth).
			Render(label + " " + truncateOneLine(val, maxVal))
	}

	action := strings.TrimSpace(m.action)
	desc := strings.TrimSpace(m.description)
	if desc == "" {
		desc = "no description provided"
	}
	summary := strings.TrimSpace(m.impact.Summary)

	rows := []string{fill("Tool:", m.toolName)}

	// Reserve one row for the concrete argument summary when there is one,
	// so the command/path stays visible even on cramped terminals.
	reserveAction := 0
	if action != "" {
		reserveAction = 1
	}
	bodyBudget := contentBudget - len(rows) - reserveAction
	if bodyBudget < 1 {
		bodyBudget = 1
	}

	// Impact summary has priority over the generic description and preview.
	if summary != "" && bodyBudget > 0 {
		summaryLines := wrap(summary, max(2, textWidth-2))
		summaryLines[0] = "Impact: " + summaryLines[0]
		for i := 1; i < len(summaryLines); i++ {
			summaryLines[i] = "  " + summaryLines[i]
		}
		summaryLines[0] = truncateOneLine(summaryLines[0], textWidth)
		maxSummary := 3
		if len(summaryLines) > maxSummary {
			summaryLines = summaryLines[:maxSummary]
			summaryLines[len(summaryLines)-1] = truncateOneLine(summaryLines[len(summaryLines)-1]+" …", textWidth)
		}
		if len(summaryLines) > bodyBudget {
			summaryLines = summaryLines[:bodyBudget]
			summaryLines[len(summaryLines)-1] = truncateOneLine(summaryLines[len(summaryLines)-1]+" …", textWidth)
		}
		rows = append(rows, summaryLines...)
		bodyBudget -= len(summaryLines)
	}

	// Bounded diff/before-after preview, one line each.
	if preview := strings.TrimSpace(m.impact.Preview); preview != "" && bodyBudget > 0 {
		previewLines := strings.Split(preview, "\n")
		maxPreview := 6
		if len(previewLines) > maxPreview {
			previewLines = previewLines[:maxPreview]
			previewLines = append(previewLines, "…")
		}
		if len(previewLines) > bodyBudget {
			previewLines = previewLines[:bodyBudget]
			previewLines[len(previewLines)-1] = truncateOneLine(previewLines[len(previewLines)-1]+" …", textWidth)
		}
		for _, line := range previewLines {
			rows = append(rows, lipgloss.NewStyle().
				Foreground(style.Muted).
				Width(textWidth).
				Render(truncateOneLine(strings.ReplaceAll(line, "\t", "    "), textWidth)))
		}
		bodyBudget -= len(previewLines)
	}

	if action != "" {
		rows = append(rows, fill("Runs:", action))
	}
	if bodyBudget > 0 && contentBudget > len(rows) {
		descLines := wrap(desc, textWidth)
		remaining := contentBudget - len(rows)
		if len(descLines) > remaining {
			descLines = descLines[:remaining]
			if len(descLines) > 0 {
				descLines[len(descLines)-1] = truncateOneLine(descLines[len(descLines)-1]+" …", textWidth)
			}
		}
		rows = append(rows, descLines...)
	}

	if len(rows) > contentBudget {
		rows = rows[:contentBudget]
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Padding(0, 1).
		Width(inner).
		Render(strings.Join(rows, "\n"))
}

func (m Model) renderButton(label string, color string, selected bool) string {
	st := lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color(color))
	if selected {
		st = st.
			Background(lipgloss.Color(color)).
			Foreground(lipgloss.Color("#000000")).
			Bold(true)
	} else {
		st = st.Background(style.Highlight)
	}
	return st.Render(label)
}

func (m Model) renderButtons() string {
	inner := m.innerWidth()
	render := func(label, color string, selected bool) string {
		return m.renderButton(label, color, selected)
	}
	renderTight := func(label, color string, selected bool) string {
		st := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		if selected {
			st = st.Background(lipgloss.Color(color)).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)
		}
		return st.Render(label)
	}
	build := func(accept, reject, all, gap string) string {
		return strings.Join([]string{
			render(accept, acceptColor, m.selected == 0),
			render(reject, rejectColor, m.selected == 1),
			render(all, acceptAllColor, m.selected == 2),
		}, gap)
	}

	// Shrink the row on narrow terminals (never its choices, just spacing,
	// padding and, when truly cramped, the labels).
	candidates := []struct {
		accept, reject, all, gap string
	}{
		{"Accept", "Reject", "Accept All", "  "},
		{"Accept", "Reject", "All", " "},
		{"OK", "No", "All", " "},
	}
	row := build(candidates[len(candidates)-1].accept, candidates[len(candidates)-1].reject, candidates[len(candidates)-1].all, candidates[len(candidates)-1].gap)
	for _, c := range candidates {
		if r := build(c.accept, c.reject, c.all, c.gap); lipgloss.Width(r) <= inner {
			row = r
			break
		}
	}
	if lipgloss.Width(row) > inner {
		row = strings.Join([]string{
			renderTight("OK", acceptColor, m.selected == 0),
			renderTight("No", rejectColor, m.selected == 1),
			renderTight("All", acceptAllColor, m.selected == 2),
		}, " ")
	}

	return lipgloss.NewStyle().
		Width(inner).
		Align(lipgloss.Center).
		Render(row)
}

func (m Model) renderReason(inner int) string {
	row := m.reason.View()
	if idx := strings.IndexByte(row, '\n'); idx >= 0 {
		row = row[:idx]
	}
	row = truncateOneLine(row, inner)
	if m.focusInput {
		rowstyle := lipgloss.NewStyle().
			Foreground(style.Text).
			Background(style.Highlight).
			Padding(0, 1).
			Width(inner)
		return rowstyle.Render(row)
	}
	return lipgloss.NewStyle().
		Foreground(style.Muted).
		Width(inner).
		Render(row)
}

func (m Model) View() string {
	if !m.visible {
		return ""
	}
	return m.renderDialog()
}

// Overlay centers the permission popup on top of bg, same as the provider
// selector.
func (m Model) Overlay(bg string) string {
	if !m.visible {
		return bg
	}

	popup := m.renderDialog()
	dw := lipgloss.Width(popup)
	dh := lipgloss.Height(popup)
	w := m.termWidth
	h := m.termHeight
	if w <= 0 {
		w = lipgloss.Width(bg)
	}
	if h <= 0 {
		h = lipgloss.Height(bg)
	}
	x := max(0, (w-dw)/2)
	y := max(0, (h-dh)/2)

	base := lipgloss.NewLayer(bg).X(0).Y(0).Z(0)
	float := lipgloss.NewLayer(popup).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, float).Render()
}

func truncateOneLine(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > maxW-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// wrap word-wraps s to at most maxW cells per line.
func wrap(s string, maxW int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		if cur == "" {
			cur = word
			continue
		}
		if lipgloss.Width(cur)+1+lipgloss.Width(word) <= maxW {
			cur += " " + word
			continue
		}
		if lipgloss.Width(cur) > maxW {
			cur = truncateOneLine(cur, maxW)
		}
		lines = append(lines, cur)
		cur = word
	}
	if cur != "" {
		if lipgloss.Width(cur) > maxW {
			cur = truncateOneLine(cur, maxW)
		}
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}
