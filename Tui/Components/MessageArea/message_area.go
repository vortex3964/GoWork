package messagearea

import (
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

type message struct {
	content string
	isUser  bool
}

func NewMessage(content string, isUser bool) message {
	return message{content: content, isUser: isUser}
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
