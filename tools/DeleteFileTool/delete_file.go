// DESC: Package deletefile implements the delete_file tool: deletes a file, and
// if that leaves its immediate parent directory empty, deletes the parent
// too (one level only — it never cascades further up).
package deletefiletool

import (
	"context"
	"encoding/json"
	"fmt"

	"GoWork/tools"
)

type Input struct {
	Path string `json:"path"`
}

type Tool struct {}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface 
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "delete_file" }

func (t *Tool) Description() string {
	return "Deletes a file. If deleting it leaves its immediate parent directory empty, the parent directory is deleted too (never more than one level up)."
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to delete, relative to the project root (e.g. \"src/foo.go\").",
			},
		},
		"required":               []string{"path"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindDelete }

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs ,rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("delete_file: invalid input: %w", err)
	}
	if input.Path == "" {
		return tools.Errf("path is required"), nil
	}

	if err := args.Root.Remove(input.Path); err != nil {
		return tools.Errf("error deleting file: %s", err), nil
	}

	// The file is gone drop its read-state entry too.
	if args.ReadState != nil {
		args.ReadState.Forget(args.PathKey(input.Path))
	}

	return tools.Ok("successfully deleted the file"), nil
}
