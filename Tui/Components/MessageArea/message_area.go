package messagearea

import (
	"encoding/json"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	glamour "charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

const padSide = 3
const botPad = 1

// msgGap is the number of blank lines left between two consecutive
// messages.
const msgGap = 1
const fallbackWidth = 80

var (
	aiLabelColor   = lipgloss.Color("111")
	userLabelColor = lipgloss.Color("212")
	aiBodyColor    = lipgloss.Color("245")
	userBodyColor  = lipgloss.Color("252")
)

// blockSeparator is the blank-line run between rendered message blocks in
// the joined viewport content.
var blockSeparator = strings.Repeat("\n", msgGap+1)

// liveHeaderStyle and liveBodyStyle are the fixed parts of the plain-text
// block shown while an assistant answer is still streaming. Reusing them
// keeps a delta from rebuilding the same styles from scratch.
var (
	liveHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(aiLabelColor)
	liveBodyStyle   = lipgloss.NewStyle().Foreground(aiBodyColor)
)

type ToolStatus int

const (
	ToolRunning ToolStatus = iota
	ToolDone
	ToolError
)

// maxToolResultLines caps how many lines of a tool's output we show inline
const maxToolResultLines = 12

// maxToolArgsLen caps the one-line argument summary shown next to the tool
// name so a giant JSON blob can't push the header off the screen.
const maxToolArgsLen = 80

type message struct {
	content string
	isUser  bool

	// tool-call message fields (isTool == true)
	isTool   bool
	toolName string
	toolArgs string
	status   ToolStatus
	result   string
}

func NewMessage(content string, isUser bool) message {
	return message{content: content, isUser: isUser}
}

func NewToolMessage(name, args string) message {
	return message{isTool: true, toolName: name, toolArgs: args, status: ToolRunning}
}

// SummarizeToolInput renders a compact one-line label for a tool call's
// raw JSON input, preferring path/url-like values over the raw blob
func SummarizeToolInput(input json.RawMessage) string {
	if len(input) == 0 || string(input) == "{}" {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}

	prefer := []string{"file_path", "path", "url", "src", "dst", "srcpath", "dstpath", "command"}
	for _, key := range prefer {
		if raw, ok := m[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var s string
		if json.Unmarshal(m[k], &s) == nil && strings.TrimSpace(s) != "" {
			return k + "=" + s
		}
	}
	return ""
}

type Model struct {
	vp       viewport.Model
	messages []message
	rendered []string
	size     int

	// content caches the joined rendered blocks handed to the viewport.
	// Appending and streaming extend it in place, so pushContent never
	// has to rejoin the whole history on every streamed delta.
	content string

	// liveIdx is the index of the assistant message currently being
	// streamed, or -1 when nothing is streaming. livePrefix caches the
	// joined blocks before it, so a delta only has to swap the tail.
	liveIdx    int
	livePrefix string

	// renderCount counts markdown (glamour) renders. Tests use it to make
	// sure streaming doesn't re-render the growing answer on every delta.
	renderCount int

	// Copy selection state: selAnchor and selFocus are inclusive message
	// indexes. selAnchor < 0 means nothing is selected. copyMode is set
	// while the message area owns the keyboard for range selection.
	selAnchor int
	selFocus  int
	copyMode  bool

	// mdRenderer is the glamour renderer used for AI messages, cached
	// and only rebuilt when the width it was built for (mdWidth) no
	// longer matches what we need, since constructing one isn't free.
	mdRenderer *glamour.TermRenderer
	mdWidth    int
}

// New creates an empty message area.
func New() Model {
	vp := viewport.New()
	vp.Style = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Border).
		PaddingLeft(2).
		PaddingRight(2)

	vp.SoftWrap = true

	return Model{
		vp:        vp,
		messages:  []message{},
		liveIdx:   -1,
		selAnchor: -1,
	}
}

func (m *Model) AppendMessage(content string, isUser bool) {
	msg := NewMessage(content, isUser)
	m.append(msg)
}

// AppendTool adds a tool-call message in its running state and returns its
// index, which the caller uses with UpdateToolMessage once the tool yields a
// result.
func (m *Model) AppendTool(name, args string) int {
	m.append(NewToolMessage(name, args))
	return len(m.messages) - 1
}

// UpdateToolMessage flips a previously-appended tool message to done/error and
// attaches its result. No-op for index out of range or a non-tool message.
func (m *Model) UpdateToolMessage(index int, status ToolStatus, result string) {
	if index < 0 || index >= len(m.messages) || !m.messages[index].isTool {
		return
	}
	m.messages[index].status = status
	m.messages[index].result = result
	m.rendered[index] = m.renderBlock(m.messages[index], m.contentWidth())
	m.rebuildContent()
	m.pushContent()
	m.vp.GotoBottom()
}

// LiveAssistantStart appends a fresh, empty assistant message and returns its
// index. The caller owns it and should feed streamed tokens to it with
// LiveAssistantUpdate, then finalize it with LiveAssistantFinish when the
// stream ends.
func (m *Model) LiveAssistantStart() int {
	m.liveIdx = len(m.messages)
	m.livePrefix = m.content
	m.appendRendered(NewMessage("", false), m.renderLiveBlock("", m.contentWidth()))
	return m.liveIdx
}

// LiveAssistantUpdate replaces the content of the assistant message at index
// (created by LiveAssistantStart) with content. While the answer streams it
// is kept as a plain-text block: running glamour over the whole growing
// answer on every delta is O(n²) over the stream. Only the tail block is
// regenerated and swapped into the cached content, so the rest of the
// history is never re-joined. No-op for a tool or user message, an
// out-of-range index, or a message that isn't the live one.
func (m *Model) LiveAssistantUpdate(index int, content string) {
	if index < 0 || index >= len(m.messages) || index != m.liveIdx {
		return
	}
	msg := m.messages[index]
	if msg.isUser || msg.isTool {
		return
	}
	msg.content = content
	m.messages[index] = msg

	block := m.renderLiveBlock(content, m.contentWidth())
	m.rendered[index] = block
	if m.livePrefix == "" {
		m.content = block
	} else {
		m.content = m.livePrefix + blockSeparator + block
	}
	m.pushContent()
	m.vp.GotoBottom()
}

// LiveAssistantFinish renders the final content of the streamed message once,
// now that the full answer is known, and clears the live state. No-op unless
// index still points at the message started by LiveAssistantStart.
func (m *Model) LiveAssistantFinish(index int, content string) {
	if index < 0 || index >= len(m.messages) || index != m.liveIdx {
		return
	}
	msg := m.messages[index]
	if msg.isUser || msg.isTool {
		return
	}
	msg.content = content
	m.messages[index] = msg
	m.liveIdx = -1
	m.livePrefix = ""
	m.rendered[index] = m.renderBlock(msg, m.contentWidth())
	m.rebuildContent()
	m.pushContent()
	m.vp.GotoBottom()
}

func (m *Model) append(msg message) {
	m.appendRendered(msg, m.renderBlock(msg, m.contentWidth()))
}

// appendRendered adds a message and its already-rendered block, extending
// the cached joined content instead of re-joining the whole history.
func (m *Model) appendRendered(msg message, block string) {
	m.messages = append(m.messages, msg)
	m.size += 1

	m.rendered = append(m.rendered, block)
	if len(m.rendered) == 1 {
		m.content = block
	} else {
		m.content += blockSeparator + block
	}
	if m.vp.Width() > 0 {
		// Only push to the viewport once it actually has dimensions
		// SetContent before any SetWidth/SetHeight call used to be a
		// sharp edge in older viewport betas. Cheap to guard against,
		// so no reason not to.
		m.pushContent()
	}
	m.vp.GotoBottom()
}

func (m Model) Size() int {
	return m.size
}

func (m *Model) ScrollUp() {
	m.vp.ScrollUp(m.vp.MouseWheelDelta)
}

func (m *Model) ScrollDown() {
	m.vp.ScrollDown(m.vp.MouseWheelDelta)
}

// Update forwards messages to the underlying viewport
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// View renders the message area, including its border and outer margin.
func (m Model) View() string {
	view := lipgloss.NewStyle().Margin(0, padSide).Render(m.vp.View())
	return m.overlaySelection(view)
}

func (m *Model) SetSize(outerWidth int, outerHeight int) {
	inner := outerWidth - padSide*2
	if inner < 1 {
		inner = 1
	}

	//always leave space from top and bottom
	h := outerHeight - botPad*2
	if h < 0 {
		h = 0
	}

	prevWidth := m.vp.Width()
	m.vp.SetWidth(inner)
	m.vp.SetHeight(h)

	if prevWidth == 0 || inner != prevWidth {
		m.rebuildRendered()
		m.pushContent()
	}
}

// contentWidth returns the width blocks should be rendered/wrapped at
// right now. vp.Width() is the viewport's OUTER width internally the
// viewport subtracts its own Style's border+padding (GetHorizontalFrameSize)
// to get the usable content width, and clips (not wraps) anything wider
// than that. So we have to do the same subtraction here before handing
// this width to glamour/lipgloss, or wrapped text overflows the frame and
// gets silently truncated instead of wrapping onto the next line.
func (m *Model) contentWidth() int {
	w := m.vp.Width()
	if w <= 0 {
		return fallbackWidth
	}
	w -= m.vp.Style.GetHorizontalFrameSize()
	if w < 1 {
		w = 1
	}
	return w
}

// rebuildRendered re-renders every cached block at the current content
// width. Only needed when that width changes - a block rendered at its
// original width doesn't otherwise go stale. The live block stays plain
// text so a resize mid-stream doesn't trigger a glamour render either.
func (m *Model) rebuildRendered() {
	width := m.contentWidth()
	rendered := make([]string, len(m.messages))
	for i, msg := range m.messages {
		if i == m.liveIdx {
			rendered[i] = m.renderLiveBlock(msg.content, width)
			continue
		}
		rendered[i] = m.renderBlock(msg, width)
	}
	m.rendered = rendered
	m.rebuildContent()
}

// renderLiveBlock renders the streaming assistant message as plain styled
// text. The final answer is markdown-rendered once by LiveAssistantFinish.
func (m *Model) renderLiveBlock(content string, width int) string {
	header := liveHeaderStyle.Render("● Ai")
	body := liveBodyStyle.Width(width).Render(content)
	return header + "\n" + body
}

// markdownRenderer returns a glamour renderer sized to width, rebuilding
// it only when the width actually changed since last time. Constructing
// one loads/parses a style, so it's not something to do per-message.
func (m *Model) markdownRenderer(width int) *glamour.TermRenderer {
	if width < 1 {
		width = 1
	}
	if m.mdRenderer == nil || m.mdWidth != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithStyles(markdownStyle()),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			// Keep whatever renderer we already had (possibly nil)
			// rather than losing it - renderBlock falls back to plain
			// styled text if this ends up nil.
			return m.mdRenderer
		}
		m.mdRenderer = r
		m.mdWidth = width
	}
	return m.mdRenderer
}

func (m *Model) renderBlock(msg message, width int) string {
	if msg.isTool {
		icon := "●"
		labelColor := style.Info
		bodyColor := style.Muted
		switch msg.status {
		case ToolDone:
			icon = "✓"
			labelColor = style.Success
			bodyColor = style.Text
		case ToolError:
			icon = "×"
			labelColor = style.Danger
			bodyColor = style.Danger
		}

		header := lipgloss.NewStyle().Bold(true).Foreground(labelColor).Render(icon + " " + msg.toolName)
		if args := msg.toolArgs; args != "" {
			if len(args) > maxToolArgsLen {
				args = args[:maxToolArgsLen] + "…"
			}
			header += " " + lipgloss.NewStyle().Foreground(style.Muted).Render(args)
		}

		var body string
		switch msg.status {
		case ToolRunning:
			body = lipgloss.NewStyle().Italic(true).Foreground(style.Muted).Render("running…")
		default:
			if msg.result != "" {
				body = lipgloss.NewStyle().Foreground(bodyColor).Width(width).Render(truncateToolResult(msg.result))
			}
		}
		return header + "\n" + body
	}

	label := "● Ai"
	labelColor := aiLabelColor
	bodyColor := aiBodyColor
	if msg.isUser {
		label = "● User"
		labelColor = userLabelColor
		bodyColor = userBodyColor
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(labelColor).Render(label)

	// Default to plain styled text this is what user messages use,
	// and also what AI messages fall back to if markdown rendering
	// isn't available or fails on this particular message. Width() here
	// word-wraps to the block's target width so these lines wrap at the
	// same boundary as glamour's AI-message output, rather than relying
	// on the viewport's cut-based SoftWrap (which doesn't respect word
	// boundaries) to do it for us.
	body := lipgloss.NewStyle().Foreground(bodyColor).Width(width).Render(msg.content)
	if !msg.isUser {
		if r := m.markdownRenderer(width); r != nil {
			m.renderCount++
			if out, err := r.Render(msg.content); err == nil {
				// glamour pads block elements with leading/trailing
				// blank lines; trim those so message spacing stays
				// governed by msgGap instead of stacking with it.
				body = strings.Trim(out, "\n")
			}
		}
	}

	return header + "\n" + body
}

// truncateToolResult caps the tool output shown inline so one huge result
// (e.g. a full file read) can't fill the whole message area.
func truncateToolResult(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxToolResultLines {
		return strings.Join(lines[:maxToolResultLines], "\n") + "\n…"
	}
	return s
}

// rebuildContent recomputes the cached joined content from the rendered
// blocks and refreshes the prefix cached for the live block. Only layout or
// history changes call this; streamed deltas patch the tail instead.
func (m *Model) rebuildContent() {
	m.content = strings.Join(m.rendered, blockSeparator)
	if m.liveIdx >= 0 && m.liveIdx <= len(m.rendered) {
		m.livePrefix = strings.Join(m.rendered[:m.liveIdx], blockSeparator)
	}
}

// pushContent hands the cached joined content to the viewport. SetContent
// always wants the whole string, but every block was already styled once
// when its message was appended/updated, and the join itself is cached, so
// this doesn't re-render or re-join anything.
func (m *Model) pushContent() {
	m.vp.SetContent(m.content)
}

func (m *Model) LastUserMessage(n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := m.size - 1; i >= 0; i-- {
		if m.messages[i].isUser {
			count++
			if count == n {
				return m.messages[i].content
			}
		}
	}
	return ""
}
