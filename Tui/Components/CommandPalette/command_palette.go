// Package commandpalette renders the slash-command autocomplete window shown
// above the prompt bar. It only owns filtering, selection and rendering; the
// commands themselves are executed by the app shell.
package commandpalette

import (
	"strings"

	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// MaxRows caps how many matches the palette shows at once. The list never
// covers more than this many rows above the prompt.
const MaxRows = 8

// Command is one palette entry.
type Command struct {
	// Name is the bare command name without the leading slash ("help").
	Name string
	// Usage is the invocation shown in the palette ("/context [path]").
	Usage string
	// Description is the one-line explanation.
	Description string
}

// Model is the autocomplete state.
type Model struct {
	commands []Command
	matches  []Command
	cursor   int
	visible  bool

	query string
	// lastInput is the most recent prompt text passed to Sync.
	lastInput string
	// dismissed remembers the exact prompt text that esc closed the palette
	// for, so it doesn't pop straight back open until the user edits again.
	dismissed string

	width int
}

var (
	nameStyle     = lipgloss.NewStyle().Foreground(style.Info).Bold(true)
	descStyle     = lipgloss.NewStyle().Foreground(style.Muted)
	selectedStyle = lipgloss.NewStyle().Background(style.Highlight).Bold(true)
)

// New returns an empty palette. Commands can be supplied up front or via
// SetCommands.
func New(commands ...Command) Model {
	m := Model{width: 80}
	m.SetCommands(commands)
	return m
}

// SetCommands replaces the registry and refilters the current query.
func (m *Model) SetCommands(commands []Command) {
	m.commands = append([]Command(nil), commands...)
	m.refilter()
	// A registry update alone doesn't mean the user is typing a command;
	// Sync decides visibility on the next prompt edit.
	m.visible = false
}

// SetWidth sets the rendered width (in columns) of the palette rows.
func (m *Model) SetWidth(width int) {
	if width > 0 {
		m.width = width
	}
}

// Sync updates visibility and matches from the prompt text. The palette only
// appears while the first token is being typed: a slash prefix with no
// whitespace yet. Once an argument or newline is present it hides.
func (m *Model) Sync(input string) {
	m.lastInput = input
	if m.dismissed != "" && input != m.dismissed {
		m.dismissed = ""
	}
	if m.dismissed == input {
		m.visible = false
		return
	}
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t\n") {
		m.visible = false
		m.matches = nil
		m.query = ""
		return
	}

	query := strings.ToLower(strings.TrimPrefix(input, "/"))
	if query != m.query {
		m.query = query
		m.cursor = 0
	}
	m.refilter()
}

func (m *Model) refilter() {
	if m.query == "" {
		m.matches = append([]Command(nil), m.commands...)
	} else {
		m.matches = m.matches[:0]
		for _, c := range m.commands {
			if strings.HasPrefix(strings.ToLower(c.Name), m.query) {
				m.matches = append(m.matches, c)
			}
		}
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.matches) {
		m.cursor = len(m.matches) - 1
	}
	m.visible = len(m.matches) > 0
}

// Visible reports whether the palette should be drawn.
func (m Model) Visible() bool { return m.visible && len(m.matches) > 0 }

// Query is the current filter text without the slash, lowercased.
func (m Model) Query() string { return m.query }

// Matches returns the commands matching the current query.
func (m Model) Matches() []Command {
	return append([]Command(nil), m.matches...)
}

// Selected returns the highlighted command.
func (m Model) Selected() (Command, bool) {
	if !m.Visible() || m.cursor < 0 || m.cursor >= len(m.matches) {
		return Command{}, false
	}
	return m.matches[m.cursor], true
}

// Move shifts the selection by delta, wrapping at both ends.
func (m *Model) Move(delta int) {
	n := len(m.matches)
	if !m.Visible() || n == 0 {
		return
	}
	m.cursor = (m.cursor + delta) % n
	if m.cursor < 0 {
		m.cursor += n
	}
}

// Close hides the palette for the current prompt text until the text changes.
func (m *Model) Close() {
	m.visible = false
	m.dismissed = m.lastInput
}

// Height is the number of rows View occupies.
func (m Model) Height() int {
	if !m.Visible() {
		return 0
	}
	if len(m.matches) > MaxRows {
		return MaxRows
	}
	return len(m.matches)
}

// View renders the palette rows, exactly Height lines.
func (m Model) View() string {
	if !m.Visible() {
		return ""
	}
	n := len(m.matches)
	start := 0
	if n > MaxRows {
		// Keep the cursor in view.
		start = m.cursor - MaxRows/2
		if start < 0 {
			start = 0
		}
		if start+MaxRows > n {
			start = n - MaxRows
		}
		n = MaxRows
	}

	lines := make([]string, 0, n)
	for i := start; i < start+n; i++ {
		c := m.matches[i]
		usage := c.Usage
		if usage == "" {
			usage = "/" + c.Name
		}
		avail := m.width - lipgloss.Width(usage) - 1
		if avail < 4 {
			avail = 4
		}
		desc := truncate(c.Description, avail)
		if i == m.cursor {
			row := truncate(usage+" "+desc, m.width)
			lines = append(lines, selectedStyle.Width(m.width).Foreground(style.Primary).Render(row))
			continue
		}
		lines = append(lines, nameStyle.Render(usage)+" "+descStyle.Render(desc))
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
