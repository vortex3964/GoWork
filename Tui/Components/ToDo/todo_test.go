package todo_test

import (
	"strings"
	"testing"

	todo "GoWork/Tui/Components/ToDo"
)

func TestInitTodoItem(t *testing.T) {
	item := todo.InitTodoItem("ship the feature")
	if item.Description != "ship the feature" {
		t.Errorf("Description = %q, want %q", item.Description, "ship the feature")
	}
}

func TestFreshItemUnmarked(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()
	list.AddItems([]todo.Todoitem{todo.InitTodoItem("x")})

	if list.Marked(0) {
		t.Error("freshly added item should not be marked")
	}
}

func TestInitTodoList(t *testing.T) {
	list := todo.InitTodoList()
	if len(list.Items) != 0 {
		t.Errorf("Items len = %d, want 0", len(list.Items))
	}
	if list.Size != 0 {
		t.Errorf("Size = %d, want 0", list.Size)
	}
	if list.Index != 0 {
		t.Errorf("Index = %d, want 0", list.Index)
	}
}

func TestAddItems(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()

	items := []todo.Todoitem{
		todo.InitTodoItem("setup repo"),
		todo.InitTodoItem("write todo tool"),
	}
	list.AddItems(items)

	if list.Size != 2 {
		t.Errorf("Size = %d, want 2", list.Size)
	}
	if len(list.Items) != 2 {
		t.Errorf("Items len = %d, want 2", len(list.Items))
	}
}

func TestAddItemsIsCumulative(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()

	list.AddItems([]todo.Todoitem{todo.InitTodoItem("a")})
	list.AddItems([]todo.Todoitem{todo.InitTodoItem("b"), todo.InitTodoItem("c")})

	if list.Size != 3 {
		t.Errorf("Size = %d, want 3", list.Size)
	}
}

func TestClear(t *testing.T) {
	list := todo.GetTodoList()
	list.AddItems([]todo.Todoitem{
		todo.InitTodoItem("a"),
		todo.InitTodoItem("b"),
	})
	list.Index = 5
	list.Clear()

	if list.Size != 0 {
		t.Errorf("Size = %d, want 0", list.Size)
	}
	if len(list.Items) != 0 {
		t.Errorf("Items len = %d, want 0", len(list.Items))
	}
	if list.Index != 0 {
		t.Errorf("Index = %d, want 0", list.Index)
	}
}

func TestMarkItems(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()
	list.AddItems([]todo.Todoitem{
		todo.InitTodoItem("a"),
		todo.InitTodoItem("b"),
		todo.InitTodoItem("c"),
	})

	list.MarkItems([]int{0, 2})

	if !list.Marked(0) {
		t.Error("item 0 should be marked")
	}
	if list.Marked(1) {
		t.Error("item 1 should stay unmarked")
	}
	if !list.Marked(2) {
		t.Error("item 2 should be marked")
	}
}

func TestMarkItemsOutOfRangeIsIgnored(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()
	list.AddItems([]todo.Todoitem{todo.InitTodoItem("a")})

	list.MarkItems([]int{-1, 0, 99})

	if !list.Marked(0) {
		t.Error("valid index 0 should be marked")
	}
	// no panic, no corruption of Size
	if list.Size != 1 {
		t.Errorf("Size = %d, want 1", list.Size)
	}
}

func TestMarkedOutOfRange(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()

	if list.Marked(0) {
		t.Error("Marked on an empty list should be false")
	}
	if list.Marked(-1) {
		t.Error("Marked(-1) should be false")
	}
}

func TestListReturnsItems(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()
	list.AddItems([]todo.Todoitem{todo.InitTodoItem("a")})

	if got := list.List(); len(got) != 1 {
		t.Errorf("List len = %d, want 1", len(got))
	}
}

func TestString(t *testing.T) {
	list := todo.GetTodoList()
	list.Clear()
	list.AddItems([]todo.Todoitem{
		todo.InitTodoItem("first"),
		todo.InitTodoItem("second"),
	})
	list.MarkItems([]int{0})

	out := list.String()

	for _, want := range []string{"[x] 0. first", "[ ] 1. second"} {
		if !strings.Contains(out, want) {
			t.Errorf("String() missing %q, got:\n%s", want, out)
		}
	}
}

func TestGetTodoListIsGlobalSingleton(t *testing.T) {
	a := todo.GetTodoList()
	b := todo.GetTodoList()
	if a != b {
		t.Error("GetTodoList should return the same global instance every time")
	}

	a.Clear()
	a.AddItems([]todo.Todoitem{todo.InitTodoItem("shared via global")})
	if b.Size != 1 {
		t.Errorf("mutation via one handle not visible through the other: Size = %d, want 1", b.Size)
	}
}