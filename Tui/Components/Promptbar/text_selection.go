package promptbar

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"

	"GoWork/Tui/Style"
)

// promptPoint is a position in the prompt text: line indexes the logical
// newline-separated lines and col is a rune offset inside that line.
type promptPoint struct {
	line int
	col  int
}

type promptTextDrag struct {
	active bool
	moved  bool
	anchor promptPoint
	focus  promptPoint
}

// promptRow is one visual (soft-wrapped) row of the prompt text. start and end
// are rune offsets into the logical line it belongs to.
type promptRow struct {
	line  int
	start int
	end   int
}

var textDragStyle = lipgloss.NewStyle().
	Background(style.Primary).
	Foreground(style.Background)

// TextDragActive reports whether a mouse drag is currently selecting prompt
// text.
func (m Model) TextDragActive() bool { return m.textDrag.active }

// TextDragMoved reports whether the pointer has moved since the drag began.
func (m Model) TextDragMoved() bool { return m.textDrag.active && m.textDrag.moved }

// BeginTextDrag starts tracking a drag at a prompt-local screen position.
// y is relative to the top of the prompt component (palette included). A
// press outside the text rows is rejected so palette and border clicks keep
// their normal behaviour.
func (m *Model) BeginTextDrag(x, y int) bool {
	p, ok := m.textPointAt(x, y, false)
	if !ok {
		return false
	}
	m.textDrag = promptTextDrag{active: true, anchor: p, focus: p}
	return true
}

// UpdateTextDrag moves the focus of the prompt selection. Once a drag is
// active, positions outside the text rows clamp to the nearest visible row so
// the selection still extends when the pointer leaves the box.
func (m *Model) UpdateTextDrag(x, y int) bool {
	if !m.textDrag.active {
		return false
	}
	p, ok := m.textPointAt(x, y, true)
	if !ok {
		return false
	}
	if p != m.textDrag.focus {
		m.textDrag.moved = true
	}
	m.textDrag.focus = p
	return true
}

// CancelTextDrag drops the drag without copying anything.
func (m *Model) CancelTextDrag() { m.textDrag = promptTextDrag{} }

// EndTextDrag finishes the drag and returns the selected prompt text. A click
// without movement reports false so it only keeps its normal focus behaviour.
func (m *Model) EndTextDrag() (string, bool) {
	if !m.textDrag.active {
		return "", false
	}
	text, ok := m.TextDragText()
	moved := m.textDrag.moved
	m.textDrag = promptTextDrag{}
	if !moved || !ok {
		return "", false
	}
	return text, true
}

// TextDragText returns the prompt text currently covered by the drag.
func (m Model) TextDragText() (string, bool) {
	if !m.textDrag.active || !m.textDrag.moved {
		return "", false
	}
	a, b := m.dragRange()
	return m.textBetween(a, b)
}

// dragRange normalises the drag endpoints and includes the cell under the
// pointer, matching normal terminal text selection.
func (m Model) dragRange() (promptPoint, promptPoint) {
	a, b := m.textDrag.anchor, m.textDrag.focus
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		a, b = b, a
	}
	b.col++
	return a, b
}

// textPointAt maps a prompt-local screen position to a logical line/rune
// position. When clamp is true, rows outside the text area snap to the first
// or last visible row; otherwise they are rejected.
func (m *Model) textPointAt(x, y int, clamp bool) (promptPoint, bool) {
	value := m.area.Value()
	if value == "" {
		return promptPoint{}, false
	}
	width := m.area.Width()
	if width <= 0 {
		return promptPoint{}, false
	}

	top := m.palette.Height() + style.PromptStyle.GetBorderTopSize()
	row := y - top
	if row < 0 || row >= m.area.Height() {
		if !clamp {
			return promptPoint{}, false
		}
		if row < 0 {
			row = 0
		} else {
			row = m.area.Height() - 1
		}
	}
	visual := m.area.ScrollYOffset() + row
	rows := promptVisualRows(value, width)
	if len(rows) == 0 {
		return promptPoint{}, false
	}
	if visual < 0 {
		visual = 0
	}
	if visual >= len(rows) {
		visual = len(rows) - 1
	}

	r := rows[visual]
	lines := strings.Split(value, "\n")
	if r.line < 0 || r.line >= len(lines) {
		return promptPoint{}, false
	}
	runes := []rune(lines[r.line])
	start := clampInt(r.start, 0, len(runes))
	end := clampInt(r.end, start, len(runes))

	originX := marginSide + style.PromptStyle.GetBorderLeftSize() + style.PromptStyle.GetPaddingLeft()
	cell := x - originX
	if cell < 0 {
		cell = 0
	}
	col := start
	used := 0
	for i, rn := range runes[start:end] {
		w := uniseg.StringWidth(string(rn))
		if used+w > cell {
			col = start + i
			break
		}
		used += w
		col = start + i + 1
	}
	return promptPoint{line: r.line, col: col}, true
}

// textBetween extracts the prompt text between two logical positions.
func (m Model) textBetween(a, b promptPoint) (string, bool) {
	value := m.area.Value()
	if value == "" {
		return "", false
	}
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		a, b = b, a
	}
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return "", false
	}
	a.line = clampInt(a.line, 0, len(lines)-1)
	b.line = clampInt(b.line, 0, len(lines)-1)

	if a.line == b.line {
		runes := []rune(lines[a.line])
		start := clampInt(a.col, 0, len(runes))
		end := clampInt(b.col, 0, len(runes))
		if start > end {
			start, end = end, start
		}
		if start == end {
			return "", false
		}
		return string(runes[start:end]), true
	}

	var out strings.Builder
	for line := a.line; line <= b.line; line++ {
		runes := []rune(lines[line])
		start, end := 0, len(runes)
		if line == a.line {
			start = clampInt(a.col, 0, len(runes))
		}
		if line == b.line {
			end = clampInt(b.col, 0, len(runes))
		}
		if end < start {
			end = start
		}
		out.WriteString(string(runes[start:end]))
		if line < b.line {
			out.WriteByte('\n')
		}
	}
	text := out.String()
	return text, text != ""
}

// overlayTextDrag paints the selected prompt characters over the rendered
// view. The textarea remains the owner of the underlying text and cursor.
func (m Model) overlayTextDrag(view string) string {
	if !m.textDrag.active || !m.textDrag.moved {
		return view
	}
	value := m.area.Value()
	width := m.area.Width()
	if value == "" || width <= 0 {
		return view
	}
	rows := promptVisualRows(value, width)
	if len(rows) == 0 {
		return view
	}
	lines := strings.Split(value, "\n")

	a, b := m.dragRange()

	originX := marginSide + style.PromptStyle.GetBorderLeftSize() + style.PromptStyle.GetPaddingLeft()
	originY := m.palette.Height() + style.PromptStyle.GetBorderTopSize()
	scroll := m.area.ScrollYOffset()
	height := m.area.Height()

	layers := []*lipgloss.Layer{lipgloss.NewLayer(view).X(0).Y(0).Z(0)}
	for visual := scroll; visual < scroll+height && visual < len(rows); visual++ {
		r := rows[visual]
		if r.line < a.line || r.line > b.line || r.line >= len(lines) {
			continue
		}
		runes := []rune(lines[r.line])
		start := clampInt(r.start, 0, len(runes))
		end := clampInt(r.end, start, len(runes))
		if r.line == a.line && a.col > start {
			start = clampInt(a.col, start, end)
		}
		if r.line == b.line && b.col < end {
			end = clampInt(b.col, start, end)
		}
		if start >= end {
			continue
		}

		offset := uniseg.StringWidth(string(runes[clampInt(r.start, 0, start):start]))
		text := string(runes[start:end])
		if text == "" {
			continue
		}
		layers = append(layers, lipgloss.NewLayer(textDragStyle.Render(text)).
			X(originX+offset).
			Y(originY+visual-scroll).
			Z(1))
	}
	return lipgloss.NewCompositor(layers...).Render()
}

// promptVisualRows maps each logical prompt line to the soft-wrapped rows the
// textarea renders. The wrapping rules mirror bubbles' textarea so mouse
// positions line up with what is on screen, including wrapped words and wide
// runes.
func promptVisualRows(value string, width int) []promptRow {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(value, "\n")
	rows := make([]promptRow, 0, len(lines))
	for line, text := range lines {
		for _, r := range wrapPromptLine([]rune(text), width) {
			r.line = line
			rows = append(rows, r)
		}
	}
	return rows
}

func wrapPromptLine(runes []rune, width int) []promptRow {
	type mappedRow struct {
		runes []rune
		start int
		end   int
	}
	mapped := []mappedRow{{start: -1, end: -1}}
	row := 0
	addOriginal := func(idx, start, end int) {
		if start < 0 {
			start = end
		}
		if mapped[idx].start < 0 {
			mapped[idx].start = start
		}
		mapped[idx].end = end
	}

	var (
		word       []rune
		wordStart  = -1
		spaces     int
		spacesFrom = -1
	)

	for i, r := range runes {
		if unicode.IsSpace(r) {
			if spaces == 0 {
				spacesFrom = i
			}
			spaces++
		} else {
			if len(word) == 0 {
				wordStart = i
			}
			word = append(word, r)
		}

		if spaces > 0 {
			chunkStart := wordStart
			if chunkStart < 0 {
				chunkStart = spacesFrom
			}
			if uniseg.StringWidth(string(mapped[row].runes))+
				uniseg.StringWidth(string(word))+spaces > width {
				row++
				mapped = append(mapped, mappedRow{start: -1, end: -1})
			}
			mapped[row].runes = append(mapped[row].runes, word...)
			mapped[row].runes = append(mapped[row].runes, repeatSpaces(spaces)...)
			addOriginal(row, chunkStart, i+1)
			word = nil
			wordStart = -1
			spaces = 0
			spacesFrom = -1
		} else {
			// Mirror bubbles' textarea: only the accumulated word is
			// measured here. The current row's width is deliberately not
			// included; textarea starts a new row only once the word
			// itself would overflow.
			lastCharLen := uniseg.StringWidth(string(r))
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				if len(mapped[row].runes) > 0 {
					row++
					mapped = append(mapped, mappedRow{start: -1, end: -1})
				}
				chunkStart := wordStart
				if chunkStart < 0 {
					chunkStart = i
				}
				mapped[row].runes = append(mapped[row].runes, word...)
				addOriginal(row, chunkStart, i+1)
				word = nil
				wordStart = -1
			}
		}
	}

	chunkStart := wordStart
	if chunkStart < 0 {
		chunkStart = spacesFrom
	}
	if chunkStart < 0 {
		chunkStart = len(runes)
	}
	if uniseg.StringWidth(string(mapped[row].runes))+
		uniseg.StringWidth(string(word))+spaces >= width {
		mapped = append(mapped, mappedRow{start: -1, end: -1})
		row++
		mapped[row].runes = append(mapped[row].runes, word...)
		mapped[row].runes = append(mapped[row].runes, repeatSpaces(spaces+1)...)
	} else {
		mapped[row].runes = append(mapped[row].runes, word...)
		mapped[row].runes = append(mapped[row].runes, repeatSpaces(spaces)...)
		mapped[row].runes = append(mapped[row].runes, ' ')
	}
	addOriginal(row, chunkStart, len(runes))

	rows := make([]promptRow, len(mapped))
	for i, r := range mapped {
		start := r.start
		if start < 0 {
			start = 0
		}
		end := r.end
		if end < start {
			end = start
		}
		rows[i] = promptRow{start: start, end: end}
	}
	return rows
}

func repeatSpaces(n int) []rune {
	if n <= 0 {
		return nil
	}
	return []rune(strings.Repeat(" ", n))
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
