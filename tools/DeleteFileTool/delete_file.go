// DESC: Package deletefile implements the delete_file tool: deletes a file, and
// if that leaves its immediate parent directory empty, deletes the parent
// too (one level only — it never cascades further up).
package deletefiletool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"GoWork/tools"
)

type Input struct {
	Path string `json:"path"`
}

type Tool struct {
	root *os.Root
}

func New(projectRoot string) (*Tool, error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving project root: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("opening project root: %w", err)
	}
	return &Tool{root: root}, nil
}

func (t *Tool) Name() string { return "delete_file" }

func (t *Tool) Description() string {
	return "Deletes a file. If deleting it leaves its immediate parent directory empty, the parent directory is deleted too (never more than one level up)."
}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to delete, relative to the project root (e.g. \"src/foo.go\").",
			},
		},
		"required": []string{"path"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindDelete }

func (t *Tool) Run(ctx context.Context, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("delete_file: invalid input: %w", err)
	}
	if input.Path == "" {
		return tools.Errf("path is required"), nil
	}

	if err := t.root.Remove(input.Path); err != nil {
		return tools.Errf("error deleting file: %s", err), nil
	}

	parent := filepath.Dir(input.Path)
	extra := ""
	// parent == "." means the file was at the project root itself; never attempt to remove the root.
	if parent != "." {
		if entries, err := fs.ReadDir(t.root.FS(), parent); err == nil && len(entries) == 0 {
			if err := t.root.Remove(parent); err == nil {
				extra = " also deleted directory " + parent
			}
		}
	}

	return tools.Ok("successfully deleted the file" + extra), nil
}
