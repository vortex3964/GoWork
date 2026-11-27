package todo

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// Arrow returns the toggle glyph shown at the edge of the todo panel. It
// points right (opens) when the panel is closed and left (closes) when it
// is open.
func Arrow(open bool) string {
	if open {
		return "❯"
	}
	return "❮"
}

func checkbox(marked bool) string {
	if marked {
		return "[●]"
	}
	return "[ ]"
}

// fitLine clips text to width cells and pads it back to exactly width cells
// before styling. Keeping every row pre-sized means lipgloss never wraps a
// long task into extra rows when the panel is rendered.
func fitLine(text string, width int, st lipgloss.Style) string {
	text = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(text)
	text = lipgloss.NewStyle().MaxWidth(width).Render(text)
	if pad := width - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return st.Render(text)
}

// RenderTodoList renders the todo panel at exactly width x height cells.
// Long descriptions are clipped to one row each instead of wrapping, and any
// rows the items do not fill are padded with the panel background.
func RenderTodoList(items []Todoitem, width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Text).
		Background(style.Panel)
	openStyle := lipgloss.NewStyle().
		Foreground(style.Text).
		Background(style.Panel)
	doneStyle := lipgloss.NewStyle().
		Foreground(style.Success).
		Background(style.Panel)
	blankStyle := lipgloss.NewStyle().
		Background(style.Panel)

	lines := make([]string, 0, height)
	lines = append(lines, fitLine("TODO", width, titleStyle))

	maxRows := height - 1
	if maxRows > MaxTodos {
		maxRows = MaxTodos
	}
	for i := 0; i < height-1; i++ {
		if i >= maxRows || i >= len(items) {
			lines = append(lines, fitLine("", width, blankStyle))
			continue
		}
		txt := fmt.Sprintf("%s %s", checkbox(items[i].marked), items[i].Description)
		if items[i].marked {
			lines = append(lines, fitLine(txt, width, doneStyle))
		} else {
			lines = append(lines, fitLine(txt, width, openStyle))
		}
	}

	return strings.Join(lines, "\n")
}
