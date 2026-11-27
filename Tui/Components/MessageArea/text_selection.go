package messagearea

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"GoWork/Tui/Style"
)

// textPoint is a position in the viewport's logical content: line indexes the
// rendered line buffer and col is a display-cell offset from the line start.
type textPoint struct {
	line int
	col  int
}

type textDragState struct {
	active bool
	moved  bool
	anchor textPoint
	focus  textPoint
}

var textDragStyle = lipgloss.NewStyle().
	Background(style.Primary).
	Foreground(style.Background)

// TextDragActive reports whether a mouse drag is currently tracking a
// character selection in the message area.
func (m Model) TextDragActive() bool { return m.textDrag.active }

// TextDragMoved reports whether the pointer moved far enough that the drag is
// a text selection rather than a plain click.
func (m Model) TextDragMoved() bool { return m.textDrag.active && m.textDrag.moved }

// BeginTextDrag starts tracking a character selection at a band-relative
// screen position. A click that never moves is left to the existing
// whole-message selection handlers.
func (m *Model) BeginTextDrag(x, y int) bool {
	p, ok := m.textPointAt(x, y)
	if !ok {
		return false
	}
	m.textDrag = textDragState{active: true, anchor: p, focus: p}
	return true
}

// UpdateTextDrag moves the focus of the character selection. The first real
// movement hands the interaction over from whole-message copy mode to the
// free text drag.
func (m *Model) UpdateTextDrag(x, y int) bool {
	if !m.textDrag.active {
		return false
	}
	p, ok := m.textPointAt(x, y)
	if !ok {
		return false
	}
	if p != m.textDrag.focus {
		m.textDrag.moved = true
	}
	m.textDrag.focus = p
	if m.textDrag.moved {
		m.copyMode = false
		m.selAnchor = -1
		m.selFocus = -1
	}
	return true
}

// CancelTextDrag drops the drag without copying anything.
func (m *Model) CancelTextDrag() { m.textDrag = textDragState{} }

// EndTextDrag finishes the drag and returns the selected text. A click that
// never moved cancels the drag and reports false so normal click behaviour is
// untouched.
func (m *Model) EndTextDrag() (string, bool) {
	if !m.textDrag.active {
		return "", false
	}
	text, ok := m.TextDragText()
	moved := m.textDrag.moved
	m.textDrag = textDragState{}
	if !moved || !ok {
		return "", false
	}
	return text, true
}

// TextDragText returns the text currently covered by the drag selection.
func (m Model) TextDragText() (string, bool) {
	if !m.textDrag.active {
		return "", false
	}
	return m.textBetween(m.textDrag.anchor, m.textDrag.focus)
}

// textPointAt maps a band-relative screen position to a logical content line
// and display column. Positions outside the text area clamp to its edges so a
// drag can continue past the viewport while selecting.
func (m *Model) textPointAt(x, y int) (textPoint, bool) {
	width := m.vp.contentWidth()
	height := m.vp.contentHeight()
	if width <= 0 || height <= 0 || len(m.vp.lines) == 0 {
		return textPoint{}, false
	}

	originX := padSide + m.vp.Style.GetBorderLeftSize() + m.vp.Style.GetPaddingLeft()
	originY := m.vp.Style.GetBorderTopSize() + m.vp.Style.GetPaddingTop()

	row := y - originY
	if row < 0 {
		row = 0
	}
	if row >= height {
		row = height - 1
	}
	globalRow := m.vp.YOffset() + row
	line := m.vp.LineAtRow(globalRow)
	if line < 0 {
		return textPoint{}, false
	}

	col := (globalRow-m.vp.RowAtLine(line))*width + (x - originX)
	if col < 0 {
		col = 0
	}
	return textPoint{line: line, col: col}, true
}

// textBetween extracts the display text between two content positions. It is
// used for both the live preview and the final clipboard contents.
func (m Model) textBetween(a, b textPoint) (string, bool) {
	if len(m.vp.lines) == 0 {
		return "", false
	}
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		a, b = b, a
	}
	a.line = clampInt(a.line, 0, len(m.vp.lines)-1)
	b.line = clampInt(b.line, 0, len(m.vp.lines)-1)

	if a.line == b.line {
		line := plainViewportLine(m.vp.lines[a.line])
		start, end := clampCols(line, a.col, b.col)
		text := strings.TrimRight(ansi.Cut(line, start, end), " \t")
		return text, text != ""
	}

	var out strings.Builder
	for line := a.line; line <= b.line; line++ {
		text := plainViewportLine(m.vp.lines[line])
		width := ansi.StringWidth(text)
		start, end := 0, width
		if line == a.line {
			start = clampInt(a.col, 0, width)
		}
		if line == b.line {
			end = clampInt(b.col, 0, width)
		}
		if end < start {
			end = start
		}
		out.WriteString(strings.TrimRight(ansi.Cut(text, start, end), " \t"))
		if line < b.line {
			out.WriteByte('\n')
		}
	}
	text := strings.TrimRight(out.String(), "\n")
	return text, text != ""
}

// overlayTextDrag paints the active drag selection over the rendered view.
// The highlight is drawn as an opaque layer containing the same characters,
// which keeps underlying wrapping and syntax colours untouched outside the
// selection.
func (m Model) overlayTextDrag(view string) string {
	if !m.textDrag.active || !m.textDrag.moved {
		return view
	}
	width := m.vp.contentWidth()
	if width <= 0 || len(m.vp.lines) == 0 {
		return view
	}

	a, b := m.textDrag.anchor, m.textDrag.focus
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		a, b = b, a
	}

	originX := padSide + m.vp.Style.GetBorderLeftSize() + m.vp.Style.GetPaddingLeft()
	originY := m.vp.Style.GetBorderTopSize() + m.vp.Style.GetPaddingTop()

	layers := []*lipgloss.Layer{lipgloss.NewLayer(view).X(0).Y(0).Z(0)}
	height := m.vp.contentHeight()
	for row := 0; row < height; row++ {
		globalRow := m.vp.YOffset() + row
		line := m.vp.LineAtRow(globalRow)
		if line < a.line || line > b.line {
			continue
		}

		text := plainViewportLine(m.vp.lines[line])
		lineWidth := ansi.StringWidth(text)
		chunkStart := (globalRow - m.vp.RowAtLine(line)) * width

		start := chunkStart
		if line == a.line && a.col > start {
			start = a.col
		}
		end := chunkStart + width
		if end > lineWidth {
			end = lineWidth
		}
		if line == b.line && b.col < end {
			end = b.col
		}
		if start < chunkStart {
			start = chunkStart
		}
		if end < start {
			end = start
		}
		if start >= end {
			continue
		}

		segment := ansi.Cut(text, start, end)
		if segment == "" {
			continue
		}
		layers = append(layers, lipgloss.NewLayer(textDragStyle.Render(segment)).
			X(originX+start-chunkStart).
			Y(originY+row).
			Z(1))
	}
	return lipgloss.NewCompositor(layers...).Render()
}

// plainViewportLine strips styling from one logical viewport line so
// selections copy human-readable text rather than escape sequences.
func plainViewportLine(line string) string { return ansi.Strip(line) }

func clampCols(line string, a, b int) (int, int) {
	width := ansi.StringWidth(line)
	a = clampInt(a, 0, width)
	b = clampInt(b, 0, width)
	if a > b {
		a, b = b, a
	}
	return a, b
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
