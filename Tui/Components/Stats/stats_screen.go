// Package stats is GoWork's usage dashboard. It turns the rows the Db
// package aggregates (per model, over the last 30 days) into two panels: a
// bar chart of the five most-used models plus "others", and a scrollable,
// searchable list of every model with its salvageable numbers. All border
// chrome is drawn in the info color and the whole screen is sized with the
// same outer padding as the message area. In stats mode the prompt bar is
// gone and the statusline is pinned below.
package stats

import (
	"fmt"
	"image/color"
	"strings"

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

	// all glyphs below are single-width (ASCII + block chars) so rune
	// counting == column counting and panning can slice on rune indexes.
	chipCol = 2   // colored chip at the front of a bar row
	nameCol = 18  // model-name column
	barWMax = 22  // full-width bar in chars
	pctCol  = 7   // "NN.N%"
	tokCol  = 10  // token total
	selCol  = 1   // "▸" selection marker
	provCol = 9
	pCol    = 9 // prompt tokens
	cCol    = 9 // completion tokens
	tCol    = 9 // total tokens
	callCol = 6
	toolCol = 6
	lastCol = 10 // "MM-DD"
)

// ornamental patterns for the two frames' edges.
var graphOrnament = []rune("━✦❘༻༺❘✦") // ┏━✦❘༻༺❘✦━━┓ / ┗━✦❘༻༺❘✦━━┛
var listOrnament = []rune("══━┈━═")     // ┌──═━┈━═──┐ / └──═━┈━═──┘

type group struct {
	name   string
	tokens int
	color  color.Color
	pct    float64
}

// ModelDetail is the Enter-to-magnify popup for one model.
type ModelDetail struct {
	model  db.ModelStat
	index  int // position in the full (unfiltered) list for ←/→ browsing
	pct    float64
	termW  int
	termH  int
	vScroll int
}

// Model is the stats screen: data, list selection/filter/scroll, the
// horizontal pan shared by both panels, and an optional magnified overlay.
type Model struct {
	width  int
	height int

	stats  []db.ModelStat
	total  int
	groups []group

	cursor      int
	items       []db.ModelStat // visible after filtering
	filtering   bool
	filterInput textinput.Model

	hScroll int

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

// SetData hands the screen a fresh set of aggregates (on tab entry and after
// every recorded generation) and recomputes everything derived from them.
func (m *Model) SetData(rows []db.ModelStat) {
	m.stats = rows
	m.total = 0
	for _, r := range rows {
		m.total += r.TotalTokens
	}
	m.groups = buildGroups(rows)
	m.applyFilter()
	m.clampAll()
}

// buildGroups makes the "top 5 + others" chart from the aggregates.
func buildGroups(stats []db.ModelStat) []group {
	if len(stats) == 0 {
		return nil
	}
	top := stats
	others := 0
	if len(stats) > 5 {
		top = stats[:5]
		for _, r := range stats[5:] {
			others += r.TotalTokens
		}
	}
	total := 0
	for _, r := range stats {
		total += r.TotalTokens
	}

	g := make([]group, 0, len(top)+1)
	for i, r := range top {
		g = append(g, group{
			name:   r.Model,
			tokens: r.TotalTokens,
			color:  style.GraphColors[i%len(style.GraphColors)],
			pct:    pctOf(r.TotalTokens, total),
		})
	}
	if others > 0 {
		g = append(g, group{
			name:   "others",
			tokens: others,
			color:  style.GraphColors[len(style.GraphColors)-1],
			pct:    pctOf(others, total),
		})
	}
	return g
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
// list frame (leaving the vertical padding and the 1-row gap out).
func (m Model) chartListHeights() (chartH, listH int) {
	inner := m.height - botPad*2
	if inner < 2 {
		return inner, 0
	}
	chartH = inner * 45 / 100
	if chartH < 6 {
		chartH = 6
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

// contentWidth is the usable width of one panel's body, after the outer
// margin (padSide), the two border columns and the 2-column inner padding.
func (m Model) contentWidth() int {
	w := m.width - padSide*2 - 4
	if w < 1 {
		w = 1
	}
	return w
}

func (m Model) graphRowWidth() int {
	return chipCol + 1 + nameCol + 2 + barWMax + 1 + pctCol + 1 + tokCol
}

func (m Model) listRowWidth() int {
	return selCol + 2 + nameCol + 1 + provCol + 1 + pCol + 1 + cCol + 1 + tCol + 1 + callCol + 1 + toolCol + 1 + lastCol
}

// requiredWidth is the widest canvas element, which drives whether the
// horizontal pan is available at all.
func (m Model) requiredWidth() int {
	return max3(m.graphRowWidth(), m.listRowWidth(), m.contentWidth())
}

func max3(a, b, c int) int {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func (m Model) hScrollMax() int {
	need, have := m.requiredWidth(), m.contentWidth()
	if need <= have {
		return 0
	}
	return need - have
}

func (m *Model) clampAll() {
	m.clampScroll()
	m.clampCursor()
}

func (m *Model) clampScroll() {
	if max := m.hScrollMax(); m.hScroll > max {
		m.hScroll = max
	}
	if m.hScroll < 0 {
		m.hScroll = 0
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

// applyFilter narrows items by the current query.
func (m *Model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		m.items = m.stats
	} else {
		m.items = m.items[:0]
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
			m.hScroll -= 4
			m.clampScroll()
			return m, nil
		case "right":
			if max := m.hScrollMax(); m.hScroll < max {
				m.hScroll += 4
				if m.hScroll > max {
					m.hScroll = max
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
		model:  sel,
		index:  abs,
		pct:    pctOf(sel.TotalTokens, m.total),
		termW:  m.width,
		termH:  m.height,
	}
}

// HandleMouseClick routes a left click inside the stats area; y is local to
// the stats canvas (row 0 = first row below the tabs).
func (m *Model) HandleMouseClick(x, y int) {
	if m.modal != nil {
		return
	}
	chartH, _ := m.chartListHeights()
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
			md.vScroll++
			return md, nil
		}
	}
	return md, nil
}

func (md *ModelDetail) scroll(up bool) {
	if up && md.vScroll > 0 {
		md.vScroll--
	} else if !up {
		md.vScroll++
	}
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
	for _, r := range rows {
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

	// chart frame body
	chartBody := m.chartBody(chartH - 2)
	chartFrame := drawFrame(graphOrnament, "┏", "┓", "┗", "┛", "┃", "┃",
		frameW, chartH, m.hScroll, chartBody)

	// list frame body
	listBody := m.listBody(listH - 2)
	listFrame := drawFrame(listOrnament, "┌", "┐", "└", "┘", "│", "│",
		frameW, listH, m.hScroll, listBody)

	joined := chartFrame + "\n" + strings.Repeat(" ", frameW) + "\n" + listFrame

	view := lipgloss.NewStyle().
		Margin(0, padSide).
		Width(frameW).
		Render(joined)

	return m.overlayModal(view)
}

// drawFrame renders a full bordered panel. Body lines are at most innerW
// (w-4) wide after the pan offset and the inner 1-column padding.
func drawFrame(ornament []rune, tl, tr, bl, br, l, r string, w, h, pan int, body []string) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	edge := lipgloss.NewStyle().Foreground(style.Info)

	var lines []string
	lines = append(lines, edge.Render(tl+fillPattern(ornament, w-2)+tr))
	for i := 0; i < h-2; i++ {
		var line string
		if i < len(body) {
			line = body[i]
		}
		mid := " " + clip(line, pan, w-4) + " "
		lines = append(lines, edge.Render(l)+mid+edge.Render(r))
	}
	lines = append(lines, edge.Render(bl+fillPattern(ornament, w-2)+br))
	return strings.Join(lines, "\n")
}

// fillPattern repeats an ornament rune cycle until it fills n columns.
func fillPattern(ornament []rune, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]rune, 0, n)
	for i := 0; len(b) < n; i++ {
		b = append(b, ornament[i%len(ornament)])
	}
	return string(b)
}

// clip returns the substring of s whose columns lie in [start, start+maxW),
// padded with trailing spaces to maxW. Only single-width runes allowed.
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
	return string(runes) + strings.Repeat(" ", maxW-len(runes))
}

// chartBody renders the graph panel's interior (full-width, pre-pan).
func (m Model) chartBody(rowsAvail int) []string {
	out := make([]string, 0, rowsAvail)

	if len(m.groups) == 0 {
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

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Info).
		Render(fmt.Sprintf("Usage · last 30d · %s tokens total", prettyInt(m.total)))
	out = append(out, header)
	if rowsAvail > 1 {
		out = append(out, "")
	}

	for i, g := range m.groups {
		line := m.barLine(g)
		if i == 0 {
			// first row gets a thin marker so the user can map colors to bars
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		out = append(out, line)
	}
	for len(out) < rowsAvail {
		out = append(out, "")
	}
	return out
}

func (m Model) barLine(g group) string {
	chip := lipgloss.NewStyle().
		Foreground(g.color).
		Width(chipCol).
		Render("▮▮")

	name := clip(g.name, 0, nameCol)
	name = lipgloss.NewStyle().
		Foreground(g.color).
		Bold(true).
		Width(nameCol).
		Render(name)

	barLen := barWMax
	if barLen > m.contentWidth()-nameCol-8 {
		barLen = max(2, m.contentWidth()-nameCol-8)
	}
	filled := int(float64(barLen) * g.pct / 100)
	if filled < 0 {
		filled = 0
	}
	if filled > barLen {
		filled = barLen
	}
	bar := lipgloss.NewStyle().Foreground(g.color).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("░", barLen-filled))

	pct := fmt.Sprintf("%.1f%%", g.pct)
	pctS := lipgloss.NewStyle().Width(pctCol).AlignHorizontal(lipgloss.Right).Render(pct)

	tok := lipgloss.NewStyle().
		Width(tokCol).
		AlignHorizontal(lipgloss.Right).
		Foreground(style.Text).
		Render(prettyInt(g.tokens))

	return chip + " " + name + "  " + bar + " " + pctS + " " + tok
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

func (m Model) listBody(linesAvail int) []string {
	if linesAvail < 0 {
		linesAvail = 0
	}
	out := make([]string, 0, linesAvail)

	// filter bar (or hint)
	if m.filtering {
		filter := m.filterInput.View()
		out = append(out, lipgloss.NewStyle().Foreground(style.Info).Render(filter))
	} else {
		out = append(out, lipgloss.NewStyle().Foreground(style.Muted).
			Render("/ filter · ↑↓ select · ←→ pan · enter magnify · esc exit"))
	}

	// header
	out = append(out, lipgloss.NewStyle().
		Foreground(style.Muted).
		Bold(true).
		Render(fmt.Sprintf("%-*s %-*s %*s %*s %*s %*s %*s %*s",
			nameCol, "model", provCol, "provider",
			pCol, "prompt", cCol, "comp", tCol, "total",
			callCol, "calls", toolCol, "tools", lastCol, "last used")))

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
		out = append(out, m.listRow(m.items[idx], idx == m.cursor))
	}
	return out
}

func (m Model) listRow(r db.ModelStat, selected bool) string {
	sel := " "
	if selected {
		sel = "▸"
	}
	sel = lipgloss.NewStyle().
		Foreground(style.Info).
		Width(selCol).
		Render(sel)

	colors := []struct {
		w int
		v string
	}{
		{nameCol, r.Model},
		{provCol, r.Provider},
		{pCol, prettyInt(r.PromptTokens)},
		{cCol, prettyInt(r.CompletionTokens)},
		{tCol, prettyInt(r.TotalTokens)},
		{callCol, fmt.Sprintf("%d", r.Calls)},
		{toolCol, fmt.Sprintf("%d", r.ToolCalls)},
		{lastCol, r.LastUsed.Format("01-02")},
	}

	var b strings.Builder
	b.WriteString(sel)
	b.WriteString(" ")
	for i, c := range colors {
		st := lipgloss.NewStyle().
			Width(c.w).
			AlignHorizontal(lipgloss.Right).
			Foreground(style.Text)
		if i == 0 {
			st = lipgloss.NewStyle().
				Width(c.w).
				AlignHorizontal(lipgloss.Left).
				Foreground(barColor(idxOf(r, m.stats)))
			if selected {
				st = st.Bold(true)
			}
		}
		b.WriteString(st.Render(c.v))
		if i < len(colors)-1 {
			b.WriteString(" ")
		}
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

func idxOf(r db.ModelStat, stats []db.ModelStat) int {
	for i, s := range stats {
		if s.Model == r.Model {
			return i
		}
	}
	return 0
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