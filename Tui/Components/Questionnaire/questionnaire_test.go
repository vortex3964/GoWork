package questionnaire

import (
	"fmt"
	"sync"
	"testing"
)

func TestActiveReturnsDefensiveCopy(t *testing.T) {
	SetActive([]Question{{
		Question: "pick one",
		Answers:  []string{"a", "b"},
	}})
	t.Cleanup(Clear)

	active := Active()
	if len(active) != 1 {
		t.Fatalf("Active() returned %d questions, want 1", len(active))
	}
	active[0].Question = "mutated"
	active[0].Answers[0] = "mutated"

	fresh := Active()
	if fresh[0].Question != "pick one" {
		t.Errorf("mutating Active() result changed internal question: %q", fresh[0].Question)
	}
	if fresh[0].Answers[0] != "a" {
		t.Errorf("mutating Active() result changed internal answer: %q", fresh[0].Answers[0])
	}
}

func TestSetActiveCopiesInput(t *testing.T) {
	qs := []Question{{
		Question: "pick one",
		Answers:  []string{"a", "b"},
	}}
	SetActive(qs)
	t.Cleanup(Clear)

	qs[0].Question = "mutated"
	qs[0].Answers[0] = "mutated"

	active := Active()
	if active[0].Question != "pick one" {
		t.Errorf("mutating SetActive input changed internal question: %q", active[0].Question)
	}
	if active[0].Answers[0] != "a" {
		t.Errorf("mutating SetActive input changed internal answer: %q", active[0].Answers[0])
	}
}

func TestClearDropsQuestionnaire(t *testing.T) {
	SetActive([]Question{{Question: "q"}})
	Clear()

	if got := Active(); got != nil {
		t.Errorf("Active() after Clear() = %v, want nil", got)
	}
}

func TestSetActiveNilStaysNil(t *testing.T) {
	SetActive([]Question{{Question: "q"}})
	SetActive(nil)

	if got := Active(); got != nil {
		t.Errorf("Active() after SetActive(nil) = %v, want nil", got)
	}
}

func TestConcurrentSetActiveActiveClear(t *testing.T) {
	Clear()
	t.Cleanup(Clear)

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
		SetActive([]Question{{
			Question: fmt.Sprintf("question-%d", i),
			Answers:  []string{"a", "b", "c"},
		}})
	})
	run(func(i int) {
		Clear()
	})
	run(func(i int) {
		for _, q := range Active() {
			_ = q.Question
			for _, answer := range q.Answers {
				_ = answer
			}
		}
	})
	run(func(i int) {
		// A reader that mutates its copy must never touch the global state or
		// race with the writer building a new questionnaire.
		active := Active()
		if len(active) > 0 && len(active[0].Answers) > 0 {
			active[0].Question = "mutated"
			active[0].Answers[0] = "mutated"
		}
	})

	close(start)
	wg.Wait()

	Clear()
	if got := Active(); got != nil {
		t.Errorf("Active() after final Clear() = %v, want nil", got)
	}
}
