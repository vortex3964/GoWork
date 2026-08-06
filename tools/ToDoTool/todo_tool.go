// DESC: todo tool lets the ai build and maintain a global todo list for the
// current session. The model lays down a baseline of tasks up front (so it
// never gets lost or finishes early) and pushes/marks/clears items as it
// works. The same global list is what the tui renders, so agent progress is
// visible to the user.
package todotool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"GoWork/tools"
	todo "GoWork/Tui/Components/ToDo"
)

type Input struct {
	Action  string   `json:"action"`            // "baseline" | "push" | "mark" | "clear" | "list"
	Tasks   []string `json:"tasks,omitempty"`   // descriptions for push/baseline
	Indices []int    `json:"indices,omitempty"` // list indexes to mark done
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "todo_list" }

func (t *Tool) Kind() tools.Kind { return tools.KindWrite }

func (t *Tool) Description() string {
	return `Maintains the session's global todo list that keeps the agent on track and shows progress to the user.

Use at the START of any multi-step task to lay down a baseline of the steps you plan to take, in order, so you don't lose your place. Then keep it updated as you work.

Actions:
- "baseline": replace the entire list with tasks. Use once at the start of a task to establish your plan.
- "push": append new tasks to the end of the list (use as you discover follow-up work).
- "mark": mark the given indices as done (call it as soon as each step actually completes, don't batch).
- "clear": empty the list (for when the task is finished or you restart).
- "list": return the current list unchanged.

Keep tasks specific and actionable. When you are blocked, keep the task open and push a new one describing the blocker.`
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"baseline", "push", "mark", "clear", "list"},
				"description": "The operation to perform on the todo list.",
			},
			"tasks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task descriptions. Required for baseline and push.",
			},
			"indices": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Zero-based indices of tasks to mark done. Required for mark.",
			},
		},
		"required":               []string{"action"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("todo_list: invalid input: %w", err)
	}

	list := todo.GetTodoList()

	switch strings.ToLower(input.Action) {
	case "baseline":
		if len(input.Tasks) == 0 {
			return tools.Errf("baseline requires at least one task in \"tasks\""), nil
		}
		list.Clear()
		list.AddItems(todoItems(input.Tasks))
	case "push":
		if len(input.Tasks) == 0 {
			return tools.Errf("push requires at least one task in \"tasks\""), nil
		}
		list.AddItems(todoItems(input.Tasks))
	case "mark":
		if len(input.Indices) == 0 {
			return tools.Errf("mark requires at least one index in \"indices\""), nil
		}
		list.MarkItems(input.Indices)
	case "clear":
		list.Clear()
	case "list":
		// nothing to mutate, just report below
	default:
		return tools.Errf("unknown action %q (use baseline, push, mark, clear or list)", input.Action), nil
	}

	content := fmt.Sprintf("total %d:\n%s", list.Size, list.String())
	return tools.Ok(content), nil
}

func todoItems(descs []string) []todo.Todoitem {
	items := make([]todo.Todoitem, 0, len(descs))
	for _, d := range descs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		items = append(items, todo.InitTodoItem(d))
	}
	return items
}