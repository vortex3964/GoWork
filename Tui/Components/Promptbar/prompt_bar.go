package promptbar

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Components/CommandPalette"
	"GoWork/Tui/Components/MentionMenu"
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

	// textDrag tracks a mouse drag over the prompt text. It is separate
	// from the textarea's keyboard state, so a click that doesn't move
	// leaves normal editing and cursor behaviour unchanged.
	textDrag promptTextDrag

	// mentions is the "@" autocomplete window for skills, MCP/LSP servers
	// and project paths. It renders the same way but is only enabled on the
	// code tab.
	mentions        mentionmenu.Model
	mentionsEnabled bool
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
	return Model{area: ta, palette: commandpalette.New(), mentions: mentionmenu.New(), mentionsEnabled: true}
}

// SetCommands installs the slash-command registry used by the palette.
func (m *Model) SetCommands(commands []commandpalette.Command) {
	m.palette.SetCommands(commands)
	m.palette.Sync(m.area.Value())
}

// Height returns the bar's full rendered height in terminal rows: the
// palette rows (when visible), the visible text rows, and the two border rows.
func (m Model) Height() int {
	return m.area.Height() + borderHeight + m.palette.Height() + m.mentions.Height()
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
	m.mentions.SetWidth(outerWidth - marginSide*2)
}

// SetMentionItems installs the current source snapshot for the "@" menu.
func (m *Model) SetMentionItems(items []mentionmenu.Item) {
	m.mentions.SetItems(items)
}

// SetMentionsEnabled turns the "@" menu on or off. The menu only belongs to
// the code tab, so the shell disables it before other tabs see the prompt.
func (m *Model) SetMentionsEnabled(on bool) {
	if m.mentionsEnabled == on {
		return
	}
	m.mentionsEnabled = on
	if !on {
		m.mentions.Close()
		return
	}
	m.syncMentions()
}

func (m *Model) Focus() tea.Cmd {
	return m.area.Focus()
}

func (m *Model) Blur() {
	m.area.Blur()
	m.palette.Sync("")
	m.mentions.Sync("", 0)
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
	m.syncMentions()
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

// MentionVisible reports whether the "@" autocomplete is showing.
func (m Model) MentionVisible() bool { return m.mentionsEnabled && m.mentions.Visible() }

// MoveMention moves the "@" menu selection by delta.
func (m *Model) MoveMention(delta int) { m.mentions.Move(delta) }

// MentionSelected returns the highlighted mention.
func (m Model) MentionSelected() (mentionmenu.Item, bool) { return m.mentions.Selected() }

// CloseMentions dismisses the "@" menu until the prompt text changes again.
func (m *Model) CloseMentions() { m.mentions.Close() }

// AcceptMention closes the "@" menu and replaces the active token with the
// selected mention (plus a trailing space), leaving the rest of the prompt
// intact. It never submits the prompt.
func (m *Model) AcceptMention() bool {
	if !m.mentionsEnabled {
		return false
	}
	// Re-evaluate against the live textarea cursor so completion replaces the
	// right token even if the cursor moved since the last key.
	m.syncMentions()
	item, start, end, ok := m.mentions.Selection()
	if !ok {
		return false
	}
	m.replaceRange(start, end, item.Mention()+" ")
	return true
}

// cursorOffset converts the textarea's line/column cursor into a rune offset
// into Value().
func (m Model) cursorOffset() int {
	value := m.area.Value()
	runes := []rune(value)
	line := m.area.Line()
	if line < 0 {
		line = 0
	}
	offset := 0
	lines := strings.Split(value, "\n")
	for i := 0; i < line && i < len(lines); i++ {
		offset += len([]rune(lines[i])) + 1
	}
	offset += m.area.Column()
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	return offset
}

// syncMentions refreshes the menu from the current prompt text and cursor.
func (m *Model) syncMentions() {
	if !m.mentionsEnabled {
		return
	}
	m.mentions.Sync(m.area.Value(), m.cursorOffset())
}

// replaceRange replaces [start,end) in the prompt with text and leaves the
// cursor right after the replacement.
func (m *Model) replaceRange(start, end int, text string) {
	runes := []rune(m.area.Value())
	if start < 0 {
		start = 0
	}
	if start > len(runes) {
		start = len(runes)
	}
	if end < start {
		end = start
	}
	if end > len(runes) {
		end = len(runes)
	}
	replacement := []rune(text)
	next := make([]rune, 0, len(runes)-(end-start)+len(replacement))
	next = append(next, runes[:start]...)
	next = append(next, replacement...)
	next = append(next, runes[end:]...)
	m.area.SetValue(string(next))
	m.setCursorOffset(start + len(replacement))
	m.palette.Sync(m.area.Value())
	m.syncMentions()
}

// setCursorOffset places the textarea cursor at a rune offset.
func (m *Model) setCursorOffset(offset int) {
	runes := []rune(m.area.Value())
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	line, col := 0, 0
	for i := 0; i < offset; i++ {
		if runes[i] == '\n' {
			line++
			col = 0
			continue
		}
		col++
	}
	m.area.CursorStart()
	for m.area.Line() > 0 {
		m.area.CursorUp()
	}
	for m.area.Line() < line {
		m.area.CursorDown()
	}
	m.area.SetCursorColumn(col)
}

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
	m.syncMentions()
}

// SetValue replaces the bar's content and resizes it to fit what was set.
func (m *Model) SetValue(str string) {
	m.area.SetValue(str)
	m.palette.Sync(str)
	m.syncMentions()
}

func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok && !m.Focused() {
		return *m, nil
	}
	oldHeight := m.Height()
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	m.palette.Sync(m.area.Value())
	m.syncMentions()
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
	parts := make([]string, 0, 3)
	if mv := m.mentions.View(); mv != "" {
		parts = append(parts, mv)
	}
	if pv := m.palette.View(); pv != "" {
		parts = append(parts, pv)
	}
	parts = append(parts, style.PromptStyle.Render(m.area.View()))
	view := lipgloss.NewStyle().
		Margin(0, marginSide).
		Render(strings.Join(parts, "\n"))
	return m.overlayTextDrag(view)
}

func (m *Model) IsEmpty() bool {
	return len(m.area.Value()) == 0
}
