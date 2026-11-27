package todo

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderTodoListClipsToAllocatedSize(t *testing.T) {
	items := []Todoitem{
		{Description: strings.Repeat("a very long task description that must not wrap ", 10)},
		{Description: "second task", marked: true},
		{Description: "third task"},
		{Description: strings.Repeat("任务描述很长的待办事项", 10)}, // wide runes
	}

	sizes := []struct {
		width  int
		height int
	}{
		{20, 5},  // the audit's overflowing case
		{20, 10}, // more rows requested than filled
		{40, 8},  // wider panel, fewer rows than MaxTodos
		{1, 1},   // degenerate but must still be exact
		{2, 3},   // narrower than the checkbox
	}

	for _, size := range sizes {
		got := RenderTodoList(items, size.width, size.height)
		if w := lipgloss.Width(got); w != size.width {
			t.Errorf("RenderTodoList(%d, %d) width = %d, want %d:\n%s", size.width, size.height, w, size.width, got)
		}
		if h := lipgloss.Height(got); h != size.height {
			t.Errorf("RenderTodoList(%d, %d) height = %d, want %d:\n%s", size.width, size.height, h, size.height, got)
		}
		for i, line := range strings.Split(got, "\n") {
			if w := lipgloss.Width(line); w != size.width {
				t.Errorf("RenderTodoList(%d, %d) line %d width = %d, want %d", size.width, size.height, i, w, size.width)
			}
		}
	}
}

func TestRenderTodoListKeepsTitleAndCheckboxes(t *testing.T) {
	items := []Todoitem{
		{Description: "open task"},
		{Description: "done task", marked: true},
	}

	got := RenderTodoList(items, 24, 5)
	for _, want := range []string{"TODO", "[ ] open task", "[●] done task"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in rendered panel, got:\n%s", want, got)
		}
	}
}
