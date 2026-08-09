// Package skills is the skills tab: a browsable list of the skills
// (.GoWork/skills/<name>/SKILL.md) found in the project, with the ones the
// current session has loaded marked. The list supports text search and
// all/loaded/unloaded filters, plus mouse scroll/click like the stats list.
// The prompt bar stays pinned below; typing in it and pressing Enter creates
// a new skill from that text.
package skills

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// Entry is one discovered skill row.
type Entry struct {
	Name        string
	Description string
	Path        string
	Loaded      bool
}

// SkillToggleMsg asks main.go to load/unload the named skill in the session
// (and refresh the model-visible tool list). Unload=false means load.
type SkillToggleMsg struct {
	Name   string
	Unload bool
}

// Filter selects which slice of the discovery the list shows.
type Filter int

const (
	FilterAll Filter = iota
	FilterLoaded
	FilterUnloaded
)

func (f Filter) String() string {
	switch f {
	case FilterLoaded:
		return "loaded"
	case FilterUnloaded:
		return "unloaded"
	default:
		return "all"
	}
}

const (
	padSide = 3
	botPad  = 1

	selCol  = 1 // ▸ marker
	dotCol  = 2 // ● / ○ loaded marker
	nameCol = 22
)

// Model is the skills list's state: the full discovery snapshot, the visible
// (filtered) rows, the cursor, the search input and the filter kind.
type Model struct {
	width  int
	height int

	all         []Entry // untouched snapshot from SetData
	items       []Entry // after filter + search
	cursor      int
	filtering   bool
	searchInput textinput.Model
	kind        Filter
}

func New() Model {
	ti := textinput.New()
	ti.Placeholder = "search skills..."
	ti.Prompt = "/ "
	ti.CharLimit = 64
	ti.SetWidth(30)
	return Model{searchInput: ti}
}

// SetData hands the tab a fresh discovery snapshot; the caller merges the
// session's loaded flags into each Entry. Called on tab entry and after
// every load/unload/create.
func (m *Model) SetData(entries []Entry) {
	m.all = make([]Entry, len(entries))
	copy(m.all, entries)
	m.applyFilter()
}

// SetSize clamps the pane to the band below the tabs.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.width < 1 {
		m.width = 1
	}
	if m.height < 1 {
		m.height = 1
	}
	m.clampCursor()
}

// CanExitSkillsMode reports whether Esc may leave the whole tab (nothing to
// dismiss first: no open search).
func (m Model) CanExitSkillsMode() bool {
	return !m.filtering
}

// Filtering reports whether the live search box is open (Enter is reserved
// to commit the search instead of focusing the prompt bar).
func (m Model) Filtering() bool { return m.filtering }

// --- filtering -----------------------------------------------------------

// applyFilter rebuilds m.items from m.all under the current search term +
// filter kind. Always a fresh slice: aliasing m.all would let a later append
// corrupt the underlying snapshot.
func (m *Model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
	kind := m.kind
	m.items = make([]Entry, 0, len(m.all))
	for _, e := range m.all {
		if kind == FilterLoaded && !e.Loaded {
			continue
		}
		if kind == FilterUnloaded && e.Loaded {
			continue
		}
		if q != "" {
			hay := strings.ToLower(e.Name + " " + e.Description)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		m.items = append(m.items, e)
	}
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if len(m.items) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Selected returns the currently highlighted row.
func (m Model) Selected() (Entry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return Entry{}, false
	}
	return m.items[m.cursor], true
}

func (m *Model) SetFilter(kind Filter) {
	m.kind = kind
	m.applyFilter()
}

func (m *Model) CycleFilter() {
	m.SetFilter((m.kind + 1) % 3)
}

// --- input ---------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		if m.filtering {
			switch key {
			case "esc":
				m.filtering = false
				m.searchInput.Blur()
				m.searchInput.SetValue("")
				m.applyFilter()
				return m, nil
			case "enter":
				// commit search, keep the text
				m.filtering = false
				m.searchInput.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}

		switch key {
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
		case "/":
			m.filtering = true
			m.searchInput.Focus()
			return m, nil
		case "a":
			m.SetFilter(FilterAll)
			return m, nil
		case "l":
			m.SetFilter(FilterLoaded)
			return m, nil
		case "u":
			m.SetFilter(FilterUnloaded)
			return m, nil
		case "f":
			m.CycleFilter()
			return m, nil
		case "x":
			if e, ok := m.Selected(); ok && e.Loaded {
				return m, toggleCmd(e.Name, true)
			}
			return m, nil
		case "p":
			if e, ok := m.Selected(); ok && !e.Loaded {
				return m, toggleCmd(e.Name, false)
			}
			return m, nil
		}

		// The 'i' key is main.go's door into the prompt bar; everything else
		// printable starts a search (shared with the '/' key).
		if key != "i" && msg.Key().Text != "" {
			m.filtering = true
			m.searchInput.Focus()
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}
	}
	return m, nil
}

func toggleCmd(name string, unload bool) tea.Cmd {
	return func() tea.Msg {
		return SkillToggleMsg{Name: name, Unload: unload}
	}
}

// HandleMouseClick routes a left click inside the skills area; y is local to
// the pane (row 0 = first row below the tabs).
func (m *Model) HandleMouseClick(x, y int) {
	rowsAvail, _ := m.listRows()
	top := m.listTop()
	row := y - headerRows()
	if row < 0 || row >= rowsAvail || row >= len(m.items) {
		return
	}
	m.cursor = top + row
	m.clampCursor()
}

// HandleMouseWheel scrolls the list on wheel events.
func (m *Model) HandleMouseWheel(up bool) {
	if up {
		if m.cursor > 0 {
			m.cursor--
		}
	} else if m.cursor < len(m.items)-1 {
		m.cursor++
	}
}

// --- geometry ------------------------------------------------------------

// contentWidth is the usable width of one panel's body, after the outer
// margin (padSide) and the two border columns.
func (m Model) contentWidth() int {
	w := m.width - padSide*2 - 4
	if w < 3 {
		w = 3
	}
	return w
}

// headerRows is the fixed number of rows above the list body: filter/hint
// line + header line.
func headerRows() int { return 2 }

// listRows returns how many entry rows fit and the lines the frame body
// holds.
func (m Model) listRows() (rows, lines int) {
	lines = m.height - botPad*2 - 2 // frame borders
	rows = lines - headerRows() - 1 // -1 keeps a footer info line
	if rows < 0 {
		rows = 0
	}
	return rows, lines
}

// listTop is the first visible item index.
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

// --- rendering ------------------------------------------------------------

func (m Model) View() string {
	frameW := m.width - padSide*2
	body := m.body()
	frame := drawFrame(frameW, m.height-botPad*2, body)

	view := lipgloss.NewStyle().
		Margin(0, padSide).
		Width(frameW).
		Render(frame)
	return view
}

func (m Model) body() []string {
	_, lines := m.listRows()
	rowsAvail, _ := m.listRows()
	innerW := m.contentWidth()

	// header: hint or live search input
	header := ""
	if m.filtering {
		header = lipgloss.NewStyle().Foreground(style.Info).Render(m.searchInput.View())
	} else {
		hint := "/ search · a all · l loaded · u unloaded · f cycle · p load · x unload · enter prompt · esc exit"
		header = lipgloss.NewStyle().Foreground(style.Muted).Render(hint)
	}

	// footer: counts + active filter (only when there is room)
	footer := m.counts(innerW)

	out := make([]string, 0, lines)
	out = append(out, clip(header, 0, innerW))

	if len(m.items) == 0 {
		empty := "no skills here"
		if m.kind == FilterLoaded {
			empty = "no loaded skills in this session"
		} else if m.kind == FilterUnloaded {
			empty = "every skill is loaded"
		}
		if strings.TrimSpace(m.searchInput.Value()) != "" {
			empty = "no skills match the search"
		}
		empty += " — type in the prompt bar and press enter to create one"
		out = append(out, clip("· "+empty, 0, innerW))
	} else {
		top := m.listTop()
		for i := 0; i < rowsAvail; i++ {
			idx := top + i
			if idx >= len(m.items) {
				out = append(out, strings.Repeat(" ", innerW))
				continue
			}
			out = append(out, m.row(m.items[idx], idx == m.cursor, innerW))
		}
	}

	out = append(out, footer)
	for len(out) < lines {
		out = append(out, strings.Repeat(" ", innerW))
	}
	if len(out) > lines {
		out = out[:lines]
	}
	return out
}

// counts renders the loaded/total status line (e.g. "2 of 5 skills loaded ·
// filter: all").
func (m Model) counts(innerW int) string {
	loaded, total := 0, len(m.all)
	for _, e := range m.all {
		if e.Loaded {
			loaded++
		}
	}
	kind := "filter: " + m.kind.String()
	if q := strings.TrimSpace(m.searchInput.Value()); q != "" {
		kind += " · search: “" + clip(q, 0, 24) + "”"
	}
	return clip(fmt.Sprintf("%d/%d skills loaded · %s", loaded, total, kind), 0, innerW)
}

// row renders one skill line: ▸ selection marker, ●/○ loaded dot, name and
// description with the description clipped to the remaining width.
func (m Model) row(e Entry, selected bool, innerW int) string {
	sel := " "
	if selected {
		sel = "▸"
	}
	selStyled := lipgloss.NewStyle().
		Foreground(style.Info).
		Width(selCol).
		Render(sel)

	dot := "○"
	if e.Loaded {
		dot = "●"
	}
	dotStyle := lipgloss.NewStyle().Width(dotCol).Render(dot)

	name := clip(e.Name, 0, nameCol)
	nameStyle := lipgloss.NewStyle().Width(nameCol)
	if e.Loaded {
		nameStyle = nameStyle.Bold(true).Foreground(style.Info)
	} else {
		nameStyle = nameStyle.Foreground(style.Text)
	}

	descW := innerW - selCol - 1 - dotCol - 1 - nameCol - 1
	if descW < 0 {
		descW = 0
	}
	desc := clip(e.Description, 0, descW)
	descStyle := lipgloss.NewStyle().
		Width(descW).
		Foreground(style.Muted).
		Render(desc)

	row := selStyled + " " + dotStyle + " " + nameStyle.Render(name) + " " + descStyle
	if selected {
		row = lipgloss.NewStyle().
			Background(style.Highlight).
			Foreground(style.Text).
			Render(row)
	}
	return row
}

// drawFrame renders the full bordered panel (same chrome as the stats
// panels). Body lines are padded/truncated to innerW (w-4) so ragged content
// can't blow out the frame.
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
