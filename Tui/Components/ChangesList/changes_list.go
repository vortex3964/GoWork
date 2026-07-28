package changeslist

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// row is either a file header line or one of its changes (Id is
// already "file/Cn" from GetDiffs).
type row struct {
	label    string
	isHeader bool
	change   Change
	filePath string
}

const (
	padSide  = 3
	gapWidth = 1
)

var (
	headerColor = lipgloss.Color("111")
	rowColor    = style.Text
	mutedColor  = style.Muted
)

type Model struct {
	watch *WatchList

	filter   textarea.Model
	results  viewport.Model
	explorer viewport.Model

	rows     []row
	filtered []row
	cursor   int

	open bool

	width  int
	height int
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

	ep := viewport.New()
	ep.Style = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		PaddingLeft(1).
		PaddingRight(1)

	m := Model{
		watch:    watch,
		filter:   ta,
		results:  rp,
		explorer: ep,
	}
	m.rebuildRows()
	return m
}

func (m *Model) Toggle() tea.Cmd {
	m.open = !m.open
	if m.open {
		m.filter.Reset()
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
}

func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	if m.watch != nil {
		for _, f := range m.watch.Files() {
			m.rows = append(m.rows, row{label: f, isHeader: true, filePath: f})
			for _, c := range m.watch.GetChanges(f) {
				m.rows = append(m.rows, row{label: c.Id, change: c, filePath: f})
			}
		}
	}
	m.applyFilter()
}

func (m *Model) applyFilter() {
	q := strings.TrimSpace(m.filter.Value())
	if q == "" {
		m.filtered = m.rows
	} else {
		m.filtered = m.filtered[:0]
		for _, r := range m.rows {
			if strings.Contains(strings.ToLower(r.filePath), strings.ToLower(q)) {
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
	m.explorer.SetWidth(inner - paneWidth - gapWidth)
	m.explorer.SetHeight(explorerContentHeight)

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
		if !r.isHeader && i == m.cursor {
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

// just a placeholder for now, shows the path of whatever's selected
func (m *Model) renderExplorer() {
	if len(m.filtered) == 0 {
		m.explorer.SetContent(lipgloss.NewStyle().Foreground(mutedColor).Render("no file selected"))
		return
	}
	sel := m.filtered[m.cursor]
	body := lipgloss.NewStyle().Foreground(mutedColor).Render(sel.filePath)
	m.explorer.SetContent(body)
}

// skip header rows, only changes are selectable
func (m *Model) CursorUp() {
	for i := m.cursor - 1; i >= 0; i-- {
		if !m.filtered[i].isHeader {
			m.cursor = i
			m.renderResults()
			m.renderExplorer()
			return
		}
	}
}

func (m *Model) CursorDown() {
	for i := m.cursor + 1; i < len(m.filtered); i++ {
		if !m.filtered[i].isHeader {
			m.cursor = i
			m.renderResults()
			m.renderExplorer()
			return
		}
	}
}

func (m *Model) ExplorerScrollUp() {
	m.explorer.ScrollUp(m.explorer.MouseWheelDelta)
}

func (m *Model) ExplorerScrollDown() {
	m.explorer.ScrollDown(m.explorer.MouseWheelDelta)
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

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftColumn,
		strings.Repeat(" ", gapWidth),
		m.explorer.View(),
	)

	return lipgloss.NewStyle().Margin(0, padSide).Render(content)
}
