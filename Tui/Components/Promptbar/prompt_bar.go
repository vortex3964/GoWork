package promptbar

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Components/CommandPalette"
	"GoWork/Tui/Style"
)

// visibleLines is the bar's resting height: it starts at 2 rows and grows up
// to maxVisibleLines as the user types, then scrolls instead of growing past
// the cap so a long prompt stays readable without eating the whole window.
const (
	visibleLines    = 2
	maxVisibleLines = 6
	maxContentLines = 1 << 20 // effectively unlimited; only caps the view
)

const (
	borderHeight = 2 // top+bottom border rows rendered by PromptStyle
)

const (
	borderWidth  = 2
	paddingWidth = 2
	marginSide   = 3 // leaves x cells of space on the left and right
)

type Model struct {
	area textarea.Model
	// palette is the slash-command autocomplete window rendered above the
	// prompt box. Its height is part of Height so the shell re-layouts the
	// band when it appears/disappears.
	palette commandpalette.Model
}

// HeightChangedMsg asks main.go to redo the band layout because the prompt
// bar's height changed (grew to maxVisibleLines, or shrank back).
type HeightChangedMsg struct{}

func baseStyles() textarea.Styles {
	state := textarea.StyleState{
		Text:        lipgloss.NewStyle().Foreground(style.Text),
		Placeholder: lipgloss.NewStyle().Foreground(style.Muted).Italic(true),
		CursorLine:  lipgloss.NewStyle(), // no per-line highlight band
	}
	return textarea.Styles{
		Focused: state,
		Blurred: state,
		Cursor: textarea.CursorStyle{
			Color: style.Primary,
			Blink: true,
		},
	}
}

func New() Model {
	ta := textarea.New()
	ta.Placeholder = "enter a prompt ... · ctrl+a copy · ctrl+y select messages"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.EndOfBufferCharacter = ' '
	ta.SetStyles(baseStyles())
	// Dynamic height: the bar grows from visibleLines up to maxVisibleLines
	// as the user types. Past the cap the inner viewport scrolls, so content
	// is never blocked or lost - only the box stops growing.
	ta.DynamicHeight = true
	ta.MinHeight = visibleLines
	ta.MaxHeight = maxVisibleLines
	ta.MaxContentHeight = maxContentLines
	ta.SetHeight(visibleLines)
	ta.SetWidth(20)
	ta.Blur()
	return Model{area: ta, palette: commandpalette.New()}
}

// SetCommands installs the slash-command registry used by the palette.
func (m *Model) SetCommands(commands []commandpalette.Command) {
	m.palette.SetCommands(commands)
	m.palette.Sync(m.area.Value())
}

// Height returns the bar's full rendered height in terminal rows: the
// palette rows (when visible), the visible text rows, and the two border rows.
func (m Model) Height() int {
	return m.area.Height() + borderHeight + m.palette.Height()
}

// CurrentContentRows returns how many text rows are currently visible in the
// textarea (between visibleLines and maxVisibleLines).
func (m Model) CurrentContentRows() int {
	return m.area.Height()
}

func (m *Model) SetWidth(outerWidth int) {
	// Subtract the margin from both sides to constrain the inner textarea
	inner := outerWidth - borderWidth - paddingWidth - (marginSide * 2)
	if inner < 1 {
		inner = 1
	}
	m.area.SetWidth(inner)
	m.palette.SetWidth(outerWidth - marginSide*2)
}

func (m *Model) Focus() tea.Cmd {
	return m.area.Focus()
}

func (m *Model) Blur() {
	m.area.Blur()
	m.palette.Sync("")
}

func (m Model) Focused() bool {
	return m.area.Focused()
}

func (m *Model) ScrollUp() {
	m.area.CursorUp()
}

func (m *Model) ScrollDown() {
	m.area.CursorDown()
}

func (m Model) Value() string {
	return m.area.Value()
}

func (m *Model) InsertNewline() {
	m.area.InsertRune('\n')
	m.palette.Sync(m.area.Value())
}

// PaletteVisible reports whether the slash-command autocomplete is showing.
func (m Model) PaletteVisible() bool { return m.palette.Visible() }

// MovePalette moves the palette selection by delta.
func (m *Model) MovePalette(delta int) { m.palette.Move(delta) }

// PaletteSelected returns the highlighted command.
func (m Model) PaletteSelected() (commandpalette.Command, bool) {
	return m.palette.Selected()
}

// ClosePalette dismisses the palette until the prompt text changes again.
func (m *Model) ClosePalette() { m.palette.Close() }

// AcceptPalette closes the palette and returns the selected command as
// completed prompt text (including the trailing space).
func (m *Model) AcceptPalette() (string, bool) {
	cmd, ok := m.palette.Selected()
	if !ok {
		return "", false
	}
	m.palette.Close()
	return "/" + cmd.Name + " ", true
}

// Reset clears the bar and brings its height back down to the resting size.
func (m *Model) Reset() {
	m.area.Reset()
	m.area.SetHeight(visibleLines)
	m.palette.Sync("")
}

// SetValue replaces the bar's content and resizes it to fit what was set.
func (m *Model) SetValue(str string) {
	m.area.SetValue(str)
	m.palette.Sync(str)
}

func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok && !m.Focused() {
		return *m, nil
	}
	oldHeight := m.Height()
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	m.palette.Sync(m.area.Value())
	if m.Height() != oldHeight {
		if cmd == nil {
			cmd = func() tea.Msg { return HeightChangedMsg{} }
		} else {
			cmd = tea.Batch(cmd, func() tea.Msg { return HeightChangedMsg{} })
		}
	}
	return *m, cmd
}

func (m *Model) View() string {
	parts := make([]string, 0, 2)
	if pv := m.palette.View(); pv != "" {
		parts = append(parts, pv)
	}
	parts = append(parts, style.PromptStyle.Render(m.area.View()))
	return lipgloss.NewStyle().
		Margin(0, marginSide).
		Render(strings.Join(parts, "\n"))
}

func (m *Model) IsEmpty() bool {
	return len(m.area.Value()) == 0
}
