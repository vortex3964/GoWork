package changeslist

import (
	"context"
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
	label    string
	isHeader bool
	change   Change
	filePath string
	lowerFile string
}

const (
	padSide  = 3
	gapWidth = 1
)

var (
	headerColor = lipgloss.Color("111")
	rowColor    = style.Text
)

const (
	keyAccept = "cntrl+a"
	keyReject = "cntrl+r"
	keyAcceptAll = "ctrl+f"
	keyRejectAll = "cntrl+d"
)

type Model struct {
	Watch *WatchList

	filter   textarea.Model
	results  viewport.Model
	explorer *FileExplorer

	rows     []row
	filtered []row
	cursor   int

	open bool

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

	m := Model{
		Watch:    watch,
		filter:   ta,
		results:  rp,
		explorer: NewFileExplorer(),
	}
	m.rebuildRows()
	return m
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
	m.Watch.Changeslist[path] = ChangeList{difs}
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
			m.rows = append(m.rows, row{label: f, isHeader: true, filePath: f, lowerFile: strings.ToLower(f)})
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

// same outer size as message_area. Results and its filter bar form
// a left column; explorer is a separate right column with no filter
// bar under it, so it uses the full outer height while results gives
// up filterBarHeight worth of rows to make room underneath it.
func (m *Model) SetSize(outerWidth, outerHeight int) {
	m.width = outerWidth
	m.height = outerHeight

	inner := outerWidth - padSide*2
	if inner < 1 {
		inner = 1
	}

	const paneBorderRows = 2
	const filterBorderRows = 2
	const filterTextRows = 1

	filterBarHeight := filterBorderRows + filterTextRows

	resultsOuterHeight := outerHeight - filterBarHeight
	if resultsOuterHeight < 1 {
		resultsOuterHeight = 1
	}
	resultsContentHeight := resultsOuterHeight - paneBorderRows
	if resultsContentHeight < 1 {
		resultsContentHeight = 1
	}

	explorerContentHeight := outerHeight - paneBorderRows
	if explorerContentHeight < 1 {
		explorerContentHeight = 1
	}

	paneWidth := (inner - gapWidth) / 2
	if paneWidth < 1 {
		paneWidth = 1
	}

	m.results.SetWidth(paneWidth)
	m.results.SetHeight(resultsContentHeight)
	m.explorerWidth = inner - paneWidth - gapWidth
	m.explorerHeight = explorerContentHeight

	filterInner := paneWidth - 2 - 2
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
	if m.cursor > 0 {
		m.cursor--
		m.renderResults()
		m.renderExplorer()
	}
}

func (m *Model) CursorDown() {
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
		m.renderResults()
		m.renderExplorer()
	}
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

func (m *Model) AcceptSelected() {
	if len(m.filtered) == 0 || m.Watch == nil {
		return
	}
	sel := m.filtered[m.cursor]
	m.Watch.mu.Lock()
	cl, ok := m.Watch.Changeslist[sel.filePath]
	if !ok {
		m.Watch.mu.Unlock()
		return
	}
	if sel.isHeader {
		Accept_all_changes(sel.filePath, cl.Changes)
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
	} else {
		Accept_change(sel.filePath, sel.change.Start, sel.change.Mid, sel.change.End)
		difs, err := GetDiffs(sel.filePath)
		if err != nil || len(difs) == 0 {
			m.Watch.removeLocked(sel.filePath)
			delete(m.Watch.Changeslist, sel.filePath)
		} else {
			m.Watch.Changeslist[sel.filePath] = ChangeList{difs}
		}
	}
	if len(m.Watch.Changeslist[sel.filePath].Changes) == 0 {
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
	}
	m.Watch.mu.Unlock()
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.rebuildRows()
}

func (m *Model) RejectSelected() {
	if len(m.filtered) == 0 || m.Watch == nil {
		return
	}
	sel := m.filtered[m.cursor]
	m.Watch.mu.Lock()
	cl, ok := m.Watch.Changeslist[sel.filePath]
	if !ok {
		m.Watch.mu.Unlock()
		return
	}
	if sel.isHeader {
		Reject_all_changes(sel.filePath, cl.Changes)
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
	} else {
		Reject_change(sel.filePath, sel.change.Start, sel.change.Mid, sel.change.End)
		difs, err := GetDiffs(sel.filePath)
		if err != nil || len(difs) == 0 {
			m.Watch.removeLocked(sel.filePath)
			delete(m.Watch.Changeslist, sel.filePath)
		} else {
			m.Watch.Changeslist[sel.filePath] = ChangeList{difs}
		}
	}
	if len(m.Watch.Changeslist[sel.filePath].Changes) == 0 {
		m.Watch.removeLocked(sel.filePath)
		delete(m.Watch.Changeslist, sel.filePath)
	}
	m.Watch.mu.Unlock()
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.rebuildRows()
}

func (m *Model) AcceptAll() {
	m.Watch.mu.Lock()
	for path, cl := range m.Watch.Changeslist {
		if len(cl.Changes) > 0 {
			Accept_all_changes(path, cl.Changes)
		}
	}
	m.Watch.Changeslist = make(map[string]ChangeList)
	m.Watch.WatchedFiles = make(map[string]struct{})
	m.Watch.mu.Unlock()
	m.rebuildRows()
}

func (m *Model) RejectAll() {
	m.Watch.mu.Lock()
	for path, cl := range m.Watch.Changeslist {
		if len(cl.Changes) > 0 {
			Reject_all_changes(path, cl.Changes)
		}
	}
	m.Watch.Changeslist = make(map[string]ChangeList)
	m.Watch.WatchedFiles = make(map[string]struct{})
	m.Watch.mu.Unlock()
	m.rebuildRows()
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

func (m Model) View() string {
	filterBar := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		PaddingLeft(1).
		PaddingRight(1).
		Render(m.filter.View())

	leftColumn := lipgloss.JoinVertical(lipgloss.Left, m.results.View(), filterBar)

	expWidth := m.explorerWidth - 4
	expHeight := m.explorerHeight - 2
	if expWidth < 1 {
		expWidth = 1
	}
	if expHeight < 1 {
		expHeight = 1
	}
	expView := m.explorer.View(expWidth, expHeight)
	expPane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		PaddingLeft(1).
		PaddingRight(1).
		Render(expView)

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftColumn,
		strings.Repeat(" ", gapWidth),
		expPane,
	)

	return lipgloss.NewStyle().Margin(0, padSide).Render(content)
}
