package changeslist

import (
	"bytes"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"

	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

type diffState int

const (
	stateNormal diffState = iota
	stateOld
	stateNew
)

// maxHighlightBytes caps how large a file we'll syntax-highlight. Tokenizing
// a huge file once per load is both slow and memory heavy; beyond this the
// file renders as plain text.
const maxHighlightBytes = 256 * 1024

// Path-name display limits, in display columns, keyed to the terminal width.
// Tune these at will.
const (
	smallScreenMax  = 80
	mediumScreenMax = 140
	pathCharsSmall  = 20
	pathCharsMedium = 50
	pathCharsLarge  = 0

	// pathNameCap is the absolute ceiling on header name cells regardless
	// of screen size: maxName = min(inner-derived, pathChars limit, cap).
	pathNameCap = 100
)

// maxPathChars returns how many display columns a path may occupy for the
// given terminal width.
func maxPathChars(screenWidth int) int {
	switch {
	case screenWidth < smallScreenMax:
		return pathCharsSmall
	case screenWidth < mediumScreenMax:
		return pathCharsMedium
	default:
		return pathCharsLarge
	}
}

// truncateCells cuts s down to at most max display columns (rune-safe),
// preserving the head and tail with an ellipsis in between. max <= 0 means
// no truncation.
func truncateCells(s string, max int) string {
	if max <= 0 || lipgloss.Width(s) <= max {
		return s
	}
	const ellipsis = "…"
	headMax := (max - lipgloss.Width(ellipsis)) / 2
	tailMax := max - lipgloss.Width(ellipsis) - headMax

	runes := []rune(s)
	var head, tail []rune
	headW, tailW := 0, 0
	for _, r := range runes {
		if w := lipgloss.Width(string(r)); headW+w <= headMax {
			head = append(head, r)
			headW += w
		} else {
			break
		}
	}
	for i := len(runes) - 1; i >= 0 && i >= len(head); i-- {
		w := lipgloss.Width(string(runes[i]))
		if tailW+w <= tailMax {
			tail = append([]rune{runes[i]}, tail...)
			tailW += w
		} else {
			break
		}
	}
	return string(head) + ellipsis + string(tail)
}

// relativePath rewrites an absolute path to start from the project root;
// paths that are already relative (or outside the root) come back as-is.
func relativePath(root, path string) string {
	if root == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

type FileExplorer struct {
	filePath   string
	lines      []string
	lineStates []diffState
	viewStart  int
	width      int
	height     int
	totalLines int
	jumpLine   int

	// root is the project root used to display paths relative to the
	// project instead of absolute; screenWidth is the terminal width that
	// decides how much of the path fits.
	root        string
	screenWidth int

	// styledLines carries the chroma-highlighted rendering of each line for
	// files whose syntax we know; nil means "render plainly".
	styledLines []string

	data     []byte
	modNanos int64

	display   []string
	lineStart []int
	textW     int
}

var (
	// Old/new halves keep their dark red/green bands, but their text is tinted
	// so a diff stays readable even for plain-text files that chroma can't
	// color (e.g. .txt fixtures), where the old content would otherwise read
	// as flat grey.
	oldBgStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3B1A1A")).
			Foreground(lipgloss.Color("#FF9E9E"))
	newBgStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1A3B1A")).
			Foreground(style.Success)
	markerStyle = lipgloss.NewStyle().
			Foreground(style.Muted).
			Italic(true)
	// markerOldStyle/markerNewStyle tint the diff markers themselves so the
	// old/base and new sides are recognizable at a glance (muted greys
	// otherwise).
	markerOldStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C27070")).
			Italic(true)
	markerNewStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6FBF7F")).
			Italic(true)
	normalStyle = lipgloss.NewStyle().
			Foreground(style.Text)
	emptyStyle = lipgloss.NewStyle().
			Foreground(style.Muted)
	// borderStyle matches the changes list panes' border color so the
	// explorer's hand-drawn frame reads as part of the same component.
	borderStyle = lipgloss.NewStyle().Foreground(style.Muted)
	// infoLineStyle colors the top and bottom frame lines (the file's name
	// strip and the base line) with the app's info accent.
	infoLineStyle = lipgloss.NewStyle().Foreground(style.Info)
)

// syntaxHex flattens a palette color to a chroma-compatible "#RRGGBB"
// string (the tokens live on the image/color interface lipgloss colors use).
func syntaxHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// syntaxChromaStyle maps the app's palette onto chroma token types so the
// explorer's syntax highlighting uses the same colors as the rest of the TUI.
// Built once since chroma.NewStyle parses the entries.
var syntaxChromaStyle = mustSyntaxStyle()

func mustSyntaxStyle() *chroma.Style {
	// chroma.NewStyle can't realistically fail for these fixed entries; on the
	// off chance it does we just fall back to an empty style, which makes the
	// formatter emit no colors (plain text) instead of crashing the viewer.
	styled, err := chroma.NewStyle("gowork", chroma.StyleEntries{
		chroma.Comment:         syntaxHex(style.Muted),
		chroma.Keyword:         syntaxHex(style.Info),
		chroma.KeywordType:     syntaxHex(style.Special),
		chroma.KeywordConstant: syntaxHex(style.Warning),
		chroma.Operator:        syntaxHex(style.Danger),
		chroma.Punctuation:     syntaxHex(style.Text),
		chroma.Name:            syntaxHex(style.Text),
		chroma.NameBuiltin:     syntaxHex(style.Special),
		chroma.NameFunction:    syntaxHex(style.Warning),
		chroma.NameClass:       "bold " + syntaxHex(style.Primary),
		chroma.NameConstant:    syntaxHex(style.Special),
		chroma.NameAttribute:   syntaxHex(style.Warning),
		chroma.NameDecorator:   syntaxHex(style.Warning),
		chroma.NameTag:         syntaxHex(style.Info),
		chroma.LiteralString:   syntaxHex(style.Success),
		chroma.LiteralNumber:   syntaxHex(style.Special),
		chroma.GenericDeleted:  syntaxHex(style.Danger),
		chroma.GenericInserted: syntaxHex(style.Success),
	})
	if err != nil {
		styled, _ = chroma.NewStyle("gowork", chroma.StyleEntries{})
	}
	return styled
}

func NewFileExplorer(root string) *FileExplorer {
	return &FileExplorer{
		root:      root,
		viewStart: 0,
		jumpLine:  -1,
	}
}

func (fe *FileExplorer) Load(path string, change *Change, cached []byte) {
	if path == fe.filePath && fe.lines != nil {
		// Cheap navigation case: same file, unchanged since we last
		// rendered it - just move the cursor.
		if fe.contentFresh(path, cached) {
			fe.jumpTo(change)
			return
		}
		// The file changed under us (external write); fall through and
		// reload so the pane never shows stale content.
	}

	fe.filePath = path
	fe.lines = nil
	fe.lineStates = nil
	fe.styledLines = nil
	fe.display = nil
	fe.lineStart = nil
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
	fe.data = data
	if fi, err := os.Stat(path); err == nil {
		fe.modNanos = fi.ModTime().UnixNano()
	} else {
		fe.modNanos = time.Now().UnixNano()
	}

	if len(data) == 0 {
		fe.lines = []string{""}
		fe.lineStates = []diffState{stateNormal}
		fe.rewrap()
		return
	}

	fe.lines = strings.Split(string(data), "\n")
	if len(fe.lines) > 0 && fe.lines[len(fe.lines)-1] == "" {
		fe.lines = fe.lines[:len(fe.lines)-1]
	}
	if len(fe.lines) == 0 {
		fe.lines = []string{""}
	}

	fe.highlight(data)
	fe.computeLineStates()
	fe.rewrap()
	fe.jumpTo(change)
}

// contentFresh reports whether the file hasn't changed since we last
// rendered it: watcher-provided bytes are authoritative when available,
// otherwise the on-disk mod time decides.
func (fe *FileExplorer) contentFresh(path string, cached []byte) bool {
	if cached != nil {
		return bytes.Equal(cached, fe.data)
	}
	if fe.modNanos == 0 {
		return true
	}
	fi, err := os.Stat(path)
	if err != nil {
		return true // file vanished; keep the old view rather than blanking
	}
	return fi.ModTime().UnixNano() == fe.modNanos
}

// rewrap wraps the source lines into display rows at the current text
// width (fe.width-4), the same word-wrap the message area uses: long lines
// flow onto extra rows instead of being cut off, and every row is padded to
// exactly textW cells so the hand-drawn frame stays intact.
func (fe *FileExplorer) rewrap() {
	w := textW(fe.width)
	fe.textW = w
	fe.display = nil
	fe.lineStart = make([]int, len(fe.lines))
	for i, raw := range fe.lines {
		fe.lineStart[i] = len(fe.display)
		rendered, st := fe.renderLine(i, raw)
		for _, seg := range strings.Split(lipgloss.Wrap(rendered, w, ""), "\n") {
			fe.display = append(fe.display, st.Width(w).Render(seg))
		}
	}
	fe.totalLines = len(fe.display)
	if fe.viewStart >= fe.totalLines {
		fe.viewStart = 0
	}
}

// ensureDisplay rewraps the file when the pane width changed since display
// was last built (or when there is no display yet).
func (fe *FileExplorer) ensureDisplay() {
	if fe.lines == nil {
		return
	}
	if w := textW(fe.width); fe.textW != w || fe.display == nil {
		fe.rewrap()
	}
}

// renderLine returns the styled rendering of source line i together with
// the style used to pad it: marker lines keep their muted italic look,
// old/new halves their background, everything else the plain (possibly
// chroma-highlighted) text.
func (fe *FileExplorer) renderLine(i int, raw string) (string, lipgloss.Style) {
	switch {
	case isMarkerLine(raw):
		switch {
		case strings.HasPrefix(raw, "<<<<<<<"):
			return raw, markerOldStyle
		case strings.HasPrefix(raw, ">>>>>>>"):
			return raw, markerNewStyle
		default:
			return raw, markerStyle
		}
	case fe.lineStates[i] == stateOld:
		return raw, oldBgStyle
	case fe.lineStates[i] == stateNew:
		return raw, newBgStyle
	default:
		if fe.styledLines != nil && i < len(fe.styledLines) && fe.styledLines[i] != "" {
			return fe.styledLines[i], normalStyle
		}
		return raw, normalStyle
	}
}

// highlight tokenizes data with chroma and stores the per-line ANSI rendering
// for the "normal" (unchanged) lines. Marker lines and old/new halves keep
// their plain background treatment so the diff stays readable.
func (fe *FileExplorer) highlight(data []byte) {
	fe.styledLines = nil
	if len(data) > maxHighlightBytes || len(fe.lines) == 0 {
		return
	}

	lexer := chroma.Coalesce(lexers.Match(fe.filePath))
	if lexer == nil {
		return
	}
	it, err := lexer.Tokenise(nil, string(data))
	if err != nil {
		return
	}

	var b strings.Builder
	if ferr := formatters.TTY16m.Format(&b, syntaxChromaStyle, it); ferr != nil {
		return
	}

	rendered := strings.Split(b.String(), "\n")
	fe.styledLines = make([]string, len(fe.lines))
	copy(fe.styledLines, rendered)
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
	if lineNum >= len(fe.lines) {
		lineNum = len(fe.lines) - 1
	}
	// Jump to the display row the change starts on (its source line may be
	// wrapped across several display rows).
	start := 0
	if fe.lineStart != nil && lineNum < len(fe.lineStart) {
		start = fe.lineStart[lineNum]
	}
	fe.jumpLine = start
	fe.viewStart = start - fe.height/3
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
	fe.ensureDisplay()

	if len(fe.display) == 0 {
		return lipgloss.Place(
			width, height, lipgloss.Center, lipgloss.Center,
			emptyStyle.Render("no file selected"),
		)
	}

	// A too-tiny pane gets a bare first line rather than a mangled frame.
	if width < 4 || height < 4 {
		return fe.display[min(fe.viewStart, len(fe.display)-1)]
	}

	// Fixed rows: top border, padding, footer, bottom border = 4.
	bodyRows := height - 4
	written := 0
	var b strings.Builder
	b.WriteString(fe.topLine(fe.width))
	for i := fe.viewStart; i < len(fe.display) && written < bodyRows; i++ {
		b.WriteString("\n ")
		// display rows are already wrapped, styled and padded to textW,
		// so " " + row is exactly as wide as the frame.
		b.WriteString(fe.display[i])
		written++
	}
	for ; written < bodyRows; written++ {
		b.WriteString("\n")
		b.WriteString(strings.Repeat(" ", fe.width))
	}

	// Padding between the content and the bottom border.
	b.WriteString("\n")
	b.WriteString(strings.Repeat(" ", fe.width))

	b.WriteString("\n")
	b.WriteString(fe.footerLine(fe.width))

	b.WriteString("\n")
	b.WriteString(infoLineStyle.Render(strings.Repeat("─", max(0, fe.width))))

	return b.String()
}

// textW is the number of cells a content row occupies: the pane width minus
// the single-cell leading indent. Display rows are wrapped and padded to
// exactly this width, so " " + row lands flush against the frame.
func textW(width int) int {
	return max(1, width-1)
}

// topLine draws the file's name strip: a straight ─ line with the
// (root-relative, width-limited) file name inlaid, no corner edges, spanning
// exactly cells wide in the info color.
func (fe *FileExplorer) topLine(cells int) string {
	name := fe.displayName()
	if name == "" {
		name = "file"
	}
	maxName := cells - 6
	if maxName < 1 {
		maxName = 1
	}
	limit := maxPathChars(fe.screenWidth)
	if limit > 0 && limit < maxName {
		maxName = limit
	}
	if maxName > pathNameCap {
		maxName = pathNameCap
	}
	if lipgloss.Width(name) > maxName {
		name = truncateCells(name, maxName)
	}

	prefix := "─ " + name + " "
	fill := max(0, cells-lipgloss.Width(prefix))
	return infoLineStyle.Render(prefix + strings.Repeat("─", fill))
}

// displayName returns the file path shown in the header: relative to the
// project root when possible, otherwise the path as given.
func (fe *FileExplorer) displayName() string {
	return relativePath(fe.root, fe.filePath)
}

// footerLine returns the scroll percentage right-aligned on its own row.
// With the side borders gone it's just "spaces + pct" (the └┘ border is
// drawn separately below it); if the pane is too narrow to fit the
// percentage it's dropped rather than letting the row overflow.
func (fe *FileExplorer) footerLine(cells int) string {
	pct := fmt.Sprintf("%.0f%%", fe.scrollPercent())
	if lipgloss.Width(pct) > cells {
		pct = ""
	}
	return strings.Repeat(" ", max(0, cells-lipgloss.Width(pct))) + pct
}

// scrollPercent returns how far through the file the current view window
// sits, as a percent, clamped to 0..100 (scrolling past the bottom must
// never report more than 100%).
func (fe *FileExplorer) scrollPercent() float64 {
	if fe.totalLines <= 0 {
		return 0
	}
	if fe.height >= fe.totalLines {
		return 100
	}
	pct := float64(fe.viewStart) / float64(fe.totalLines-fe.height) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}

// Invalidate forces the next Load to re-read the file even when the path
// hasn't changed (e.g. right after an accept/reject rewrote it) and to
// rewrap at the current width.
func (fe *FileExplorer) Invalidate() {
	fe.lines = nil
	fe.lineStates = nil
	fe.styledLines = nil
	fe.display = nil
	fe.lineStart = nil
	fe.viewStart = 0
	fe.jumpLine = -1
	fe.totalLines = 0
}

func (fe *FileExplorer) Clear() {
	fe.Invalidate()
	fe.filePath = ""
}
