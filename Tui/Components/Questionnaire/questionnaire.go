// Questionnaire holds the data struct and the tui model for the single
// questionaire that asks the user questions instead of the prompt bar
package questionnaire

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// Question is one question with up to 3 selectable answers
type Question struct {
	Question string
	Answers  []string
}

// questionaire is the single active questionnaire shared by the tool and the tui
var questionaire []Question

// Active returns the pending questionnaire or nil when none is active
func Active() []Question { return questionaire }

// SetActive stores the questionnaire the ai asked for
func SetActive(qs []Question) { questionaire = qs }

// Clear drops the active questionnaire
func Clear() { questionaire = nil }

// Height is how many rows the questionnaire occupies in place of the prompt bar
const Height = 6

// maxAnswers is the number of option rows shown plus the always-on custom row
const maxAnswers = 3

// marginSide matches the prompt bar's side margin so the swap is seamless
const marginSide = 3

// DoneMsg carries the user's answers back to the main update loop
type DoneMsg struct {
	Answers   []string
	Cancelled bool
}

// Model is the tea model that renders and drives the active questionnaire
type Model struct {
	width     int
	questions []Question
	answers   []string
	index     int
	choice    int
	custom    textinput.Model
	customOn  bool
}

// New wraps a questionnaire in a ready to use model
func New(qs []Question) Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "type your own answer"
	ti.CharLimit = 200
	return Model{questions: qs, answers: make([]string, len(qs)), custom: ti}
}

// SetWidth reflows the options and inline input to the window width
func (m *Model) SetWidth(w int) {
	m.width = w
	inner := w - marginSide*2
	if inner < 1 {
		inner = 1
	}
	m.custom.SetWidth(inner - 2)
}

// Questions returns the questions backing this model
func (m Model) Questions() []Question { return m.questions }

func (m Model) last() bool { return m.index == len(m.questions)-1 }

func (m *Model) openCustom() {
	m.customOn = true
	m.custom.SetValue("")
	m.custom.Focus()
}

func (m Model) submit() tea.Cmd {
	return func() tea.Msg {
		return DoneMsg{Answers: m.answers, Cancelled: false}
	}
}

func (m Model) submitCancelled() tea.Cmd {
	return func() tea.Msg {
		return DoneMsg{Answers: m.answers, Cancelled: true}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if len(m.questions) == 0 {
		return m, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	if m.customOn {
		switch key.String() {
		case "enter":
			m.custom.Blur()
			m.customOn = false
			if txt := strings.TrimSpace(m.custom.Value()); txt != "" {
				m.answers[m.index] = txt
				if m.last() {
					return m, m.submit()
				}
				m.index++
				m.choice = 0
			}
		case "esc":
			m.custom.Blur()
			m.customOn = false
		default:
			var cmd tea.Cmd
			m.custom, cmd = m.custom.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch key.String() {
	case "up":
		if m.choice > 0 {
			m.choice--
		}
	case "down":
		if m.choice < len(m.questions[m.index].Answers) {
			m.choice++
		}
	case "left":
		if m.index > 0 {
			m.index--
			m.choice = 0
		}
	case "right":
		if !m.last() {
			m.index++
			m.choice = 0
		}
	case "enter":
		opts := m.questions[m.index].Answers
		if m.choice >= len(opts) {
			m.openCustom()
			return m, m.custom.Focus()
		}
		m.answers[m.index] = opts[m.choice]
		if m.last() {
			return m, m.submit()
		}
		m.index++
		m.choice = 0
	case "esc":
		return m, m.submitCancelled()
	}
	return m, nil
}

func (m Model) View() string {
	if len(m.questions) == 0 {
		return ""
	}
	inner := m.width - marginSide*2
	if inner < 1 {
		inner = 1
	}
	q := m.questions[m.index]
	answered := m.answers[m.index]

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#1a1b26")).
		Background(style.Questionnaire).
		Width(inner).
		Render(fmt.Sprintf("QUESTION %d/%d", m.index+1, len(m.questions)))

	questionRow := lipgloss.NewStyle().
		Foreground(style.Text).
		Width(inner).
		Render(truncate(q.Question, inner))

	lines := []string{header, questionRow}

	for i := 0; i < maxAnswers; i++ {
		line := lipgloss.NewStyle().Width(inner).Foreground(style.Text)
		text := ""
		if i < len(q.Answers) {
			mark := "○"
			if answered == q.Answers[i] {
				mark = "●"
			}
			text = "  " + mark + " " + truncate(q.Answers[i], inner-6)
		}
		if m.choice == i && !m.customOn {
			line = line.Background(style.Highlight)
		}
		lines = append(lines, line.Render(text))
	}

	if m.customOn {
		lines = append(lines, lipgloss.NewStyle().
			Width(inner).
			Background(style.Highlight).
			Render("  "+m.custom.View()))
	} else {
		text := "  ○ type your own answer"
		if answered != "" && !contains(q.Answers, answered) {
			text = "  ● " + truncate(answered, inner-6)
		}
		line := lipgloss.NewStyle().Width(inner).Foreground(style.Muted)
		if m.choice >= len(q.Answers) {
			line = line.Background(style.Highlight).Foreground(style.Text)
		}
		lines = append(lines, line.Render(text))
	}

	return lipgloss.NewStyle().Margin(0, marginSide).Render(strings.Join(lines, "\n"))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}
