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

	selected   int
	focusInput bool
	reason     textinput.Model

	termWidth  int
	termHeight int
}

func New(toolName, description, action string) Model {
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
	m.reason.SetWidth(max(10, m.innerWidth()-14))
}

// dialogWidth / dialogHeight mirror the provider selector's float sizing.
func (m Model) dialogWidth() int {
	w := floatWidth
	if m.termWidth > 0 && m.termWidth-4 < w {
		w = max(24, m.termWidth-4)
	}
	return w
}

func (m Model) dialogHeight() int {
	h := floatHeight
	if m.termHeight > 0 && m.termHeight-2 < h {
		h = max(13, m.termHeight-2)
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
		Render("PERMISSION REQUESTED")

	help := lipgloss.NewStyle().
		Foreground(style.Muted).
		Render("↔ choose · ↕/tab toggles reason · enter ok · esc reject")

	// Short terminals give description lines up first
	info := m.renderInfo(max(1, m.dialogHeight()-16))

	buttons := m.renderButtons()

	reasonRow := m.renderReason(inner)

	return lipgloss.NewStyle().
		Border(permissionBorder).
		BorderForeground(style.Success).
		Foreground(style.Text).
		Background(style.Background).
		Padding(1, 2).
		Width(m.dialogWidth()).
		Height(m.dialogHeight()).
		Render(strings.Join([]string{title, help, "", info, "", buttons, "", reasonRow}, "\n"))
}

// renderInfo shows the tool name, what it runs and a description. maxDescLines
// caps the description so the box shrinks on short terminals.
func (m Model) renderInfo(maxDescLines int) string {
	inner := m.innerWidth()
	fill := func(label, val string) string {
		maxVal := inner - lipgloss.Width(label) - 1
		if maxVal < 1 {
			maxVal = 1
		}
		return lipgloss.NewStyle().
			Foreground(style.Text).
			Width(inner).
			Render(label + " " + truncateOneLine(val, maxVal))
	}

	desc := strings.TrimSpace(m.description)
	if desc == "" {
		desc = "no description provided"
	}
	descLines := wrap(desc, inner-2)
	if len(descLines) > maxDescLines {
		descLines = append(descLines[:maxDescLines], "…")
	}

	rows := []string{
		fill("Tool:", m.toolName),
		"",
		strings.Join(descLines, "\n"),
	}
	if a := strings.TrimSpace(m.action); a != "" {
		rows = append(rows, "", fill("Runs:", a))
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
	row := strings.Join([]string{
		m.renderButton("Accept", acceptColor, m.selected == 0),
		m.renderButton("Reject", rejectColor, m.selected == 1),
		m.renderButton("Accept All", acceptAllColor, m.selected == 2),
	}, "  ")

	return lipgloss.NewStyle().
		Width(m.dialogWidth()).
		Align(lipgloss.Center).
		Render(row)
}

func (m Model) renderReason(inner int) string {
	row := m.reason.View()
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
