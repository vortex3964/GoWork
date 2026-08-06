package todo

import (
	"fmt"
	"strings"
)

type Todoitem struct {
	Description string
	marked      bool
}

const MaxTodos = 7

type TodoList struct {
	Items []Todoitem
	Index int
	Size  int
}

// todo_list is the single global todo list for the whole app. The ai tools
// populate it from the model and the tui reads it to render progress, so
// everyone shares one slice of state.
var todo_list TodoList

func InitTodoItem(desc string) Todoitem {
	return Todoitem{Description: desc, marked: false}
}

func InitTodoList() TodoList {
	return TodoList{Items: []Todoitem{}, Index: 0, Size: 0}
}

// GetTodoList returns the global todo list shared across tools and the tui.
func GetTodoList() *TodoList {
	return &todo_list
}

func (t *TodoList) AddItems(items []Todoitem) {
	t.Items = append(t.Items, items...)
	t.Size += len(items)
	for len(t.Items) > MaxTodos {
		t.Items = t.Items[1:]
		t.Size = len(t.Items)
	}
}

func (t *TodoList) Clear() {
	t.Items = []Todoitem{}
	t.Size = 0
	t.Index = 0
}

func (t *TodoList) MarkItems(idx []int) {
	for _, i := range idx {
		if i < 0 || i >= len(t.Items) {
			continue
		}
		t.Items[i].marked = true
	}
}

// Marked reports whether the item at idx was marked done. Out of range
// indices return false.
func (t *TodoList) Marked(idx int) bool {
	if idx < 0 || idx >= len(t.Items) {
		return false
	}
	return t.Items[idx].marked
}

// List returns all items so the tui can render them without touching the
// internal slice directly.
func (t *TodoList) List() []Todoitem {
	return t.Items
}

func (t *TodoList) String() string {
	var b strings.Builder
	for i, item := range t.Items {
		mark := "[ ] "
		if item.marked {
			mark = "[x] "
		}
		fmt.Fprintf(&b, "%s%d. %s\n", mark, i, item.Description)
	}
	return b.String()
}
