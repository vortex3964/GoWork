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

***REMOVED***
***REMOVED***

type ToolStatus int

const (
	ToolRunning ToolStatus = iota
	ToolDone
	ToolError
)

// maxToolResultLines caps how many lines of a tool's output we show inline
// (crush uses a similar height cap; the full result still goes back to the
// model in context).
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
// raw JSON input, preferring path/url-like values over the raw blob - the
// "main param" of crush's `● bash git status` style header.
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
		vp:       vp,
		messages: []message{},
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
	m.pushContent()
	m.vp.GotoBottom()
}

// LiveAssistantStart appends a fresh, empty assistant message and returns its
// index. The caller owns it and should feed streamed tokens to it with
// LiveAssistantUpdate, then leave it as the final message when the stream ends.
func (m *Model) LiveAssistantStart() int {
	m.append(NewMessage("", false))
	return len(m.messages) - 1
}

// LiveAssistantUpdate replaces the content of the assistant message at index
// (created by LiveAssistantStart) with content. It re-renders just that block,
// so streaming token-by-token stays cheap even with many messages on screen.
// No-op for a tool or user message, or an out-of-range index.
func (m *Model) LiveAssistantUpdate(index int, content string) {
	if index < 0 || index >= len(m.messages) {
		return
	}
	msg := m.messages[index]
	if msg.isUser || msg.isTool {
		return
	}
	msg.content = content
	m.messages[index] = msg
	m.rendered[index] = m.renderBlock(msg, m.contentWidth())
	m.pushContent()
	m.vp.GotoBottom()
}

func (m *Model) append(msg message) {
	m.messages = append(m.messages, msg)
	m.size += 1

	m.rendered = append(m.rendered, m.renderBlock(msg, m.contentWidth()))
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
	return lipgloss.NewStyle().Margin(0, padSide).Render(m.vp.View())
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
// original width doesn't otherwise go stale.
func (m *Model) rebuildRendered() {
	width := m.contentWidth()
	rendered := make([]string, len(m.messages))
	for i, msg := range m.messages {
		rendered[i] = m.renderBlock(msg, width)
	}
	m.rendered = rendered
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
	***REMOVED***
	***REMOVED***
	***REMOVED***
	
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

// pushContent joins the cached per-message blocks and hands the result to
// the viewport. SetContent always wants the whole string, but since every
// block was already styled once when its message was appended (or
// rebuilt), this is just a cheap string join, not a re-render.
func (m *Model) pushContent() {
	if len(m.rendered) == 0 {
		m.vp.SetContent("")
		return
	}
	m.vp.SetContent(strings.Join(m.rendered, strings.Repeat("\n", msgGap+1)))
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
