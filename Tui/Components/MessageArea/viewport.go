package messagearea

import (
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// contentViewport is a small viewport tailored to a message log that grows at
// the tail. The bubbles viewport's SetContent re-splits and re-measures the
// whole string on every call, so pushing the conversation on each streamed
// token rescans every prior message. This viewport keeps the content as
// logical lines plus a cached wrapped-row count per line; replacing the live
// block only revisits the lines from that block onwards.
type contentViewport struct {
	width  int
	height int

	// Style carries the border/padding, exactly like the bubbles viewport's
	// Style field used before.
	Style lipgloss.Style

	// lines is the whole content, one logical line per entry. rows caches how
	// many visual rows each line occupies at the current width, and starts[i]
	// is the first visual row of logical line i (starts has len(lines)+1
	// entries; the last one is the total row count).
	lines  []string
	rows   []int
	starts []int

	yOffset         int
	mouseWheelDelta int

	// lineMeasures counts logical lines whose row count was computed. Tests
	// and benchmarks use it to prove a streamed delta does not rescan the
	// whole history.
	lineMeasures int
}

func newContentViewport(style lipgloss.Style) contentViewport {
	return contentViewport{
		Style:           style,
		starts:          []int{0},
		mouseWheelDelta: 3,
	}
}

func (v *contentViewport) Width() int  { return v.width }
func (v *contentViewport) Height() int { return v.height }

// SetWidth changes the viewport's outer width. Wrapped-row counts depend on
// the usable content width, so they are recomputed (a resize is rare compared
// with streamed deltas).
func (v *contentViewport) SetWidth(w int) {
	if w == v.width {
		return
	}
	v.width = w
	v.recomputeRows(0)
	v.clampOffset()
}

func (v *contentViewport) SetHeight(h int) {
	v.height = h
	v.clampOffset()
}

// SetLines replaces the entire content. It is used when the layout rebuilds
// every block, e.g. after a resize.
func (v *contentViewport) SetLines(lines []string) {
	v.ReplaceLines(0, len(v.lines), lines)
}

// AppendLines adds lines at the end of the content.
func (v *contentViewport) AppendLines(lines []string) {
	v.ReplaceLines(len(v.lines), len(v.lines), lines)
}

// ReplaceLines swaps the logical lines in [start,end) for lines and updates
// only the row bookkeeping that can have changed. Appending (start==end at
// the end) or replacing the tail therefore costs O(len(lines)), not
// O(history).
func (v *contentViewport) ReplaceLines(start, end int, lines []string) {
	if start < 0 {
		start = 0
	}
	if start > len(v.lines) {
		start = len(v.lines)
	}
	if end < start {
		end = start
	}
	if end > len(v.lines) {
		end = len(v.lines)
	}

	newRows := make([]int, len(lines))
	for i, line := range lines {
		newRows[i] = v.rowsForLine(line)
	}
	v.lineMeasures += len(lines)

	v.lines = slices.Replace(v.lines, start, end, lines...)
	v.rows = slices.Replace(v.rows, start, end, newRows...)

	// Row starts before `start` are untouched; every following line shifts by
	// the same amount, so only the tail has to be re-accumulated.
	if cap(v.starts) < len(v.lines)+1 {
		starts := make([]int, len(v.starts), len(v.lines)+1)
		copy(starts, v.starts)
		v.starts = starts
	}
	v.starts = v.starts[:start+1]
	for i := start; i < len(v.lines); i++ {
		v.starts = append(v.starts, v.starts[i]+v.rows[i])
	}
	v.clampOffset()
}

// recomputeRows recalculates wrapped-row counts from line `from` onwards. It
// is only needed when the usable width changes.
func (v *contentViewport) recomputeRows(from int) {
	if from < 0 {
		from = 0
	}
	if from > len(v.lines) {
		from = len(v.lines)
	}
	if len(v.rows) < len(v.lines) {
		rows := make([]int, len(v.lines))
		copy(rows, v.rows)
		v.rows = rows
	} else {
		v.rows = v.rows[:len(v.lines)]
	}
	for i := from; i < len(v.lines); i++ {
		v.rows[i] = v.rowsForLine(v.lines[i])
	}
	v.lineMeasures += len(v.lines) - from

	if cap(v.starts) < len(v.lines)+1 {
		starts := make([]int, len(v.starts), len(v.lines)+1)
		copy(starts, v.starts)
		v.starts = starts
	}
	v.starts = v.starts[:from+1]
	for i := from; i < len(v.lines); i++ {
		v.starts = append(v.starts, v.starts[i]+v.rows[i])
	}
}

// rowsForLine mirrors how the bubbles viewport computes a line's height for
// soft-wrapped content: the ceiling of its display width over the usable
// content width, with empty lines still taking one row.
func (v *contentViewport) rowsForLine(line string) int {
	width := v.contentWidth()
	if width <= 0 {
		return 1
	}
	lineWidth := ansi.StringWidth(line)
	if lineWidth <= width {
		return 1
	}
	return (lineWidth + width - 1) / width
}

// contentWidth is the width available to content inside the border/padding.
func (v *contentViewport) contentWidth() int {
	w := v.width - v.Style.GetHorizontalFrameSize()
	if w < 1 {
		return 0
	}
	return w
}

// contentHeight is the number of rows available inside the border/padding.
func (v *contentViewport) contentHeight() int {
	h := v.height - v.Style.GetVerticalFrameSize()
	if h < 0 {
		return 0
	}
	return h
}

func (v *contentViewport) totalRows() int {
	if len(v.starts) == 0 {
		return 0
	}
	return v.starts[len(v.lines)]
}

func (v *contentViewport) maxYOffset() int {
	maxOffset := v.totalRows() - v.contentHeight()
	if maxOffset < 0 {
		return 0
	}
	return maxOffset
}

func (v *contentViewport) clampOffset() {
	if v.yOffset < 0 {
		v.yOffset = 0
	}
	if max := v.maxYOffset(); v.yOffset > max {
		v.yOffset = max
	}
}

func (v *contentViewport) YOffset() int { return v.yOffset }

func (v *contentViewport) SetYOffset(n int) {
	v.yOffset = n
	v.clampOffset()
}

func (v *contentViewport) AtTop() bool    { return v.yOffset <= 0 }
func (v *contentViewport) AtBottom() bool { return v.yOffset >= v.maxYOffset() }

func (v *contentViewport) GotoBottom() { v.SetYOffset(v.maxYOffset()) }
func (v *contentViewport) GotoTop()    { v.SetYOffset(0) }

func (v *contentViewport) ScrollUp(n int) {
	if v.AtTop() || n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.yOffset - n)
}

func (v *contentViewport) ScrollDown(n int) {
	if v.AtBottom() || n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.yOffset + n)
}

func (v *contentViewport) PageDown()     { v.ScrollDown(v.contentHeight()) }
func (v *contentViewport) PageUp()       { v.ScrollUp(v.contentHeight()) }
func (v *contentViewport) HalfPageDown() { v.ScrollDown(v.contentHeight() / 2) }
func (v *contentViewport) HalfPageUp()   { v.ScrollUp(v.contentHeight() / 2) }

// EnsureVisible scrolls just enough to bring the given visual row into view.
func (v *contentViewport) EnsureVisible(row int) {
	if row < v.yOffset || row >= v.yOffset+v.contentHeight() {
		v.SetYOffset(row)
	}
}

// LineAtRow returns the logical line containing a visual row, or -1 when the
// row is outside the content.
func (v *contentViewport) LineAtRow(row int) int {
	if row < 0 || len(v.lines) == 0 {
		return -1
	}
	i := sort.Search(len(v.lines), func(i int) bool { return v.starts[i+1] > row })
	if i >= len(v.lines) {
		return -1
	}
	return i
}

// RowAtLine returns the visual row a logical line starts at. Out-of-range
// lines clamp to the first/last line.
func (v *contentViewport) RowAtLine(line int) int {
	if len(v.lines) == 0 {
		return 0
	}
	if line < 0 {
		line = 0
	}
	if line >= len(v.lines) {
		line = len(v.lines) - 1
	}
	return v.starts[line]
}

// visibleLines renders the rows currently in view, soft-wrapping any line
// wider than the usable content width exactly like the bubbles viewport did.
func (v *contentViewport) visibleLines() []string {
	maxHeight := v.contentHeight()
	maxWidth := v.contentWidth()
	if maxHeight <= 0 || maxWidth <= 0 || len(v.lines) == 0 {
		return nil
	}

	first := v.LineAtRow(v.yOffset)
	if first < 0 {
		return nil
	}
	vOffset := v.yOffset - v.starts[first]

	out := make([]string, 0, maxHeight)
	for i := first; i < len(v.lines) && len(out) < maxHeight; i++ {
		line := v.lines[i]
		lineWidth := ansi.StringWidth(line)
		if lineWidth <= maxWidth {
			out = append(out, line)
			continue
		}
		start := 0
		if i == first {
			start = vOffset * maxWidth
		}
		for col := start; col < lineWidth && len(out) < maxHeight; col += maxWidth {
			out = append(out, ansi.Cut(line, col, col+maxWidth))
		}
	}
	return out
}

// View renders the visible window with the border/padding style, matching the
// bubbles viewport's rendering contract.
func (v *contentViewport) View() string {
	w, h := v.width, v.height
	if sw := v.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := v.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}
	if w == 0 || h == 0 {
		return ""
	}

	contents := lipgloss.NewStyle().
		Width(w - v.Style.GetHorizontalFrameSize()).
		Height(h - v.Style.GetVerticalFrameSize()).
		Render(strings.Join(v.visibleLines(), "\n"))
	return v.Style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// messageAreaKeys mirrors the pager-style bindings the bubbles viewport used,
// so keys the app forwards to the message area keep scrolling it.
type messageAreaKeys struct {
	pageDown     key.Binding
	pageUp       key.Binding
	halfPageUp   key.Binding
	halfPageDown key.Binding
	up           key.Binding
	down         key.Binding
}

var msgAreaKeys = messageAreaKeys{
	pageDown:     key.NewBinding(key.WithKeys("pgdown", "space", "f")),
	pageUp:       key.NewBinding(key.WithKeys("pgup", "b")),
	halfPageUp:   key.NewBinding(key.WithKeys("u", "ctrl+u")),
	halfPageDown: key.NewBinding(key.WithKeys("d", "ctrl+d")),
	up:           key.NewBinding(key.WithKeys("up", "k")),
	down:         key.NewBinding(key.WithKeys("down", "j")),
}

// Update applies the same key/mouse scrolling the bubbles viewport provided.
// There is no command to return; the signature keeps MessageArea.Update
// compatible.
func (v *contentViewport) Update(msg tea.Msg) {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(m, msgAreaKeys.pageDown):
			v.PageDown()
		case key.Matches(m, msgAreaKeys.pageUp):
			v.PageUp()
		case key.Matches(m, msgAreaKeys.halfPageDown):
			v.HalfPageDown()
		case key.Matches(m, msgAreaKeys.halfPageUp):
			v.HalfPageUp()
		case key.Matches(m, msgAreaKeys.down):
			v.ScrollDown(1)
		case key.Matches(m, msgAreaKeys.up):
			v.ScrollUp(1)
		}
	case tea.MouseWheelMsg:
		switch m.Button {
		case tea.MouseWheelUp:
			v.ScrollUp(v.mouseWheelDelta)
		case tea.MouseWheelDown:
			v.ScrollDown(v.mouseWheelDelta)
		}
	}
}
