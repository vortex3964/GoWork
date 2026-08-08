// Package stats is GoWork's usage dashboard. It turns the rows the Db
// package aggregates into two panels: a stacked bar chart of daily token
// usage over a 30-day window (one bar per day, segments colored per model,
// browsable with ←/→), and a scrollable, searchable list of every model
// with its aggregate numbers. All border chrome is drawn in the info color
// and the whole screen is sized with the same outer padding as the message
// area. In stats mode the prompt bar is gone and the statusline is pinned
// below.
package stats

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Db"
	"GoWork/Tui/Style"
)

const (
	padSide = 3
	botPad  = 1
	gap     = 1

	// windowDays is the number of days shown in the chart at once.
	windowDays = 30

	// dayColW is one day-bar's column width (bar glyphs are single-width so
	// rune counting == column counting). dayGap is the blank column between
	// bars.
	dayColW = 3
	dayGap  = 1

	// all glyphs below are single-width (ASCII + block chars) so rune
	// counting == column counting.
	selCol  = 1 // "▸" selection marker
	nameCol = 18
	provCol = 9
	pCol    = 9 // prompt tokens
	cCol    = 9 // completion tokens
	tCol    = 9 // total tokens
	callCol = 6
	toolCol = 6
	lastCol = 10 // "MM-DD"
)

// group is one model's slot in the stable color assignment: the top models
// by all-time total tokens (over the loaded stats), plus an "others"
// bucket for the rest. Every day-bar's segments and the list's row accents
// both key off this same assignment so colors mean the same thing in both
// panels.
type group struct {
	name  string
	color color.Color
}

// ModelDetail is the Enter-to-magnify popup for one model.
type ModelDetail struct {
	model   db.ModelStat
	index   int // position in the full (unfiltered) list for ←/→ browsing
	pct     float64
	termW   int
	termH   int
	vScroll int
}

// dayBar is one prepared column for the chart: the calendar day, its
// total, its per-model segments in stacked (bottom-to-top) draw order, and
// the day's tool-call activity. A day with no usage at all has
// hasData=false and renders as a blank column, distinct from a day that
// legitimately totalled zero.
type dayBar struct {
	day        string // "2006-01-02"
	label      string // "Aug 08" for the header/detail
	total      int
	tokens     map[string]int // per-model tokens, for the detail line
	toolCalls  int
	modelTools map[string]int // tool calls per model, for the detail line
	hasData    bool
	segments   []segment
}

type segment struct {
	color color.Color
	share float64 // 0..1 of this day's total
}

// Model is the stats screen: data, list selection/filter/scroll, the
// chart's day-window offset, and an optional magnified overlay.
type Model struct {
	width  int
	height int

	stats  []db.ModelStat
	daily  []db.DailyBucket
	total  int
	groups []group

	// dayOffset shifts the 30-day chart window into the past: 0 means the
	// window ends today (the newest possible window); larger values slide
	// it earlier. Clamped so the window can never run past "today" on the
	// right and never past the oldest day we have data for on the left.
	dayOffset int

	// dayCursor is the day inside the current 30-day window that's
	// selected (highlighted column + detail line). A selected day near
	// either edge makes the arrows page the window one day at a time, so
	// the highlight can walk the whole recorded history.
	dayCursor int

	cursor      int
	items       []db.ModelStat // visible after filtering
	filtering   bool
	filterInput textinput.Model

	modal *ModelDetail
}

func New() Model {
	ti := textinput.New()
	ti.Placeholder = "filter model..."
	ti.Prompt = "/ "
	ti.CharLimit = 64
	ti.SetWidth(30)
	return Model{filterInput: ti}
}

// SetData hands the screen a fresh set of aggregates (on tab entry and
// after every recorded generation) and recomputes everything derived from
// them. rows is the per-model 30-day aggregate (for the list + legend
// coloring); daily is the day-bucketed breakdown the chart draws from.
func (m *Model) SetData(rows []db.ModelStat, daily []db.DailyBucket) {
	m.stats = rows
	m.daily = daily
	m.total = 0
	for _, r := range rows {
		m.total += r.TotalTokens
	}
	m.groups = buildGroups(rows)
	m.applyFilter()
	m.clampAll()
}

// buildGroups makes the stable model→color assignment shared by the chart
// and the list: the top 5 models by all-time total get their own color,
// everything else shares a single "others" color.
func buildGroups(stats []db.ModelStat) []group {
	if len(stats) == 0 {
		return nil
	}
	top := stats
	hasOthers := false
	if len(stats) > 5 {
		top = stats[:5]
		hasOthers = true
	}
	g := make([]group, 0, len(top)+1)
	for i, r := range top {
		g = append(g, group{
			name:  r.Model,
			color: style.GraphColors[i%len(style.GraphColors)],
		})
	}
	if hasOthers {
		g = append(g, group{
			name:  "others",
			color: style.GraphColors[len(style.GraphColors)-1],
		})
	}
	return g
}

// colorFor returns a model's chart/list color, or the shared "others"
// color when it isn't one of the top 5.
func (m Model) colorFor(model string) color.Color {
	for _, g := range m.groups {
		if g.name == model {
			return g.color
		}
	}
	for _, g := range m.groups {
		if g.name == "others" {
			return g.color
		}
	}
	return style.Muted
}

func pctOf(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.width < 1 {
		m.width = 1
	}
	if m.height < 1 {
		m.height = 1
	}
	if m.modal != nil {
		m.modal.termW = width
		m.modal.termH = height
	}
	m.clampAll()
}

// --- helpers on the geometry -------------------------------------------------

// chartListHeights splits the inner height between the chart frame and the
// list frame (leaving the vertical padding and the 1-row gap out). The
// chart gets the bigger share: its detail line + bars need room to breathe.
func (m Model) chartListHeights() (chartH, listH int) {
	inner := m.height - botPad*2
	if inner < 2 {
		return inner, 0
	}
	chartH = inner * 55 / 100
	if chartH < 10 {
		chartH = 10
	}
	if chartH > inner-gap-1 {
		chartH = inner - gap - 1
	}
	listH = inner - chartH - gap
	if listH < 0 {
		listH = 0
	}
	return chartH, listH
}

// chartStep is the horizontal pitch of one day column (column width +
// gap), scaled down so a narrow terminal still fits all 30 days instead of
// clipping the newest ones. Falls back to 1 (a single block per day).
func (m Model) chartStep() int {
	avail := m.contentWidth()
	if len(m.daily) == 0 {
		return dayColW + dayGap
	}
	step := dayColW + dayGap // 4
	for step > 1 && step*windowDays > avail {
		step--
	}
	return step
}

// chartCells splits the current step into the cell width and gap applied
// between consecutive day columns.
func chartCells(step int) (cell, gap int) {
	if step <= 1 {
		return 1, 0
	}
	if step >= dayColW+dayGap {
		return dayColW, dayGap
	}
	return step, 0
}

// contentWidth is the usable width of one panel's body, after the outer
// margin (padSide), the two border columns and the 2-column inner padding.
func (m Model) contentWidth() int {
	w := m.width - padSide*2 - 4
	if w < 1 {
		w = 1
	}
	return w
}

func (m *Model) clampAll() {
	m.clampDayOffset()
	m.clampDayCursor()
	m.clampCursor()
}

// clampDayCursor keeps the selected day inside the 30-day window. A stale
// value outside [0, windowDays) would otherwise index past windowedDays().
func (m *Model) clampDayCursor() {
	if m.dayCursor < 0 {
		m.dayCursor = 0
	}
	if m.dayCursor > windowDays-1 {
		m.dayCursor = windowDays - 1
	}
}

// oldestDayOffset is the largest dayOffset that still shows at least one
// day we have data for (so ← stops scrolling once the window has paged
// past the oldest recorded usage).
func (m Model) oldestDayOffset() int {
	if len(m.daily) == 0 {
		return 0
	}
	oldest := day2006str(m.daily[0].Day)
	for _, b := range m.daily[1:] {
		if d := day2006str(b.Day); d.Before(oldest) {
			oldest = d
		}
	}
	if oldest.IsZero() {
		return 0
	}
	today := time.Now().Local()
	todayMid := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	daysAgo := int(todayMid.Sub(oldest).Hours() / 24)
	max := daysAgo - windowDays + 1
	if max < 0 {
		max = 0
	}
	return max
}

func (m *Model) clampDayOffset() {
	if m.dayOffset < 0 {
		m.dayOffset = 0
	}
	if max := m.oldestDayOffset(); m.dayOffset > max {
		m.dayOffset = max
	}
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// day2006 parses a DailyBucket's Day field ("2006-01-02") back into a Time
// at local midnight. Unparseable entries (shouldn't happen - the field is
// always written by DailyStats) fall back to the zero time.
func day2006str(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

// applyFilter narrows items by the current query. m.items always gets a
// fresh slice: aliasing it directly to m.stats (or reusing its backing
// array across filter changes) would let a later append corrupt the
// underlying aggregate data.
func (m *Model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		m.items = make([]db.ModelStat, len(m.stats))
		copy(m.items, m.stats)
	} else {
		m.items = make([]db.ModelStat, 0, len(m.stats))
		for _, r := range m.stats {
			if strings.Contains(strings.ToLower(r.Model), q) {
				m.items = append(m.items, r)
			}
		}
	}
	m.clampCursor()
}

// --- input ------------------------------------------------------------

// Update handles every message forwarded while stats mode is active.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m.modal != nil {
		mod, cmd := m.modal.Update(msg, m.stats)
		m.modal = mod
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "/":
			m.filtering = true
			m.filterInput.Focus()
			return m, nil
		case "esc":
			if m.filtering {
				m.filterInput.Blur()
				m.filterInput.SetValue("")
				m.filtering = false
				m.applyFilter()
			}
			return m, nil
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
			return m, nil
		case "left":
			// ← moves the day highlight one day into the past. At the
			// left edge of the window the chart pages one day further
			// back (until the oldest recorded usage) so the selected
			// day keeps walking. Not available while filtering (the
			// input eats arrow keys for cursor movement instead).
			if !m.filtering {
				if m.dayCursor > 0 {
					m.dayCursor--
				} else if m.dayOffset < m.oldestDayOffset() {
					m.dayOffset++
					m.clampDayOffset()
				}
			}
			return m, nil
		case "right":
			if !m.filtering {
				if m.dayCursor < windowDays-1 {
					m.dayCursor++
				} else if m.dayOffset > 0 {
					m.dayOffset--
				}
			}
			return m, nil
		case "enter":
			m.magnifySelected()
			return m, nil
		}

		if m.filtering {
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}

		// A printable key (Key.Text is populated only for printable chars)
		// typed anywhere in stats mode starts the filter.
		if key := msg.Key(); key.Text != "" {
			m.filtering = true
			m.filterInput.Focus()
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}
	}
	return m, nil
}

// CanExitStatsMode reports whether Esc in stats mode may leave the tab
// entirely (nothing to dismiss first).
func (m Model) CanExitStatsMode() bool {
	return m.modal == nil && !m.filtering
}

func (m *Model) magnifySelected() {
	if len(m.items) == 0 {
		return
	}
	sel := m.items[m.cursor]
	abs := 0
	for i, r := range m.stats {
		if r.Model == sel.Model {
			abs = i
			break
		}
	}
	m.modal = &ModelDetail{
		model: sel,
		index: abs,
		pct:   pctOf(sel.TotalTokens, m.total),
		termW: m.width,
		termH: m.height,
	}
}

// HandleMouseClick routes a left click inside the stats area; y is local to
// the stats canvas (row 0 = first row below the tabs). Clicks on the chart
// select that day's column; clicks on the list move its cursor.
func (m *Model) HandleMouseClick(x, y int) {
	if m.modal != nil {
		return
	}
	chartH, _ := m.chartListHeights()
	if y < chartH && x >= 0 {
		// chart content starts after the margin, the border and the
		// inner padding; each day spans chartStep() columns.
		cx := x - padSide - 2
		if step := m.chartStep(); step >= 1 {
			if di := cx / step; di >= 0 && di < windowDays {
				m.dayCursor = di
			}
		}
		return
	}
	listTop := chartH + gap
	row := y - listTop
	rowsAvail, _ := m.listRows()
	if row < 0 || row >= rowsAvail || row >= len(m.items) {
		return
	}
	// translate the visible row back to an absolute index: the view shows
	// items[top..top+rowsAvail)
	top := m.listTop()
	m.cursor = top + row
	m.clampCursor()
}

// HandleMouseWheel scrolls the model list (or the modal) on wheel events.
func (m *Model) HandleMouseWheel(x, y int, up bool) {
	if m.modal != nil {
		m.modal.scroll(up)
		return
	}
	if up {
		if m.cursor > 0 {
			m.cursor--
		}
	} else {
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	}
}

// --- detail modal ------------------------------------------------------------

func (md *ModelDetail) Update(msg tea.Msg, all []db.ModelStat) (*ModelDetail, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "esc", "enter", "tab", "shift+tab":
			return nil, nil
		case "left", "right":
			if len(all) == 0 {
				return md, nil
			}
			n := len(all)
			idx := md.index
			if key == "left" {
				idx = (idx - 1 + n) % n
			} else {
				idx = (idx + 1) % n
			}
			md.model = all[idx]
			md.index = idx
			md.vScroll = 0
			total := 0
			for _, r := range all {
				total += r.TotalTokens
			}
			md.pct = pctOf(md.model.TotalTokens, total)
			return md, nil
		case "up":
			if md.vScroll > 0 {
				md.vScroll--
			}
			return md, nil
		case "down":
			if md.vScroll < md.vScrollMax() {
				md.vScroll++
			}
			return md, nil
		}
	}
	return md, nil
}

func (md *ModelDetail) scroll(up bool) {
	if up && md.vScroll > 0 {
		md.vScroll--
	} else if !up && md.vScroll < md.vScrollMax() {
		md.vScroll++
	}
}

// vScrollMax is the furthest the detail rows can scroll: the number of
// detail rows beyond what fits inside the dialog body.
func (md ModelDetail) vScrollMax() int {
	const detailRowCount = 9 // len(rows) in render()
	visible := md.dialogHeight() - 10
	if visible < 1 {
		visible = 1
	}
	max := detailRowCount - visible
	if max < 0 {
		max = 0
	}
	return max
}

// dialogWidth/Height follow the ProviderSelect resize logic: default size,
// clamped down to the terminal minus a small margin.
func (md ModelDetail) dialogWidth() int {
	w := 76
	if md.termW > 0 && md.termW-4 < w {
		w = max(30, md.termW-4)
	}
	return w
}

func (md ModelDetail) dialogHeight() int {
	h := 24
	if md.termH > 0 && md.termH-2 < h {
		h = max(10, md.termH-2)
	}
	return h
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (md ModelDetail) render() string {
	w := md.dialogWidth() - 4

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Info).
		Render("✦ " + md.model.Model + " ✦")

	sub := lipgloss.NewStyle().
		Foreground(style.Muted).
		Render(md.model.Provider)

	barLen := w - 2
	filled := int(float64(barLen) * md.pct / 100)
	bar := lipgloss.NewStyle().Foreground(barColor(md.index)).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("░", barLen-filled))

	pctLine := lipgloss.NewStyle().Bold(true).Foreground(style.Text).
		Render(fmt.Sprintf("%.1f%% of usage · %s tokens", md.pct, prettyInt(md.model.TotalTokens)))

	rows := [][2]string{
		{"model", md.model.Model},
		{"provider", md.model.Provider},
		{"prompt tokens", prettyInt(md.model.PromptTokens)},
		{"completion tokens", prettyInt(md.model.CompletionTokens)},
		{"total tokens", prettyInt(md.model.TotalTokens)},
		{"generations", fmt.Sprintf("%d", md.model.Calls)},
		{"tool calls", fmt.Sprintf("%d", md.model.ToolCalls)},
		{"first used", md.model.FirstUsed.Format("2006-01-02 15:04")},
		{"last used", md.model.LastUsed.Format("2006-01-02 15:04")},
	}
	var b strings.Builder
	keyW := 0
	for _, r := range rows {
		if len(r[0]) > keyW {
			keyW = len(r[0])
		}
	}
	visible := md.dialogHeight() - 10
	if visible < 1 {
		visible = 1
	}
	start := md.vScroll
	if start > len(rows) {
		start = len(rows)
	}
	end := start + visible
	if end > len(rows) {
		end = len(rows)
	}
	for _, r := range rows[start:end] {
		b.WriteString(lipgloss.NewStyle().
			Foreground(style.Muted).
			Width(keyW).
			Render(r[0]))
		b.WriteString("  ")
		b.WriteString(lipgloss.NewStyle().
			Foreground(style.Text).
			Render(r[1]))
		b.WriteString("\n")
	}

	help := lipgloss.NewStyle().
		Foreground(style.Muted).
		Render("← → browse · ↑ ↓ scroll · esc close")

	content := title + "\n" + sub + "\n\n" + bar + "\n" + pctLine + "\n\n" +
		strings.TrimRight(b.String(), "\n") + "\n\n" + help

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Background(style.Background).
		Padding(1, 2).
		Width(md.dialogWidth()).
		Height(md.dialogHeight()).
		Render(content)
}

func barColor(index int) color.Color {
	return style.GraphColors[index%len(style.GraphColors)]
}

func (m Model) overlayModal(content string) string {
	if m.modal == nil {
		return content
	}
	dialog := m.modal.render()
	dw := lipgloss.Width(dialog)
	dh := lipgloss.Height(dialog)
	w := m.width
	h := m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	x := (w - dw) / 2
	y := (h - dh) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	base := lipgloss.NewLayer(content).X(0).Y(0).Z(0)
	float := lipgloss.NewLayer(dialog).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, float).Render()
}

// --- rendering --------------------------------------------------------------

func (m Model) View() string {
	chartH, listH := m.chartListHeights()
	frameW := m.width - padSide*2

	chartBody := m.chartBody(chartH - 2)
	chartFrame := drawFrame(frameW, chartH, chartBody)

	listBody := m.listBody(listH - 2)
	listFrame := drawFrame(frameW, listH, listBody)

	joined := chartFrame + "\n" + strings.Repeat(" ", frameW) + "\n" + listFrame

	view := lipgloss.NewStyle().
		Margin(0, padSide).
		Width(frameW).
		Render(joined)

	return m.overlayModal(view)
}

// drawFrame renders a full bordered panel with the same plain rounded
// border used throughout the app (see ProviderSelect). Body lines are
// padded/truncated to innerW (w-4) so ragged content can't blow out the
// frame. Lines may already carry ANSI styling (chartBody/listBody render
// colored segments), so width is measured and truncated the ANSI-aware
// way via lipgloss rather than by naively slicing runes, which would
// corrupt escape sequences.
func drawFrame(w, h int, body []string) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}

	innerW := w - 4
	innerH := h - 2
	rowStyle := lipgloss.NewStyle().MaxWidth(innerW).Width(innerW)
	var lines []string
	for i := 0; i < innerH; i++ {
		var line string
		if i < len(body) {
			line = body[i]
		}
		lines = append(lines, rowStyle.Render(line))
	}
	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Background(style.Background).
		Padding(0, 1).
		Width(w - 2).
		Height(h - 2).
		Render(content)
}

// clip returns the substring of s (plain, unstyled text only - never call
// this on already-ANSI-styled strings, since rune-slicing would corrupt
// escape sequences) whose columns lie in [start, start+maxW), padded with
// trailing spaces to maxW.
func clip(s string, start, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	runes := []rune(s)
	if start > 0 {
		if start >= len(runes) {
			return strings.Repeat(" ", maxW)
		}
		runes = runes[start:]
	}
	if len(runes) > maxW {
		runes = runes[:maxW]
	}
	pad := maxW - lipgloss.Width(string(runes))
	if pad < 0 {
		pad = 0
	}
	return string(runes) + strings.Repeat(" ", pad)
}

// --- daily chart --------------------------------------------------------

// windowedDays returns the windowDays-long slice of dayBars currently in
// view (oldest first), built from m.daily and the current m.dayOffset.
// Days with no recorded usage get hasData=false so the caller can render a
// blank column instead of a zero-height sliver.
func (m Model) windowedDays() []dayBar {
	byDay := make(map[string]db.DailyBucket, len(m.daily))
	for _, b := range m.daily {
		byDay[b.Day] = b
	}

	today := time.Now().Local()
	end := today.AddDate(0, 0, -m.dayOffset)
	start := end.AddDate(0, 0, -(windowDays - 1))

	out := make([]dayBar, 0, windowDays)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		bar := dayBar{day: key, label: d.Format("Jan 02")}
		if b, ok := byDay[key]; ok && b.Total > 0 {
			bar.hasData = true
			bar.total = b.Total
			bar.toolCalls = b.ToolCalls
			bar.tokens = make(map[string]int, len(b.Tokens))
			for model, n := range b.Tokens {
				bar.tokens[model] = n
			}
			bar.modelTools = make(map[string]int, len(b.Tools))
			for model, n := range b.Tools {
				bar.modelTools[model] = n
			}
			bar.segments = segmentsFor(m, b)
		}
		out = append(out, bar)
	}
	return out
}

// segmentsFor turns one day's per-model token map into stacked segments,
// largest share first, using the same colors as the model list/legend.
// Models sharing the "others" color are merged into a single segment so
// the stack doesn't fragment into slivers per minor model.
func segmentsFor(m Model, b db.DailyBucket) []segment {
	type acc struct {
		color color.Color
		tok   int
	}
	byColor := map[color.Color]*acc{}
	order := []color.Color{}
	for model, tok := range b.Tokens {
		c := m.colorFor(model)
		a, ok := byColor[c]
		if !ok {
			a = &acc{color: c}
			byColor[c] = a
			order = append(order, c)
		}
		a.tok += tok
	}
	sort.Slice(order, func(i, j int) bool {
		return byColor[order[i]].tok > byColor[order[j]].tok
	})
	segs := make([]segment, 0, len(order))
	for _, c := range order {
		a := byColor[c]
		segs = append(segs, segment{color: c, share: float64(a.tok) / float64(b.Total)})
	}
	return segs
}

// chartBody renders the chart panel's interior: a header line (visible
// date range, window token/tool totals, paging hint), the selected day's
// detail line (tokens and tool calls, per model), a month band, the
// stacked daily bars with the selected day's column highlighted, an axis
// of day numbers, a caret row marking the selection and tool-call days,
// and a one-line color legend.
func (m Model) chartBody(rowsAvail int) []string {
	out := make([]string, 0, rowsAvail)

	if len(m.daily) == 0 {
		for i := 0; i < rowsAvail; i++ {
			if i == 0 {
				out = append(out, lipgloss.NewStyle().Foreground(style.Muted).
					Render("No usage recorded in the last 30 days yet."))
			} else {
				out = append(out, "")
			}
		}
		return out
	}

	days := m.windowedDays()
	sel := m.dayCursor
	if sel < 0 || sel >= len(days) {
		sel = 0
	}
	cellW, gapW := chartCells(m.chartStep())

	// The detail line only gets its own row when the panel is tall
	// enough to keep the bars at at least two rows; otherwise the bars
	// absorb the whole area.
	fixed := 5 // month band + bar area + axis + caret row + legend
	withDetail := rowsAvail-fixed >= 3
	barRows := rowsAvail - fixed
	if withDetail {
		barRows--
	}
	if barRows < 2 {
		barRows = 2
	}

	// header: visible date range (month names), totals, paging hint.
	header := lipgloss.NewStyle().Bold(true).Foreground(style.Info).
		Render(fmt.Sprintf("%s – %s", days[0].label, days[len(days)-1].label))
	windowTotal := 0
	windowTools := 0
	for _, d := range days {
		windowTotal += d.total
		windowTools += d.toolCalls
	}
	header += lipgloss.NewStyle().Foreground(style.Muted).
		Render(fmt.Sprintf("  ·  %s tokens", prettyInt(windowTotal)))
	if windowTools > 0 {
		header += lipgloss.NewStyle().Foreground(style.Warning).
			Render(fmt.Sprintf("  ·  %d tool call%s", windowTools, plural(windowTools)))
	}
	if m.dayOffset > 0 || m.oldestDayOffset() > 0 {
		header += lipgloss.NewStyle().Foreground(style.Muted).Render("  ·  ←→ browse days")
	}
	out = append(out, header)

	if withDetail {
		out = append(out, m.dayDetailLine(days[sel]))
	}

	if cellW >= 3 {
		out = append(out, monthBandRow(days, cellW, gapW))
	}

	maxTotal := 0
	for _, d := range days {
		if d.total > maxTotal {
			maxTotal = d.total
		}
	}

	// Build the bar area top-down: row 0 is the top of the tallest
	// possible bar, row barRows-1 sits on the axis.
	grid := make([][]string, barRows)
	for r := range grid {
		grid[r] = make([]string, len(days))
	}
	barGlyph := strings.Repeat("█", cellW)
	for di, d := range days {
		selected := di == sel
		if !d.hasData || maxTotal <= 0 {
			for r := 0; r < barRows; r++ {
				grid[r][di] = colCell(strings.Repeat(" ", cellW), selected, nil)
			}
			continue
		}
		filledRows := int(float64(barRows) * float64(d.total) / float64(maxTotal))
		if filledRows < 1 {
			filledRows = 1 // a day with any usage always shows at least a sliver
		}
		if filledRows > barRows {
			filledRows = barRows
		}
		// distribute filledRows across segments by share, largest first,
		// so every segment with a nonzero share gets at least one row.
		rowsForSeg := make([]int, len(d.segments))
		remaining := filledRows
		for i, s := range d.segments {
			n := int(float64(filledRows) * s.share)
			if n < 1 && remaining > 0 {
				n = 1
			}
			if n > remaining {
				n = remaining
			}
			rowsForSeg[i] = n
			remaining -= n
		}
		// any leftover rows (rounding) go to the largest segment
		if remaining > 0 && len(rowsForSeg) > 0 {
			rowsForSeg[0] += remaining
		}

		// paint from the axis upward: segments are already sorted
		// largest-share first, so visually the largest sits at the bottom.
		rowCursor := barRows - 1
		for i, s := range d.segments {
			n := rowsForSeg[i]
			for k := 0; k < n && rowCursor >= 0; k++ {
				grid[rowCursor][di] = colCell(barGlyph, selected, s.color)
				rowCursor--
			}
		}
		for rowCursor >= 0 {
			grid[rowCursor][di] = colCell(strings.Repeat(" ", cellW), selected, nil)
			rowCursor--
		}
	}

	sep := strings.Repeat(" ", gapW)
	for r := 0; r < barRows; r++ {
		out = append(out, strings.Join(grid[r], sep))
	}

	// axis: day-of-month number under every bar; the selected day is
	// bright with the highlight behind it.
	axisCells := make([]string, len(days))
	for di, d := range days {
		axisCells[di] = lipgloss.NewStyle().
			Foreground(style.Muted).
			Width(cellW).
			AlignHorizontal(lipgloss.Right).
			Render(fmt.Sprintf("%2d", dayOfMonth(d.day)))
	}
	out = append(out, lipgloss.NewStyle().Render(strings.Join(axisCells, sep)))

	// caret row: the selected day gets the caret (plus its highlight),
	// every day in the window that called tools gets a dot.
	caretCells := make([]string, len(days))
	for di, d := range days {
		switch {
		case di == sel:
			caretCells[di] = lipgloss.NewStyle().
				Foreground(style.Info).
				Bold(true).
				Background(style.Highlight).
				Width(cellW).
				AlignHorizontal(lipgloss.Center).
				Render("▲")
		case d.hasData && d.toolCalls > 0:
			caretCells[di] = lipgloss.NewStyle().
				Foreground(style.Warning).
				Width(cellW).
				AlignHorizontal(lipgloss.Center).
				Render("•")
		default:
			caretCells[di] = strings.Repeat(" ", cellW)
		}
	}
	out = append(out, lipgloss.NewStyle().Render(strings.Join(caretCells, sep)))

	out = append(out, m.legendLine())

	for len(out) < rowsAvail {
		out = append(out, "")
	}
	if len(out) > rowsAvail {
		out = out[:rowsAvail]
	}
	return out
}

// colCell renders one chart column cell: a fill string (bar glyphs or
// spaces) optionally carrying the model segment color, with the selected
// day's highlight behind the whole column.
func colCell(fill string, selected bool, c color.Color) string {
	st := lipgloss.NewStyle()
	if c != nil {
		st = st.Foreground(c)
	}
	if selected {
		st = st.Background(style.Highlight)
	}
	return st.Render(fill)
}

// monthBandRow renders the 3-letter month abbreviations centered over
// each month's run of columns (the exact dates live in the day-number
// axis below the bars).
func monthBandRow(days []dayBar, cellW, gapW int) string {
	type span struct{ start, end int }
	monOf := func(i int) string {
		t, err := time.Parse("2006-01-02", days[i].day)
		if err != nil {
			return ""
		}
		return t.Month().String()[:3]
	}
	cells := make([]string, len(days))
	for i := 0; i < len(days); {
		mon := monOf(i)
		j := i
		for j < len(days) && monOf(j) == mon {
			j++
		}
		cells[i+(j-i-1)/2] = mon // centered over the run
		i = j
	}
	out := make([]string, len(days))
	for i, name := range cells {
		out[i] = lipgloss.NewStyle().
			Foreground(style.Muted).
			Width(cellW).
			Render(name)
	}
	return strings.Join(out, strings.Repeat(" ", gapW))
}

// dayOfMonth extracts the day-of-month from a "2006-01-02" key.
func dayOfMonth(key string) int {
	t, err := time.Parse("2006-01-02", key)
	if err != nil {
		return 0
	}
	return t.Day()
}

// dayDetailLine renders the selected day's numbers: date, token total,
// tool-call count, and a per-model breakdown. Pieces are appended only
// while the available width lasts (the list's legendLine does the same),
// so the line can never grow past the frame and wrap. A small safety
// margin accounts for the frame's re-render width bookkeeping.
func (m Model) dayDetailLine(d dayBar) string {
	avail := m.contentWidth() - 4
	if avail < 8 {
		avail = 8
	}
	var b strings.Builder
	used := 0
	appendStyled := func(fg color.Color, s string) {
		need := lipgloss.Width(s)
		if used+need > avail {
			if used < avail {
				b.WriteString(lipgloss.NewStyle().Foreground(style.Muted).
					Render(clip(s, 0, avail-used)))
			}
			return
		}
		used += need
		b.WriteString(lipgloss.NewStyle().Foreground(fg).Render(s))
	}

	appendStyled(style.Info, d.label+" ·")
	if !d.hasData {
		appendStyled(style.Muted, " no usage")
		return b.String()
	}
	appendStyled(style.Text, " "+prettyInt(d.total)+" tokens")
	if d.toolCalls > 0 {
		appendStyled(style.Warning, fmt.Sprintf(" · %d tool call%s", d.toolCalls, plural(d.toolCalls)))
	}
	type part struct {
		name  string
		tok   int
		tools int
	}
	parts := make([]part, 0, len(d.tokens))
	for name, tok := range d.tokens {
		parts = append(parts, part{name: name, tok: tok, tools: d.modelTools[name]})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].tok > parts[j].tok })
	for _, p := range parts {
		label := "  " + p.name + " " + compact(p.tok)
		if p.tools > 0 {
			label += fmt.Sprintf(" (%d tool call%s)", p.tools, plural(p.tools))
		}
		if used+lipgloss.Width(label) > avail {
			room := avail - used
			if room > 2 {
				room = max(room-2, 3)
				b.WriteString("  ")
				b.WriteString(lipgloss.NewStyle().Foreground(m.colorFor(p.name)).
					Render(clip(p.name, 0, room)))
			}
			break
		}
		used += lipgloss.Width(label)
		b.WriteString(lipgloss.NewStyle().Foreground(m.colorFor(p.name)).Render(label))
	}
	return b.String()
}

// compact renders a token count as a short human string ("8.5k", "1.2M").
func compact(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// legendLine renders a single-line "swatch model  swatch model  ..." strip
// mapping each chart color to its model name, truncating with a "+N more"
// tail if it doesn't fit the available width.
func (m Model) legendLine() string {
	if len(m.groups) == 0 {
		return ""
	}
	avail := m.contentWidth()
	var b strings.Builder
	used := 0
	shown := 0
	const swatch = "██ " // 3 display columns: two block glyphs + a space
	for i, g := range m.groups {
		sep := ""
		if i > 0 {
			sep = "  "
		}
		need := len([]rune(sep)) + len([]rune(swatch)) + len([]rune(g.name))
		// always leave room for a possible "+N more" tail
		remainingGroups := len(m.groups) - shown - 1
		tailRoom := 0
		if remainingGroups > 0 {
			tailRoom = len([]rune(fmt.Sprintf("  +%d more", remainingGroups)))
		}
		if used+need+tailRoom > avail && shown > 0 {
			b.WriteString(fmt.Sprintf("  +%d more", len(m.groups)-shown))
			return b.String()
		}
		b.WriteString(sep)
		b.WriteString(lipgloss.NewStyle().Foreground(g.color).Render("██"))
		b.WriteString(" ")
		b.WriteString(lipgloss.NewStyle().Foreground(style.Text).Render(g.name))
		used += need
		shown++
	}
	return b.String()
}

// listTop is the first item index currently on screen.
func (m Model) listTop() int {
	rowsAvail, _ := m.listRows()
	if rowsAvail < 1 || len(m.items) <= rowsAvail {
		return 0
	}
	maxTop := len(m.items) - rowsAvail
	top := m.cursor - rowsAvail/2
	if top > maxTop {
		top = maxTop
	}
	if top < 0 {
		top = 0
	}
	return top
}

// listRows returns how many model rows fit and how many lines the list
// body holds.
func (m Model) listRows() (rows, lines int) {
	_, listH := m.chartListHeights()
	lines = listH - 2 // body lines between the frame edges
	rows = lines - 2  // filter line + header line
	if rows < 0 {
		rows = 0
	}
	return rows, lines
}

// listColumns decides which optional columns fit in the available width,
// widest/least-important first, so the list degrades gracefully instead of
// clipping. name and total always show; everything else is dropped in
// this order as space runs out: last used, tools, calls, provider,
// completion, prompt.
type listCols struct {
	provider, prompt, completion, tools, calls, lastUsed bool
}

func (m Model) fitColumns() listCols {
	avail := m.contentWidth()
	c := listCols{true, true, true, true, true, true}
	fixed := func() int {
		w := selCol + 1 + nameCol + 1 + tCol // always-on: selector, name, total
		if c.provider {
			w += 1 + provCol
		}
		if c.prompt {
			w += 1 + pCol
		}
		if c.completion {
			w += 1 + cCol
		}
		if c.calls {
			w += 1 + callCol
		}
		if c.tools {
			w += 1 + toolCol
		}
		if c.lastUsed {
			w += 1 + lastCol
		}
		return w
	}
	// drop in priority order until it fits, cheapest signal first
	for fixed() > avail && c.lastUsed {
		c.lastUsed = false
	}
	for fixed() > avail && c.tools {
		c.tools = false
	}
	for fixed() > avail && c.calls {
		c.calls = false
	}
	for fixed() > avail && c.provider {
		c.provider = false
	}
	for fixed() > avail && c.completion {
		c.completion = false
	}
	for fixed() > avail && c.prompt {
		c.prompt = false
	}
	return c
}

func (m Model) listBody(linesAvail int) []string {
	if linesAvail < 0 {
		linesAvail = 0
	}
	out := make([]string, 0, linesAvail)
	cols := m.fitColumns()

	// filter bar (or hint)
	if m.filtering {
		filter := m.filterInput.View()
		out = append(out, lipgloss.NewStyle().Foreground(style.Info).Render(filter))
	} else {
		out = append(out, lipgloss.NewStyle().Foreground(style.Muted).
			Render("/ filter · ↑↓ select · enter magnify · esc exit"))
	}

	// header
	out = append(out, m.listHeader(cols))

	rowsAvail := linesAvail - 2
	if rowsAvail < 0 {
		rowsAvail = 0
	}
	top := m.listTop()
	for i := 0; i < rowsAvail; i++ {
		idx := top + i
		if idx >= len(m.items) {
			out = append(out, "")
			continue
		}
		out = append(out, m.listRow(m.items[idx], idx == m.cursor, cols))
	}
	return out
}

func (m Model) listHeader(cols listCols) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", selCol+1))
	b.WriteString(fmt.Sprintf("%-*s", nameCol, "model"))
	if cols.provider {
		b.WriteString(" " + fmt.Sprintf("%*s", provCol, "provider"))
	}
	if cols.prompt {
		b.WriteString(" " + fmt.Sprintf("%*s", pCol, "prompt"))
	}
	if cols.completion {
		b.WriteString(" " + fmt.Sprintf("%*s", cCol, "comp"))
	}
	b.WriteString(" " + fmt.Sprintf("%*s", tCol, "total"))
	if cols.calls {
		b.WriteString(" " + fmt.Sprintf("%*s", callCol, "calls"))
	}
	if cols.tools {
		b.WriteString(" " + fmt.Sprintf("%*s", toolCol, "tools"))
	}
	if cols.lastUsed {
		b.WriteString(" " + fmt.Sprintf("%*s", lastCol, "last used"))
	}
	return lipgloss.NewStyle().Foreground(style.Muted).Bold(true).Render(b.String())
}

func (m Model) listRow(r db.ModelStat, selected bool, cols listCols) string {
	sel := " "
	if selected {
		sel = "▸"
	}
	selStyled := lipgloss.NewStyle().
		Foreground(style.Info).
		Width(selCol).
		Render(sel)

	nameStyle := lipgloss.NewStyle().
		Width(nameCol).
		AlignHorizontal(lipgloss.Left).
		Foreground(m.colorFor(r.Model))
	if selected {
		nameStyle = nameStyle.Bold(true)
	}

	var b strings.Builder
	b.WriteString(selStyled)
	b.WriteString(" ")
	b.WriteString(nameStyle.Render(clip(r.Model, 0, nameCol)))

	rightCol := func(w int, v string) {
		b.WriteString(" ")
		b.WriteString(lipgloss.NewStyle().
			Width(w).
			AlignHorizontal(lipgloss.Right).
			Foreground(style.Text).
			Render(v))
	}
	if cols.provider {
		rightCol(provCol, r.Provider)
	}
	if cols.prompt {
		rightCol(pCol, prettyInt(r.PromptTokens))
	}
	if cols.completion {
		rightCol(cCol, prettyInt(r.CompletionTokens))
	}
	rightCol(tCol, prettyInt(r.TotalTokens))
	if cols.calls {
		rightCol(callCol, fmt.Sprintf("%d", r.Calls))
	}
	if cols.tools {
		rightCol(toolCol, fmt.Sprintf("%d", r.ToolCalls))
	}
	if cols.lastUsed {
		rightCol(lastCol, r.LastUsed.Format("01-02"))
	}

	row := b.String()
	if selected {
		row = lipgloss.NewStyle().
			Background(style.Highlight).
			Foreground(style.Text).
			Render(row)
	}
	return row
}

func prettyInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = s[1:]
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	if n < 0 {
		return "-" + string(out)
	}
	return string(out)
}
