package changeslist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

type WatcherEventMsg struct {
	FilePath string
}

// row is either a file header line or one of its changes (Id is
// already "file/Cn" from GetDiffs).
type row struct {
	label     string
	isHeader  bool
	change    Change
	filePath  string
	lowerFile string
}

const (
	padSide  = 3
	gapWidth = 1
)

// botPad matches message_area's 1-row top/bottom breathing space so both
// panes occupy the same vertical budget (see SetSize).
const botPad = 1

var (
	headerColor = lipgloss.Color("111")
	rowColor    = style.Text
)

const (
	keyAccept    = "cntrl+a"
	keyReject    = "cntrl+r"
	keyAcceptAll = "ctrl+f"
	keyRejectAll = "cntrl+d"
)

type Model struct {
	Watch *WatchList

	// root is the project root; file paths are displayed relative to it.
	root string

	filter   textarea.Model
	results  viewport.Model
	explorer *FileExplorer

	rows     []row
	filtered []row
	cursor   int

	open     bool
	showHelp bool

	// filterBarHeight is how many rows the filter input currently occupies
	// (3 with its border, 0 once the window gets too short to afford it).
	filterBarHeight int

	width          int
	height         int
	explorerWidth  int
	explorerHeight int

	watchCtx    context.Context
	watchCancel context.CancelFunc

	lastQuery string

	lastWatcherPath string
	lastWatcherData []byte
}

func New(watch *WatchList) Model {
	ta := textarea.New()
	ta.Placeholder = "filter files ..."
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	state := textarea.StyleState{
		Text:        lipgloss.NewStyle().Foreground(style.Text),
		Placeholder: lipgloss.NewStyle().Foreground(style.Muted).Italic(true),
		CursorLine:  lipgloss.NewStyle(),
	}
	ta.SetStyles(textarea.Styles{
		Focused: state,
		Blurred: state,
		Cursor: textarea.CursorStyle{
			Color: style.Primary,
			Blink: true,
		},
	})
	ta.Blur()

	rp := viewport.New()
	rp.Style = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		PaddingLeft(1).
		PaddingRight(1)
	// Always render a full panel (padding with blank rows) so the left
	// column's frame stays exactly as tall as the layout expects instead of
	// collapsing when the filtered list is shorter than the pane.
	rp.FillHeight = true

	m := Model{
		Watch:    watch,
		filter:   ta,
		results:  rp,
		explorer: NewFileExplorer(""),
	}
	m.rebuildRows()
	return m
}

// SetRoot configures the project root used to display file paths relative
// to the project instead of absolute.
func (m *Model) SetRoot(root string) {
	m.root = root
	m.explorer.root = root
}

// TrackFile registers path in the watch list and scans it for diff markers
// immediately, so it shows up in the changes list right away instead of
// waiting for the next fsnotify event on it. Returns false if the file can't
// be read or isn't already watched.
func (m *Model) TrackFile(path string) bool {
	if m.Watch == nil {
		return false
	}
	difs, err := GetDiffs(path)
	if err != nil {
		return false
	}
	m.Watch.mu.Lock()
	m.Watch.addLocked(path)
	m.Watch.Changeslist[path] = ChangeList{difs}
	m.Watch.addDirToWatcherLocked(path)
	m.Watch.mu.Unlock()
	m.rebuildRows()
	return true
}

func (m *Model) RefreshDiffs() {
	if m.Watch == nil {
		return
	}
	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	for file := range m.Watch.WatchedFiles {
		difs, err := GetDiffs(file)
		if err != nil {
			m.Watch.removeLocked(file)
			delete(m.Watch.Changeslist, file)
			continue
		}
		m.Watch.Changeslist[file] = ChangeList{difs}
		m.Watch.addDirToWatcherLocked(file)
	}
}

func (m *Model) PauseWatching() {
	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	// The watcher handler already skips rebuilding the list while the AI is
	// thinking (*m.aiThink), so pausing here is a no-op by design. Kept as a
	// named hook so callers document their intent; if list-live-updates
	// during AI turns are ever wanted, this is where they'd be gated.
}

func (m *Model) WatchCmd() tea.Cmd {
	if m.Watch == nil || m.Watch.Watcher == nil {
		return nil
	}
	if m.watchCtx == nil || m.watchCtx.Err() != nil {
		ctx, cancel := context.WithCancel(context.Background())
		m.watchCtx = ctx
		m.watchCancel = cancel
	}
	return watcherCmd(m.Watch, m.watchCtx)
}

func watcherCmd(w *WatchList, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-w.events:
			return msg
		case <-ctx.Done():
			return nil
		}
	}
}

// HandleWatcherEvent re-scans a watched file after an external write (an
// editor, git, the AI's own tools) and refreshes the change list for the
// next rebuild, so the list shows whatever the write left behind. The
// explorer picks the new content up on its own when it re-renders (it
// compares the on-disk bytes/mtime against what it last drew). The bool
// reports whether the event addressed a watched file at all.
func (m *Model) HandleWatcherEvent(eventPath string) bool {
	path, ok := m.Watch.hasWatchedFile(eventPath)
	if !ok {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	difs := GetDiffsBytes(data, filepath.Base(path))
	m.Watch.mu.Lock()
	m.Watch.Changeslist[path] = ChangeList{Changes: difs}
	m.Watch.mu.Unlock()
	m.lastWatcherPath = path
	m.lastWatcherData = data
	return true
}

func (m *Model) RebuildRows() {
	m.rebuildRows()
}

func (m *Model) Toggle() tea.Cmd {
	m.open = !m.open
	if m.open {
		m.filter.Reset()
		m.RefreshDiffs()
		m.rebuildRows()
		return m.filter.Focus()
	}
	m.filter.Blur()
	return nil
}

func (m Model) Open() bool {
	return m.open
}

func (m *Model) Close() {
	m.open = false
	m.filter.Blur()
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
}

func (m *Model) rebuildRows() {
	m.lastQuery = "\x00"
	m.rows = m.rows[:0]
	if m.Watch != nil {
		files := m.Watch.Files()
		sort.Strings(files)
		for _, f := range files {
			label := relativePath(m.root, f)
			m.rows = append(m.rows, row{label: label, isHeader: true, filePath: f, lowerFile: strings.ToLower(f)})
			for _, c := range m.Watch.GetChanges(f) {
				m.rows = append(m.rows, row{label: c.Id, change: c, filePath: f, lowerFile: strings.ToLower(f)})
			}
		}
	}
	m.lastWatcherPath = ""
	m.lastWatcherData = nil
	m.applyFilter()
}

func (m *Model) applyFilter() {
	q := strings.TrimSpace(m.filter.Value())
	if q == m.lastQuery {
		return
	}
	m.lastQuery = q

	if q == "" {
		m.filtered = m.rows
	} else {
		lowerQ := strings.ToLower(q)
		m.filtered = m.filtered[:0]
		for _, r := range m.rows {
			if strings.Contains(r.lowerFile, lowerQ) {
				m.filtered = append(m.filtered, r)
			}
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.renderResults()
	m.renderExplorer()
}

func (m *Model) SetSize(outerWidth, outerHeight int) {
	outerHeight -= botPad * 2
	if outerHeight < 1 {
		outerHeight = 1
	}

	m.width = outerWidth
	m.height = outerHeight
	if m.width < 1 {
		m.width = 1
	}
	if m.height < 1 {
		m.height = 1
	}

	inner := outerWidth - padSide*2
	if inner < 1 {
		inner = 1
	}

	// The filter bar gives up its 3 rows wholesale once the window gets too
	// short to afford it. Dropping it keeps the two panes from eating into
	// each other (or past the bottom) on tiny windows.
	m.filterBarHeight = 3
	if m.height < 13 {
		m.filterBarHeight = 0
	}

	paneWidth := (inner - gapWidth) / 2
	if paneWidth < 1 {
		paneWidth = 1
	}

	// The results viewport is sized so result panel + filter bar exactly
	// fill the column; the explorer column matches the same height.
	m.results.SetWidth(paneWidth)
	m.results.SetHeight(m.height - m.filterBarHeight)

	m.explorerWidth = inner - paneWidth - gapWidth
	if m.explorerWidth < 1 {
		m.explorerWidth = 1
	}
	m.explorerHeight = m.height
	m.explorer.screenWidth = m.width

	filterInner := paneWidth - 4
	if filterInner < 1 {
		filterInner = 1
	}
	m.filter.SetWidth(filterInner)

	m.renderResults()
	m.renderExplorer()
}

func (m *Model) renderResults() {
	w := m.results.Width() - m.results.Style.GetHorizontalFrameSize()
	if w < 1 {
		w = 1
	}
	var b strings.Builder
	for i, r := range m.filtered {
		line := r.label
		lineStyle := lipgloss.NewStyle().Width(w)
		switch {
		case r.isHeader:
			lineStyle = lineStyle.Bold(true).Foreground(headerColor)
		default:
			lineStyle = lineStyle.Foreground(rowColor).PaddingLeft(2)
			line = "▸ " + shortChangeLabel(r)
		}
		if i == m.cursor {
			lineStyle = lineStyle.Background(style.Highlight)
		}
		b.WriteString(lineStyle.Render(line))
		b.WriteString("\n")
	}
	m.results.SetContent(strings.TrimRight(b.String(), "\n"))

	if len(m.filtered) > 0 {
		m.results.EnsureVisible(m.cursor, 0, 1)
	}
}

// strips the file path off, so we just show "Cn" under the header
func shortChangeLabel(r row) string {
	if idx := strings.LastIndex(r.change.Id, "/"); idx != -1 {
		return r.change.Id[idx+1:]
	}
	return r.change.Id
}

func (m *Model) renderExplorer() {
	if len(m.filtered) == 0 {
		m.explorer.Clear()
		return
	}
	sel := m.filtered[m.cursor]
	var cached []byte
	if sel.filePath == m.lastWatcherPath {
		cached = m.lastWatcherData
	}
	if sel.change.Id != "" {
		m.explorer.Load(sel.filePath, &sel.change, cached)
	} else {
		m.explorer.Load(sel.filePath, nil, cached)
	}
}

func (m *Model) CursorUp() {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor--
	if m.cursor < 0 {
		m.cursor = len(m.filtered) - 1
	}
	m.renderResults()
	m.renderExplorer()
}

func (m *Model) CursorDown() {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor++
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
	m.renderResults()
	m.renderExplorer()
}

func (m *Model) ExplorerScrollUp() {
	m.explorer.ScrollUp(3)
}

func (m *Model) ExplorerScrollDown() {
	m.explorer.ScrollDown(3)
}

func (m *Model) acceptChange(path string, c Change) {
	Accept_change(path, c.Start, c.Mid, c.End)
	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	difs, err := GetDiffs(path)
	if err != nil || len(difs) == 0 {
		m.Watch.removeLocked(path)
		delete(m.Watch.Changeslist, path)
	} else {
		m.Watch.Changeslist[path] = ChangeList{difs}
	}
}

func (m *Model) rejectChange(path string, c Change) {
	Reject_change(path, c.Start, c.Mid, c.End)
	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	difs, err := GetDiffs(path)
	if err != nil || len(difs) == 0 {
		m.Watch.removeLocked(path)
		delete(m.Watch.Changeslist, path)
	} else {
		m.Watch.Changeslist[path] = ChangeList{difs}
	}
}

// ChangeEventMsg is emitted after an accept/reject action. 
type ChangeEventMsg struct {
	Rejected bool
	Summary  string
	Lines []string
}

// changeCountLabel pluralizes "change" so summaries read naturally.
func changeCountLabel(n int) string {
	if n == 1 {
		return "1 change"
	}
	return fmt.Sprintf("%d changes", n)
}

func (m *Model) eventCmd(rejected bool, summary string, lines []string) tea.Cmd {
	return func() tea.Msg {
		return ChangeEventMsg{Rejected: rejected, Summary: summary, Lines: lines}
	}
}

// splitContentLines trims a hunk half's trailing newlines and splits it into
// display lines; nil for empty content.
func splitContentLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (m *Model) AcceptSelected() tea.Cmd {
	if len(m.filtered) == 0 || m.Watch == nil {
		return nil
	}
	sel := m.filtered[m.cursor]

	m.Watch.mu.Lock()
	cl, ok := m.Watch.Changeslist[sel.filePath]
	if !ok {
		m.Watch.mu.Unlock()
		return nil
	}

	var summary string
	var kept []string
	if sel.isHeader {
		data, err := os.ReadFile(sel.filePath)
		if err == nil {
			for _, c := range cl.Changes {
				_, new := ChangeSections(data, c)
				kept = append(kept, splitContentLines(new)...)
			}
		}
		Accept_all_changes(sel.filePath, cl.Changes)
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
		summary = sel.filePath + " (" + changeCountLabel(len(cl.Changes)) + ")"
	} else {
		data, err := os.ReadFile(sel.filePath)
		if err == nil {
			_, new := ChangeSections(data, sel.change)
			kept = splitContentLines(new)
		}
		Accept_change(sel.filePath, sel.change.Start, sel.change.Mid, sel.change.End)
		difs, err := GetDiffs(sel.filePath)
		if err != nil || len(difs) == 0 {
			m.Watch.removeLocked(sel.filePath)
			delete(m.Watch.Changeslist, sel.filePath)
		} else {
			m.Watch.Changeslist[sel.filePath] = ChangeList{difs}
		}
		summary = sel.filePath + "/" + shortChangeLabel(sel)
	}
	if len(m.Watch.Changeslist[sel.filePath].Changes) == 0 {
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
	}
	m.Watch.mu.Unlock()

	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.explorer.Invalidate()
	m.rebuildRows()
	return m.eventCmd(false, summary, kept)
}

func (m *Model) RejectSelected() tea.Cmd {
	if len(m.filtered) == 0 || m.Watch == nil {
		return nil
	}
	sel := m.filtered[m.cursor]

	m.Watch.mu.Lock()
	cl, ok := m.Watch.Changeslist[sel.filePath]
	if !ok {
		m.Watch.mu.Unlock()
		return nil
	}

	var summary string
	var kept []string
	if sel.isHeader {
		data, err := os.ReadFile(sel.filePath)
		if err == nil {
			for _, c := range cl.Changes {
				old, _ := ChangeSections(data, c)
				kept = append(kept, splitContentLines(old)...)
			}
		}
		Reject_all_changes(sel.filePath, cl.Changes)
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
		summary = sel.filePath + " (" + changeCountLabel(len(cl.Changes)) + ")"
	} else {
		data, err := os.ReadFile(sel.filePath)
		if err == nil {
			old, _ := ChangeSections(data, sel.change)
			kept = splitContentLines(old)
		}
		Reject_change(sel.filePath, sel.change.Start, sel.change.Mid, sel.change.End)
		difs, err := GetDiffs(sel.filePath)
		if err != nil || len(difs) == 0 {
			m.Watch.removeLocked(sel.filePath)
			delete(m.Watch.Changeslist, sel.filePath)
		} else {
			m.Watch.Changeslist[sel.filePath] = ChangeList{difs}
		}
		summary = sel.filePath + "/" + shortChangeLabel(sel)
	}
	if len(m.Watch.Changeslist[sel.filePath].Changes) == 0 {
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
	}
	m.Watch.mu.Unlock()

	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.explorer.Invalidate()
	m.rebuildRows()
	return m.eventCmd(true, summary, kept)
}

func (m *Model) AcceptAll() tea.Cmd {
	m.Watch.mu.Lock()
	files, total := 0, 0
	for path, cl := range m.Watch.Changeslist {
		if len(cl.Changes) > 0 {
			Accept_all_changes(path, cl.Changes)
			files++
			total += len(cl.Changes)
		}
	}
	m.Watch.Changeslist = make(map[string]ChangeList)
	m.Watch.WatchedFiles = make(map[string]struct{})
	m.Watch.absWatched = make(map[string]string)
	m.Watch.mu.Unlock()

	m.explorer.Invalidate()
	m.rebuildRows()
	if total == 0 {
		return nil
	}
	return m.eventCmd(false, fmt.Sprintf("%s across %d files", changeCountLabel(total), files), nil)
}

func (m *Model) RejectAll() tea.Cmd {
	m.Watch.mu.Lock()
	files, total := 0, 0
	for path, cl := range m.Watch.Changeslist {
		if len(cl.Changes) > 0 {
			Reject_all_changes(path, cl.Changes)
			files++
			total += len(cl.Changes)
		}
	}
	m.Watch.Changeslist = make(map[string]ChangeList)
	m.Watch.WatchedFiles = make(map[string]struct{})
	m.Watch.absWatched = make(map[string]string)
	m.Watch.mu.Unlock()

	m.explorer.Invalidate()
	m.rebuildRows()
	if total == 0 {
		return nil
	}
	return m.eventCmd(true, fmt.Sprintf("%s across %d files", changeCountLabel(total), files), nil)
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.open {
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.applyFilter()
	return m, cmd
}

func (m *Model) ToggleHelp() {
	m.showHelp = !m.showHelp
}

func (m Model) View() string {
	if m.showHelp {
		return m.helpView()
	}

	var left strings.Builder
	left.WriteString(m.results.View())
	if m.filterBarHeight > 0 {
		left.WriteString("\n")
		left.WriteString(m.renderFilterBar())
	}

	expPane := m.explorer.View(m.explorerWidth, m.explorerHeight)

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		left.String(),
		strings.Repeat(" ", gapWidth),
		expPane,
	)

	// Last-resort clamp: even if a child renders a row too many, the joined
	// panes can't spill past the space allocated to the whole component.
	return lipgloss.NewStyle().
		Margin(0, padSide).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(content)
}

func (m Model) renderFilterBar() string {
	st := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		PaddingLeft(1).
		PaddingRight(1)
	if m.filterBarHeight >= 3 {
		return st.Render(m.filter.View())
	}
	return lipgloss.NewStyle().
		Foreground(style.Muted).
		MaxWidth(max(1, m.results.Width())).
		Render(m.filter.View())
}

// helpView replaces the panes with a centered keybinds reference.
func (m Model) helpView() string {
	keys := [][2]string{
		{"up / down", "move selection"},
		{"ctrl+a", "accept selected change"},
		{"ctrl+r", "reject selected change"},
		{"ctrl+f", "accept all changes"},
		{"ctrl+d", "reject all changes"},
		{"type", "filter files by path"},
		{"?", "toggle this help"},
		{"esc / tab / enter", "close changes list"},
		{"ctrl+l", "toggle changes list"},
		{"ctrl+p", "pick provider / model"},
		{"ctrl+i / tab", "interrupt the AI"},
		{"up (empty prompt)", "recall last prompt"},
		{"ctrl+a (prompt)", "copy prompt"},
		{"ctrl+v", "paste into prompt"},
		{"ctrl+u", "clear prompt"},
		{"ctrl+c", "quit"},
	}

	keyW := 0
	for _, row := range keys {
		if w := lipgloss.Width(row[0]); w > keyW {
			keyW = w
		}
	}

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(style.Primary).Render("keybinds"))
	b.WriteString("\n")
	for _, row := range keys {
		b.WriteString(lipgloss.NewStyle().
			Foreground(style.Muted).
			Width(keyW).
			Render(row[0]))
		b.WriteString("  ")
		b.WriteString(lipgloss.NewStyle().
			Foreground(style.Text).
			Render(row[1]))
		b.WriteString("\n")
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		Padding(1, 2).
		Render(strings.TrimRight(b.String(), "\n"))

	w, h := m.width-(padSide*2), m.height
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	return lipgloss.NewStyle().
		Margin(0, padSide).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box))
}
