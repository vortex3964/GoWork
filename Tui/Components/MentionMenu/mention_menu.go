// Package mentionmenu renders the "@" autocomplete window shown above the
// prompt bar. It only owns filtering, selection and rendering; the sources
// (skills, MCP servers, language servers and project paths) are enumerated by
// the app shell and handed in as a flat item list.
package mentionmenu

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"GoWork/Tui/Style"
)

// MaxRows caps how many matches the menu shows at once. Longer result sets
// scroll around the selection instead of pushing the prompt off screen.
const MaxRows = 14

// maxProjectEntries bounds one project scan so a huge repository cannot stall
// the update loop just because the user typed "@".
const maxProjectEntries = 4000

// Kind is the source category of one mention.
type Kind string

const (
	KindSkill  Kind = "skill"
	KindMCP    Kind = "mcp"
	KindLSP    Kind = "lsp"
	KindFolder Kind = "folder"
	KindFile   Kind = "file"
)

// Item is one mention candidate.
type Item struct {
	// Kind is the source category shown in the left column.
	Kind Kind
	// Name is the short display name (skill/server name, or a path's base).
	Name string
	// Description is the short, source-specific detail: a skill description,
	// an MCP transport/status, LSP language IDs, or a root-relative path.
	Description string
	// Insert overrides the text completed into the prompt. When empty the
	// mention form is derived from Kind and Name.
	Insert string
}

// Mention returns the canonical text for this item, including the leading
// "@": @skill, @mcp:server, @lsp:name or @path/to/file.
func (i Item) Mention() string {
	if i.Insert != "" {
		return i.Insert
	}
	switch i.Kind {
	case KindMCP:
		return "@mcp:" + i.Name
	case KindLSP:
		return "@lsp:" + i.Name
	default:
		return "@" + i.Name
	}
}

// searchText is the haystack used by fuzzy matching. It deliberately includes
// the kind and the completed mention form so queries such as "mcp:" or
// "folder" narrow to one source as well as matching names and descriptions.
func (i Item) searchText() string {
	return string(i.Kind) + " " + i.Name + " " + i.Description + " " + i.Mention()
}

// Model is the autocomplete state.
type Model struct {
	items   []Item
	matches []Item
	cursor  int
	visible bool
	active  bool // the cursor is inside an "@" token

	query string
	// key is the prompt text up to the cursor for the active token. It is
	// what Close remembers so a dismissed menu stays closed until the text
	// changes.
	key       string
	lastKey   string
	dismissed string

	// tokenStart/tokenEnd are rune offsets around the active "@" token; the
	// completion replaces [tokenStart, tokenEnd).
	tokenStart int
	tokenEnd   int

	width int
}

var (
	kindStyle     = lipgloss.NewStyle().Foreground(style.Muted)
	nameStyle     = lipgloss.NewStyle().Foreground(style.Info).Bold(true)
	descStyle     = lipgloss.NewStyle().Foreground(style.Muted)
	selectedStyle = lipgloss.NewStyle().Background(style.Highlight).Bold(true)
)

// New returns an empty menu. Items can be supplied up front or via SetItems.
func New(items ...Item) Model {
	m := Model{width: 80, tokenStart: -1, tokenEnd: -1}
	m.SetItems(items)
	return m
}

// SetItems replaces the source snapshot and refilters the current query when a
// token is active.
func (m *Model) SetItems(items []Item) {
	m.items = append([]Item(nil), items...)
	if m.active {
		m.refilter()
	}
}

// SetWidth sets the rendered width (in columns) of the menu rows.
func (m *Model) SetWidth(width int) {
	if width > 0 {
		m.width = width
	}
}

// Sync updates the menu from the prompt text and cursor position (both in
// runes). The menu is active only while the cursor sits inside an "@" token:
// an "@" at the start of the prompt or right after whitespace, with no
// whitespace between it and the cursor.
func (m *Model) Sync(input string, cursor int) {
	runes := []rune(input)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	start, ok := mentionTokenStart(runes, cursor)
	m.tokenStart = start
	m.tokenEnd = cursor
	m.active = ok
	if !ok {
		m.visible = false
		m.matches = nil
		m.query = ""
		m.key = ""
		return
	}

	m.key = string(runes[:cursor])
	if m.dismissed != "" && m.key != m.dismissed {
		m.dismissed = ""
	}
	if m.dismissed == m.key {
		m.visible = false
		return
	}
	m.query = string(runes[start+1 : cursor])
	if m.key != m.lastKey {
		m.cursor = 0
		m.lastKey = m.key
	}
	m.refilter()
}

// mentionTokenStart scans backwards from cursor for the "@" that starts the
// token being edited. It returns -1 when there is no token.
func mentionTokenStart(runes []rune, cursor int) (int, bool) {
	for i := cursor - 1; i >= 0; i-- {
		switch {
		case runes[i] == '@':
			if i == 0 || isSpace(runes[i-1]) {
				return i, true
			}
			return -1, false
		case isSpace(runes[i]):
			return -1, false
		}
	}
	return -1, false
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func (m *Model) refilter() {
	if !m.active {
		m.matches = nil
		m.visible = false
		return
	}
	if m.query == "" {
		m.matches = append([]Item(nil), m.items...)
	} else {
		haystacks := make([]string, len(m.items))
		for i, item := range m.items {
			haystacks[i] = item.searchText()
		}
		found := fuzzy.FindNoSort(m.query, haystacks)
		// FindNoSort leaves ties in source order; sorting stably by score keeps
		// the ranking deterministic for equal scores.
		sort.SliceStable(found, func(i, j int) bool { return found[i].Score > found[j].Score })
		m.matches = m.matches[:0]
		for _, match := range found {
			m.matches = append(m.matches, m.items[match.Index])
		}
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.matches) {
		m.cursor = len(m.matches) - 1
	}
	m.visible = len(m.matches) > 0 && m.dismissed != m.key
}

// Visible reports whether the menu should be drawn.
func (m Model) Visible() bool { return m.active && m.visible && len(m.matches) > 0 }

// Query is the current filter text after the "@".
func (m Model) Query() string { return m.query }

// Matches returns the items matching the current query.
func (m Model) Matches() []Item {
	return append([]Item(nil), m.matches...)
}

// Selected returns the highlighted item.
func (m Model) Selected() (Item, bool) {
	if !m.Visible() || m.cursor < 0 || m.cursor >= len(m.matches) {
		return Item{}, false
	}
	return m.matches[m.cursor], true
}

// Selection returns the highlighted item and the rune offsets the completion
// must replace.
func (m Model) Selection() (Item, int, int, bool) {
	item, ok := m.Selected()
	if !ok || m.tokenStart < 0 || m.tokenEnd < m.tokenStart {
		return Item{}, 0, 0, false
	}
	return item, m.tokenStart, m.tokenEnd, true
}

// Move shifts the selection by delta, wrapping at both ends.
func (m *Model) Move(delta int) {
	n := len(m.matches)
	if !m.Visible() || n == 0 {
		return
	}
	m.cursor = (m.cursor + delta) % n
	if m.cursor < 0 {
		m.cursor += n
	}
}

// Close hides the menu for the current prompt text until the text changes.
func (m *Model) Close() {
	m.visible = false
	if m.active {
		m.dismissed = m.key
	}
}

// Height is the number of rows View occupies.
func (m Model) Height() int {
	if !m.Visible() {
		return 0
	}
	if len(m.matches) > MaxRows {
		return MaxRows
	}
	return len(m.matches)
}

// View renders the menu rows, exactly Height lines.
func (m Model) View() string {
	if !m.Visible() {
		return ""
	}
	n := len(m.matches)
	start := 0
	if n > MaxRows {
		start = m.cursor - MaxRows/2
		if start < 0 {
			start = 0
		}
		if start+MaxRows > n {
			start = n - MaxRows
		}
		n = MaxRows
	}

	lines := make([]string, 0, n)
	for i := start; i < start+n; i++ {
		item := m.matches[i]
		kind := fmt.Sprintf("%-6s", string(item.Kind))
		avail := m.width - lipgloss.Width(kind) - 2
		if avail < 8 {
			avail = 8
		}
		nameWidth := avail / 3
		if nameWidth < 8 {
			nameWidth = 8
		}
		if nameWidth > 28 {
			nameWidth = 28
		}
		name := truncate(item.Name, nameWidth)
		descWidth := avail - lipgloss.Width(name) - 2
		if descWidth < 0 {
			descWidth = 0
		}
		desc := truncate(item.Description, descWidth)
		row := kind + "  " + name + "  " + desc
		if i == m.cursor {
			row = truncate(row, m.width)
			lines = append(lines, selectedStyle.Width(m.width).Foreground(style.Primary).Render(row))
			continue
		}
		lines = append(lines, kindStyle.Render(kind)+"  "+nameStyle.Render(name)+"  "+descStyle.Render(desc))
	}
	return strings.Join(lines, "\n")
}

// ProjectItems enumerates the folders and files under root, root-relative and
// slash-separated. The scan is read-only and bounded; VCS metadata is skipped
// so the list stays useful. Unreadable entries are ignored rather than
// aborting the whole scan.
func ProjectItems(root string) []Item {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	var items []Item
	_ = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == abs {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case ".git", ".hg", ".svn":
				return fs.SkipDir
			}
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			items = append(items, Item{
				Kind:        KindFolder,
				Name:        name,
				Description: rel + "/",
				Insert:      "@" + rel,
			})
		} else {
			items = append(items, Item{
				Kind:        KindFile,
				Name:        name,
				Description: rel,
				Insert:      "@" + rel,
			})
		}
		if len(items) >= maxProjectEntries {
			return fs.SkipAll
		}
		return nil
	})
	return items
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
