package promptbar

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

const visibleLines = 2

const (
	borderWidth  = 2 
	paddingWidth = 2
	marginSide   = 3 // leaves x cells of space on the left and right
)

const Height = visibleLines + 2

type Model struct {
	area textarea.Model
}

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
	ta.SetHeight(visibleLines)
	ta.SetWidth(20) 
	ta.Blur()        
	return Model{area: ta}
}

func (m *Model) SetWidth(outerWidth int) {
	// Subtract the margin from both sides to constrain the inner textarea
	inner := outerWidth - borderWidth - paddingWidth - (marginSide * 2)
	if inner < 1 {
		inner = 1
	}
	m.area.SetWidth(inner)
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

func (m *Model) Reset() {
	m.area.Reset()
}

func (m *Model) InsertNewline() {
	m.area.InsertRune('\n')
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok && !m.Focused() {
		return m, nil
	}
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	// Apply the margin to the outside of the prompt style
	return lipgloss.NewStyle().
		Margin(0, marginSide).
		Render(style.PromptStyle.Render(m.area.View()))
}
