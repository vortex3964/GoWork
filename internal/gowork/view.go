package gowork

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	changeslist "GoWork/Tui/Components/ChangesList"
	todo "GoWork/Tui/Components/ToDo"
	"GoWork/Tui/Style"
)

func (m model) emptyStateHeight() int {
	h := m.winHeight - topBarHeight - spinnerHeight - statusLineHeight - m.bottomBarHeight()
	h -= 2
	if h < 0 {
		h = 0
	}
	return h
}

func (m model) renderLogo() string {
	return m.renderLogoWidth(m.winWidth)
}

func (m model) renderLogoWidth(width int) string {
	availH := m.emptyStateHeight()

	// The logo block is fixed-size: on short terminals it would overflow the
	// band and squeeze the prompt bar and the statusline off-screen, so leave
	// a bare band instead of centering an unclippable logo.
	if availH < 6 || len(m.logoLines) > availH-2 {
		return lipgloss.Place(width, availH, lipgloss.Center, lipgloss.Center, "")
	}
	if len(m.logoLines) == 0 {
		return lipgloss.Place(width, availH, lipgloss.Center, lipgloss.Center, "")
	}

	logoWidth := 0
	for _, line := range m.logoLines {
		if w := lipgloss.Width(line); w > logoWidth {
			logoWidth = w
		}
	}

	block := strings.Join(m.logoLines, "\n")

	return lipgloss.Place(
		width, availH,
		lipgloss.Center, lipgloss.Center,
		lipgloss.NewStyle().Width(logoWidth).Render(block),
	)
}

func (m model) View() tea.View {
	top := m.Tabs.View()
	var content string

	switch m.Tabs.Active().Name {
	case "stats":
		// Stats mode hides the prompt bar and pins the statusline; only the
		// dashboard sits between them.
		content = top + "\n" + m.Stats.View() + "\n" + renderStatusLine(m)
	case "skills":
		// Skills uses the same layout as the code tab: the pane sits in
		// the message-area band, the prompt bar and the statusline stay
		// pinned at the bottom, and the skill-form modal floats over the
		// pane when it's open.
		content = top + "\n" + m.Skills.View()
		content += "\n"
		if *m.AiThink && m.Mode != modeQuestionnaire {
			content += lipgloss.NewStyle().Margin(0, 3).Foreground(style.Info).Render(m.spinner.View() + " thinking....")
		}
		content += "\n"
		content += m.Prompt.View()
		content += "\n"
		content += renderStatusLine(m)
		content = m.skillForm.Overlay(content)
	case "tools":
		// The tools tab shares the skills layout: the three-section pane
		// fills the message band, the prompt bar and statusline stay pinned,
		// and the add form / skill-creation modal float over the pane.
		content = top + "\n" + m.Tools.View()
		content += "\n"
		if *m.AiThink && m.Mode != modeQuestionnaire {
			content += lipgloss.NewStyle().Margin(0, 3).Foreground(style.Info).Render(m.spinner.View() + " thinking....")
		}
		content += "\n"
		content += m.Prompt.View()
		content += "\n"
		content += renderStatusLine(m)
		content = m.Tools.Overlay(content)
		content = m.skillForm.Overlay(content)
	default:
		content += top + "\n"
		msgWidth := m.msgAreaWidth()
		var band string
		if m.Mode == modeChangeHandling {
			band = m.ChangesList.View()
		} else if m.MessageArea.Size() == 0 {
			band = m.renderLogoWidth(msgWidth)
		} else {
			band = m.MessageArea.View()
		}
		if m.todoPanelOpen && m.Mode != modeChangeHandling {
			panel := todo.RenderTodoList(
				todo.GetTodoList().List(),
				m.panelWidth(),
				m.promptTop()-m.panelTop(),
			)
			band = lipgloss.JoinHorizontal(lipgloss.Top, band, panel)
		}
		content += band
		content += "\n"
		if *m.AiThink && m.Mode != modeQuestionnaire {
			content += lipgloss.NewStyle().Margin(0, 3).Foreground(style.Info).Render(m.spinner.View() + " thinking....")
		}
		content += "\n"
		if m.Mode == modeQuestionnaire {
			content += m.questionnaire.View()
		} else {
			content += m.Prompt.View()
		}
		content += "\n"
		content += renderStatusLine(m)
		content = m.overlayTodoArrow(content)
	}

	if m.Mode == modeProviderSelect {
		content = m.providerSelect.Overlay(content)
	}

	if m.Mode == modePermission {
		content = m.permissionBox.Overlay(content)
	}

	if m.popUp.IsVisible() {
		content = m.popUp.Overlay(content)
	}

	if m.modal.IsVisible() {
		content = m.modal.Overlay(content)
	}

	if m.Coffee.IsVisible() {
		content = m.Coffee.Overlay(content)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // required in v2 for any mouse msg to arrive at all
	v.BackgroundColor = style.Background
	return v
}

func (m model) overlayTodoArrow(content string) string {
	glyph := todo.Arrow(m.todoPanelOpen)
	x := m.arrowCol()
	y := m.arrowRow()
	if x < 0 || y < 0 {
		return content
	}

	stylized := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Border).
		Background(style.Muted).
		Render(glyph)

	base := lipgloss.NewLayer(content).X(0).Y(0).Z(0)
	floating := lipgloss.NewLayer(stylized).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, floating).Render()
}

// maxChangePreviewLines caps how many diff lines an accept/reject message
// inlines so a single huge change can't flood the message area.
const maxChangePreviewLines = 30

// changeEventMarkdown renders an accept/reject summary as markdown: a
// blockquote header, then a fenced ```diff block whose +/- lines the message
// area's syntax highlighter colors (green for accepted new code, red for
// rejected old code).
func changeEventMarkdown(msg changeslist.ChangeEventMsg) string {
	icon, verb, mark := "✓", "**ACCEPTED**", "+"
	if msg.Rejected {
		icon, verb, mark = "✗", "**REJECTED**", "-"
	}

	var b strings.Builder
	b.WriteString("> " + icon + " " + verb + " `" + msg.Summary + "`")

	if len(msg.Lines) > 0 {
		b.WriteString("\n\n```diff\n")
		lines := msg.Lines
		more := 0
		if len(lines) > maxChangePreviewLines {
			lines = lines[:maxChangePreviewLines]
			more = len(msg.Lines) - maxChangePreviewLines
		}
		for _, line := range lines {
			b.WriteString(mark + " " + line + "\n")
		}
		if more > 0 {
			b.WriteString(mark + fmt.Sprintf(" … %d more lines", more) + "\n")
		}
		b.WriteString("```")
	}
	return b.String()
}
