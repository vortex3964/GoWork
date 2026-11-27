package todo

import (
	"fmt"
	"strings"
	"sync"
)

type Todoitem struct {
	Description string
	marked      bool
}

const MaxTodos = 7

// TodoList is the shared todo state. Tools mutate it from parallel goroutines
// while the tui reads it, so every method guards the internal slice with mu.
type TodoList struct {
	mu    sync.Mutex
	items []Todoitem
	index int
}

// todo_list is the single global todo list for the whole app. The ai tools
// populate it from the model and the tui reads it to render progress, so
// everyone shares one slice of state.
var todo_list TodoList

func InitTodoItem(desc string) Todoitem {
	return Todoitem{Description: desc, marked: false}
}

func InitTodoList() *TodoList {
	return &TodoList{items: []Todoitem{}, index: 0}
}

// GetTodoList returns the global todo list shared across tools and the tui.
func GetTodoList() *TodoList {
	return &todo_list
}

func (t *TodoList) AddItems(items []Todoitem) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.items = append(t.items, items...)
	for len(t.items) > MaxTodos {
		t.items = t.items[1:]
	}
}

func (t *TodoList) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.items = []Todoitem{}
	t.index = 0
}

func (t *TodoList) MarkItems(idx []int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, i := range idx {
		if i < 0 || i >= len(t.items) {
			continue
		}
		t.items[i].marked = true
	}
}

// Marked reports whether the item at idx was marked done. Out of range
// indices return false.
func (t *TodoList) Marked(idx int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.items) {
		return false
	}
	return t.items[idx].marked
}

// Size returns how many items the list currently holds.
func (t *TodoList) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.items)
}

// List returns a snapshot of all items so the tui can render them without
// touching the internal slice directly.
func (t *TodoList) List() []Todoitem {
	t.mu.Lock()
	defer t.mu.Unlock()

	items := make([]Todoitem, len(t.items))
	copy(items, t.items)
	return items
}

func (t *TodoList) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var b strings.Builder
	for i, item := range t.items {
		mark := "[ ] "
		if item.marked {
			mark = "[x] "
		}
		fmt.Fprintf(&b, "%s%d. %s\n", mark, i, item.Description)
	}
	return b.String()
}
