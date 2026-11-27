package todo

import (
	"fmt"
	"sync"
	"testing"
)

func TestListReturnsDefensiveCopy(t *testing.T) {
	list := InitTodoList()
	list.AddItems([]Todoitem{InitTodoItem("one"), InitTodoItem("two")})

	got := list.List()
	if len(got) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(got))
	}

	got[0].Description = "mutated"
	got[0].marked = true
	got[1] = InitTodoItem("replaced")

	fresh := list.List()
	if fresh[0].Description != "one" {
		t.Errorf("mutating List() result changed internal description: %q", fresh[0].Description)
	}
	if fresh[0].marked {
		t.Error("mutating List() result changed internal marked state")
	}
	if fresh[1].Description != "two" {
		t.Errorf("mutating List() result changed internal item: %q", fresh[1].Description)
	}
}

func TestAddItemsCopiesInputItems(t *testing.T) {
	list := InitTodoList()
	items := []Todoitem{InitTodoItem("original")}
	list.AddItems(items)

	items[0].Description = "mutated"

	if got := list.List()[0].Description; got != "original" {
		t.Errorf("mutating input slice changed internal item: %q", got)
	}
}

func TestConcurrentTodoListAccess(t *testing.T) {
	list := GetTodoList()
	list.Clear()
	t.Cleanup(list.Clear)

	const iterations = 300
	start := make(chan struct{})
	var wg sync.WaitGroup

	run := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				fn(i)
			}
		}()
	}

	run(func(i int) {
		list.AddItems([]Todoitem{InitTodoItem(fmt.Sprintf("task-%d", i))})
	})
	run(func(i int) {
		list.AddItems([]Todoitem{InitTodoItem("a"), InitTodoItem("b")})
	})
	run(func(i int) {
		list.MarkItems([]int{-1, 0, i % MaxTodos, MaxTodos})
	})
	run(func(i int) {
		list.Clear()
	})
	run(func(i int) {
		for _, item := range list.List() {
			_ = item.Description
			_ = item.marked
		}
	})
	run(func(i int) {
		_ = list.String()
		_ = list.Size()
		_ = list.Marked(i % MaxTodos)
		_ = list.Marked(-1)
		_ = list.Marked(MaxTodos)
	})
	run(func(i int) {
		// A reader holding a snapshot must never see it change underneath it.
		snapshot := list.List()
		before := len(snapshot)
		_ = list.String()
		if after := len(snapshot); after != before {
			t.Errorf("snapshot length changed from %d to %d", before, after)
		}
	})

	close(start)
	wg.Wait()

	if got := list.Size(); got > MaxTodos {
		t.Errorf("Size() = %d, want <= %d", got, MaxTodos)
	}
	if got, want := len(list.List()), list.Size(); got != want {
		t.Errorf("len(List()) = %d, Size() = %d, want equal", got, want)
	}
}
