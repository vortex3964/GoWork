package messagearea

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

const padSide = 3
const botPad = 1

// msgGap is the number of blank lines left between two consecutive
// messages.
const msgGap = 1

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
	// rendered caches the styled (but NOT width-wrapped) block for each
	// message, aligned by index with messages. The viewport does the
	// actual wrapping itself at render time, so a block only needs to be
	// built once, ever resizing doesn't touch it.
	rendered []string
	size     int
}

// New creates an empty message area.
func New() Model {
	vp := viewport.New()
	vp.Style = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Border).
		PaddingLeft(2).
		PaddingRight(2)

	// Let the viewport wrap its own content. It re-wraps to whatever
	// width it currently has, including after a resize, so we don't
	// need to hand-roll that ourselves.
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

	m.rendered = append(m.rendered, m.renderBlock(msg))
	if m.vp.Width() > 0 {
		// Only push to the viewport once it actually has dimensions
		// SetContent before any SetWidth/SetHeight call used to be a
		// sharp edge in older viewport betas. Cheap to guard against,
		// so no reason not to.
		m.pushContent()
	}
	m.vp.GotoBottom()
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

	firstSize := m.vp.Width() == 0
	m.vp.SetWidth(inner)
	m.vp.SetHeight(h)

	if firstSize {
		m.pushContent()
	}

}

// renderBlock styles a single message a small colored header on its own
// line, then the body. It does NOT wrap the text; the viewport handles
// that. That makes this a one-time cost per message rather than something
// that has to be redone on every resize.
func (m *Model) renderBlock(msg message) string {
	label := "● Ai"
	labelColor := aiLabelColor
	bodyColor := aiBodyColor
	if msg.isUser {
		label = "● User"
		labelColor = userLabelColor
		bodyColor = userBodyColor
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(labelColor).Render(label)
	body := lipgloss.NewStyle().Foreground(bodyColor).Render(msg.content)

	return header + "\n" + body
}

// pushContent joins the cached per-message blocks and hands the result to
// the viewport. SetContent always wants the whole string, but since every
// block was already styled once when its message was appended, this is
// just a cheap string join, not a re-render.
func (m *Model) pushContent() {
	if len(m.rendered) == 0 {
		m.vp.SetContent("")
		return
	}
	m.vp.SetContent(strings.Join(m.rendered, strings.Repeat("\n", msgGap+1)))
}
