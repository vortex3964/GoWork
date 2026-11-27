package messagearea

import (
	"strings"

	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

var (
	selectionStartStyle = lipgloss.NewStyle().Foreground(style.Info).Bold(true)
	selectionEndStyle   = lipgloss.NewStyle().Foreground(style.Primary).Bold(true)
)

// InCopyMode reports whether the message area is currently owning the
// keyboard for a copy selection.
func (m Model) InCopyMode() bool { return m.copyMode }

// HasSelection reports whether any message is selected.
func (m Model) HasSelection() bool {
	return m.selAnchor >= 0 && m.selAnchor < len(m.messages)
}

// Selection returns the inclusive message range currently selected.
func (m Model) Selection() (from, to int, ok bool) {
	if !m.HasSelection() {
		return 0, 0, false
	}
	from, to = m.selAnchor, m.selFocus
	if from > to {
		from, to = to, from
	}
	if from < 0 {
		from = 0
	}
	if to >= len(m.messages) {
		to = len(m.messages) - 1
	}
	return from, to, from <= to
}

// BeginCopy enters copy mode. With nothing selected yet it starts on the
// newest message, which is the common "copy the last answer" case. It
// reports false when there is nothing to copy.
func (m *Model) BeginCopy() bool {
	if len(m.messages) == 0 {
		return false
	}
	m.copyMode = true
	if !m.HasSelection() {
		m.selAnchor = len(m.messages) - 1
		m.selFocus = m.selAnchor
	}
	m.ensureSelectionVisible()
	return true
}

// EndCopy leaves copy mode and clears the selection.
func (m *Model) EndCopy() {
	m.copyMode = false
	m.selAnchor = -1
	m.selFocus = -1
}

// MoveSelection moves the focus one message up (-1) or down (+1). With
// extend it grows/shrinks the range from the fixed anchor; otherwise the
// selection collapses to a single message.
func (m *Model) MoveSelection(delta int, extend bool) bool {
	if len(m.messages) == 0 {
		return false
	}
	if !m.HasSelection() {
		m.selAnchor = len(m.messages) - 1
		m.selFocus = m.selAnchor
	}
	target := m.selFocus + delta
	if target < 0 {
		target = 0
	}
	if target >= len(m.messages) {
		target = len(m.messages) - 1
	}
	if extend {
		m.selFocus = target
	} else {
		m.selAnchor = target
		m.selFocus = target
	}
	m.ensureSelectionVisible()
	return true
}

// SelectAll selects every message in the area.
func (m *Model) SelectAll() bool {
	if len(m.messages) == 0 {
		return false
	}
	m.selAnchor = 0
	m.selFocus = len(m.messages) - 1
	m.ensureSelectionVisible()
	return true
}

// MessageText returns the copyable plain text of one message. AI/user text
// is the raw content (markdown source), and a tool row includes its call
// header plus full result so copying a trace keeps the useful output.
func (m Model) MessageText(index int) string {
	if index < 0 || index >= len(m.messages) {
		return ""
	}
	msg := m.messages[index]
	if !msg.isTool {
		return msg.content
	}
	head := strings.TrimSpace(msg.toolName + " " + msg.toolArgs)
	if msg.result == "" {
		return head
	}
	if head == "" {
		return msg.result
	}
	return head + "\n" + msg.result
}

// SelectedText returns the copyable text for every message in the selected
// range, separated by a blank line.
func (m Model) SelectedText() (string, bool) {
	from, to, ok := m.Selection()
	if !ok {
		return "", false
	}
	parts := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		if text := strings.TrimRight(m.MessageText(i), "\n"); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}

// HandleClick maps a left click inside the message band (x is the screen
// column, y is the row relative to the top of the band including its outer
// margin) to a message. extend keeps the existing anchor and moves the range
// focus, mirroring shift-click. It reports whether a message was hit.
func (m *Model) HandleClick(x, y int, extend bool) bool {
	line := m.contentLineAt(x, y)
	if line < 0 {
		return false
	}
	index := m.messageAtLine(line)
	if index < 0 {
		return false
	}
	if extend && m.HasSelection() {
		m.selFocus = index
	} else {
		m.selAnchor = index
		m.selFocus = index
	}
	m.copyMode = true
	m.ensureSelectionVisible()
	return true
}

// contentLineAt translates band-relative screen coordinates into a line in
// the viewport's content, or -1 when the click misses the text area.
func (m *Model) contentLineAt(x, y int) int {
	if m.vp.Width() <= 0 || m.vp.Height() <= 2 {
		return -1
	}
	if x < padSide || x >= padSide+m.vp.Width() {
		return -1
	}
	// The viewport's top/bottom border rows are not content.
	if y < 1 || y >= m.vp.Height()-1 {
		return -1
	}
	return m.vp.YOffset() + (y - 1)
}

// messageAtLine returns the message whose block contains line, or the last
// message above it when the line falls in the blank gap between blocks.
func (m Model) messageAtLine(line int) int {
	if line < 0 || len(m.messages) == 0 {
		return -1
	}
	starts := m.blockStartLines()
	index := -1
	for i, start := range starts {
		if line < start {
			break
		}
		index = i
	}
	return index
}

// MessageAtLine exposes the click mapping for tests and callers that already
// have a viewport content line.
func (m Model) MessageAtLine(line int) int { return m.messageAtLine(line) }

// blockStartLines returns the content line each rendered message block
// starts at. Blocks are joined by msgGap blank lines.
func (m Model) blockStartLines() []int {
	starts := make([]int, len(m.rendered))
	line := 0
	for i, block := range m.rendered {
		starts[i] = line
		line += strings.Count(block, "\n") + 1 + msgGap
	}
	return starts
}

// ensureSelectionVisible scrolls the viewport just enough to bring the
// selected block's first line into view.
func (m *Model) ensureSelectionVisible() {
	if !m.HasSelection() || len(m.rendered) == 0 {
		return
	}
	starts := m.blockStartLines()
	index := m.selFocus
	if index < 0 || index >= len(starts) {
		index = m.selAnchor
	}
	if index < 0 || index >= len(starts) {
		return
	}
	m.vp.EnsureVisible(starts[index], 0, 0)
}

// overlaySelection draws a small gutter marker in the viewport's left
// padding for the first and last message of the selected range. It lives in
// the padding so content wrapping and scrolling are untouched.
func (m Model) overlaySelection(view string) string {
	if !m.HasSelection() || m.vp.Width() <= 0 || m.vp.Height() <= 2 {
		return view
	}
	from, to, ok := m.Selection()
	if !ok {
		return view
	}
	starts := m.blockStartLines()
	lineY := func(index int) int {
		if index < 0 || index >= len(starts) {
			return -1
		}
		y := 1 + starts[index] - m.vp.YOffset()
		if y < 1 || y >= m.vp.Height()-1 {
			return -1
		}
		return y
	}

	x := padSide + 1
	layers := []*lipgloss.Layer{lipgloss.NewLayer(view).X(0).Y(0).Z(0)}
	if y := lineY(from); y >= 0 {
		layers = append(layers,
			lipgloss.NewLayer(selectionStartStyle.Render("▌")).X(x).Y(y).Z(1))
	}
	if to != from {
		if y := lineY(to); y >= 0 {
			layers = append(layers,
				lipgloss.NewLayer(selectionEndStyle.Render("▐")).X(x).Y(y).Z(2))
		}
	}
	return lipgloss.NewCompositor(layers...).Render()
}
