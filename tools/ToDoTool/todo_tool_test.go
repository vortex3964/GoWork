package todotool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"GoWork/tools"
	todotool "GoWork/tools/ToDoTool"
	todo "GoWork/Tui/Components/ToDo"
)

type testTool struct {
	tools.AgentTool
	args tools.DispatchArgs
}

func newTool() testTool {
	return testTool{AgentTool: todotool.New()}
}

func runJSON(t *testing.T, tt testTool, input string) (string, bool) {
	t.Helper()
	result, err := tt.Run(context.Background(), tt.args, json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	return result.Content, result.IsError
}

func resetGlobalTodo() {
	todo.GetTodoList().Clear()
}

func mustContain(t *testing.T, content, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Errorf("output missing %q, got:\n%s", want, content)
	}
}

func TestBaselineBuildsList(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	content, isErr := runJSON(t, tt, `{"action":"baseline","tasks":["setup repo","write todo tool","run tests"]}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	mustContain(t, content, "total 3")
	mustContain(t, content, "0. setup repo")
	mustContain(t, content, "2. run tests")

	if got := todo.GetTodoList().Size; got != 3 {
		t.Errorf("global list Size = %d, want 3", got)
	}
}

func TestBaselineReplacesExistingList(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	runJSON(t, tt, `{"action":"baseline","tasks":["old task"]}`)
	content, isErr := runJSON(t, tt, `{"action":"baseline","tasks":["new task"]}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	mustContain(t, content, "total 1")
	if strings.Contains(content, "old task") {
		t.Errorf("baseline should replace the old list, got:\n%s", content)
	}
}

func TestPushAppendsTasks(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	runJSON(t, tt, `{"action":"baseline","tasks":["a"]}`)
	content, isErr := runJSON(t, tt, `{"action":"push","tasks":["b"]}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	mustContain(t, content, "total 2")
	mustContain(t, content, "0. a")
	mustContain(t, content, "1. b")
}

func TestMarkDone(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	runJSON(t, tt, `{"action":"baseline","tasks":["a","b","c"]}`)
	content, isErr := runJSON(t, tt, `{"action":"mark","indices":[0,2]}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	mustContain(t, content, "[x] 0. a")
	mustContain(t, content, "[ ] 1. b")
	mustContain(t, content, "[x] 2. c")
}

func TestClear(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	runJSON(t, tt, `{"action":"baseline","tasks":["a","b"]}`)
	content, isErr := runJSON(t, tt, `{"action":"clear"}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	mustContain(t, content, "total 0")
	if got := todo.GetTodoList().Size; got != 0 {
		t.Errorf("global list Size = %d, want 0", got)
	}
}

func TestListDoesNotMutate(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	runJSON(t, tt, `{"action":"baseline","tasks":["a","b"]}`)
	content, isErr := runJSON(t, tt, `{"action":"list"}`)
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	mustContain(t, content, "total 2")
	mustContain(t, content, "0. a")

	if todo.GetTodoList().Size != 2 {
		t.Errorf("list action mutated the list, Size = %d, want 2", todo.GetTodoList().Size)
	}
}

func TestEndToEndPlanLifecycle(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	steps := []struct {
		input string
		want  string
	}{
		{`{"action":"baseline","tasks":["understand codebase","implement todo tool","write tests","verify build"]}`, "total 4"},
		{`{"action":"mark","indices":[0]}`, "[x] 0. understand codebase"},
		{`{"action":"push","tasks":["fix lint"]}`, "total 5"},
		{`{"action":"mark","indices":[1,2,3,4]}`, "[x] 4. fix lint"},
	}
	for _, s := range steps {
		content, isErr := runJSON(t, tt, s.input)
		if isErr {
			t.Fatalf("unexpected error result for %s: %s", s.input, content)
		}
		mustContain(t, content, s.want)
	}
}

func TestErrors(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	cases := []struct {
		name  string
		input string
	}{
		{"baseline with no tasks", `{"action":"baseline","tasks":[]}`},
		{"push with no tasks", `{"action":"push"}`},
		{"mark with no indices", `{"action":"mark","indices":[]}`},
		{"unknown action", `{"action":"nope"}`},
		{"missing action", `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content, isErr := runJSON(t, tt, c.input)
			if !isErr {
				t.Errorf("expected an error result, got: %s", content)
			}
		})
	}
}

func TestMalformedJSONReturnsGoError(t *testing.T) {
	resetGlobalTodo()
	tt := newTool()

	_, err := tt.Run(context.Background(), tt.args, json.RawMessage(`{not json`))
	if err == nil {
		t.Error("expected a Go error for malformed JSON")
	}
}

func TestNameAndSchema(t *testing.T) {
	tt := newTool()
	if tt.Name() != "todo_list" {
		t.Errorf("Name() = %q, want %q", tt.Name(), "todo_list")
	}
	if len(tt.InputSchema()) == 0 {
		t.Error("InputSchema() returned empty")
	}
}