package promptbar

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	// lastHeight is the bar height (text rows) from the previous frame, used
	// to detect growth/shrink and notify the shell so it can re-layout.
	lastHeight int
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
	ta.Placeholder = "enter a prompt ..."
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
	return Model{area: ta, lastHeight: visibleLines}
}

// Height returns the bar's full rendered height in terminal rows: the
// visible text rows (textarea height, grows up to maxVisibleLines) plus the
// two border rows.
func (m Model) Height() int {
	return m.area.Height() + borderHeight
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
	// The wrap width can change how many rows the content needs; keep the
	// height tracker in sync.
	m.lastHeight = m.area.Height()
}

func (m *Model) Focus() tea.Cmd {
	return m.area.Focus()
}

func (m *Model) Blur() {
	m.area.Blur()
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
}

// Reset clears the bar and brings its height back down to the resting size.
func (m *Model) Reset() {
	m.area.Reset()
	m.area.SetHeight(visibleLines)
	m.lastHeight = visibleLines
}

// SetValue replaces the bar's content and resizes it to fit what was set.
func (m *Model) SetValue(str string) {
	m.area.SetValue(str)
	m.lastHeight = m.area.Height()
}

func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok && !m.Focused() {
		return *m, nil
	}
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	if h := m.area.Height(); h != m.lastHeight {
		m.lastHeight = h
		if cmd == nil {
			cmd = func() tea.Msg { return HeightChangedMsg{} }
		} else {
			cmd = tea.Batch(cmd, func() tea.Msg { return HeightChangedMsg{} })
		}
	}
	return *m, cmd
}

func (m *Model) View() string {
	// Apply the margin to the outside of the prompt style
	return lipgloss.NewStyle().
		Margin(0, marginSide).
		Render(style.PromptStyle.Render(m.area.View()))
}

func (m *Model) IsEmpty() bool {
	return len(m.area.Value()) == 0
}
