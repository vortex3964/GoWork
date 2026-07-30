package changeslist

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

type diffState int

const (
	stateNormal diffState = iota
	stateOld
	stateNew
)

type FileExplorer struct {
	filePath string
	lines []string
	lineStates []diffState
	viewStart int
	width int
	height int
	totalLines int
	jumpLine int
}

var (
	oldBgStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3B1A1A"))
	newBgStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1A3B1A"))
	markerStyle = lipgloss.NewStyle().
			Foreground(style.Muted).
			Italic(true)
	normalStyle = lipgloss.NewStyle().
			Foreground(style.Text)
)

func NewFileExplorer() *FileExplorer {
	return &FileExplorer{
		viewStart: 0,
		jumpLine:  -1,
	}
}

func (fe *FileExplorer) Load(path string, change *Change, cached []byte) {
	if path == fe.filePath && fe.lines != nil {
		fe.jumpTo(change)
		return
	}

	fe.filePath = path
	fe.lines = nil
	fe.lineStates = nil
	fe.viewStart = 0
	fe.jumpLine = -1
	fe.totalLines = 0

	var data []byte
	var err error
	if cached != nil {
		data = cached
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return
		}
	}

	if len(data) == 0 {
		fe.lines = []string{""}
		fe.lineStates = []diffState{stateNormal}
		fe.totalLines = 1
		return
	}

	fe.lines = strings.Split(string(data), "\n")
	if len(fe.lines) > 0 && fe.lines[len(fe.lines)-1] == "" {
		fe.lines = fe.lines[:len(fe.lines)-1]
	}
	if len(fe.lines) == 0 {
		fe.lines = []string{""}
	}
	fe.totalLines = len(fe.lines)

	fe.computeLineStates()
	fe.jumpTo(change)
}

func (fe *FileExplorer) jumpTo(change *Change) {
	if change == nil {
		fe.jumpLine = -1
		fe.viewStart = 0
		return
	}
	offset := change.Start
	lineNum := 0
	pos := 0
	for i, line := range fe.lines {
		if pos >= offset {
			lineNum = i
			break
		}
		pos += len(line) + 1
		lineNum = i + 1
	}
	if lineNum >= fe.totalLines {
		lineNum = fe.totalLines - 1
	}
	fe.jumpLine = lineNum
	fe.viewStart = lineNum - fe.height/3
	if fe.viewStart < 0 {
		fe.viewStart = 0
	}
}

func (fe *FileExplorer) computeLineStates() {
	fe.lineStates = make([]diffState, len(fe.lines))
	state := stateNormal
	for i, line := range fe.lines {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			state = stateOld
			fe.lineStates[i] = stateNormal
		case strings.HasPrefix(line, "======="):
			state = stateNew
			fe.lineStates[i] = stateNormal
		case strings.HasPrefix(line, ">>>>>>>"):
			state = stateNormal
			fe.lineStates[i] = stateNormal
		default:
			fe.lineStates[i] = state
		}
	}
}

func (fe *FileExplorer) SetSize(width, height int) {
	fe.width = width
	fe.height = height
	if fe.jumpLine >= 0 {
		fe.viewStart = fe.jumpLine - height/3
		if fe.viewStart < 0 {
			fe.viewStart = 0
		}
	}
}

func (fe *FileExplorer) ScrollUp(n int) {
	fe.viewStart -= n
	if fe.viewStart < 0 {
		fe.viewStart = 0
	}
	if fe.jumpLine >= 0 {
		fe.jumpLine = -1
	}
}

func (fe *FileExplorer) ScrollDown(n int) {
	fe.viewStart += n
	if fe.viewStart >= fe.totalLines {
		fe.viewStart = fe.totalLines - 1
	}
	if fe.viewStart < 0 {
		fe.viewStart = 0
	}
	if fe.jumpLine >= 0 {
		fe.jumpLine = -1
	}
}

func isMarkerLine(line string) bool {
	return strings.HasPrefix(line, "<<<<<<<") ||
		strings.HasPrefix(line, "=======") ||
		strings.HasPrefix(line, ">>>>>>>")
}

func (fe *FileExplorer) View(width, height int) string {
	fe.SetSize(width, height)

	if len(fe.lines) == 0 {
		return lipgloss.NewStyle().Foreground(style.Muted).Render("no file selected")
	}

	contentWidth := width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	if height < 1 {
		height = 1
	}

	end := fe.viewStart + height
	if end > fe.totalLines {
		end = fe.totalLines
	}

	var b strings.Builder
	for i := fe.viewStart; i < end; i++ {
		line := fe.lines[i]
		if len(line) > contentWidth {
			line = line[:contentWidth]
		}

		if isMarkerLine(line) {
			b.WriteString(" ")
			b.WriteString(markerStyle.Width(contentWidth).Render(line))
			b.WriteString("\n")
			continue
		}

		switch fe.lineStates[i] {
		case stateOld:
			b.WriteString(" ")
			b.WriteString(oldBgStyle.Width(contentWidth).Render(line))
		case stateNew:
			b.WriteString(" ")
			b.WriteString(newBgStyle.Width(contentWidth).Render(line))
		default:
			b.WriteString(" ")
			b.WriteString(normalStyle.Width(contentWidth).Render(line))
		}
		b.WriteString("\n")
	}

	// Fill remaining lines
	for i := end; i < fe.viewStart+height; i++ {
		b.WriteString(" ")
		b.WriteString(strings.Repeat(" ", contentWidth))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func (fe *FileExplorer) Clear() {
	fe.filePath = ""
	fe.lines = nil
	fe.lineStates = nil
	fe.viewStart = 0
	fe.jumpLine = -1
	fe.totalLines = 0
}
