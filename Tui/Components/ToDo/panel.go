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

func RenderTodoList(items []Todoitem, width, height int) string {
	if width < 3 {
		width = 3
	}
	if height < 3 {
		height = 3
	}

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Text).
		Background(style.Panel).
		Render("TODO")

	var body strings.Builder
	maxRows := MaxTodos
	if height-1 < maxRows {
		maxRows = height - 1
	}
	for i := 0; i < maxRows; i++ {
		line := ""
		if i < len(items) {
			txt := fmt.Sprintf("%s %s", checkbox(items[i].marked), items[i].Description)
			if items[i].marked {
				line = lipgloss.NewStyle().
					Foreground(style.Success).
					Background(style.Panel).
					Render(txt)
			} else {
				line = lipgloss.NewStyle().
					Foreground(style.Text).
					Background(style.Panel).
					Render(txt)
			}
		}
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		body.WriteString(line)
	}

	content := title + "\n" + body.String()

	return lipgloss.NewStyle().
		Background(style.Panel).
		Width(width).
		Height(height).
		Render(content)
}
