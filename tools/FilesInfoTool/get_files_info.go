// Package fileinfo implements the get_files_info tool: listing the
// immediate (non-recursive) contents of a directory.
package fileinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"

	"GoWork/tools"
)

type Input struct {
	Path string `json:"path"`
}

type Tool struct {}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface 
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "get_files_info" }

func (t *Tool) Description() string {
	return "Lists the immediate (non-recursive) contents of a directory: whether each entry is a file or a directory, and file size in bytes. Respects .gitignore and .agentignore."
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `Directory to inspect, relative to the project root. Use "." for the root itself.`,
			},
		},
		"required": []string{"path"},
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindRead }

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs , rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("get_files_info: invalid input: %w", err)
	}

	dir := input.Path

	if dir == "" {
		dir = "."
	}

	entries, err := fs.ReadDir(args.Root.FS(), dir)
	if err != nil {
		return tools.Errf("error reading directory: %s", err), nil
	}

	ignores := tools.LoadIgnores(args.RootPath)

	var resp string
	for _, entry := range entries {
		rel := entry.Name()
		if dir != "." {
			rel = filepath.Join(dir, entry.Name())
		}
		if tools.IsIgnored(ignores, rel) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if entry.IsDir() {
			resp += entry.Name() + "/: dir\n"
		} else {
			resp += entry.Name() + ": file, " + strconv.FormatInt(info.Size(), 10) + " bytes\n"
		}
	}

	if resp == "" {
		return tools.Ok("directory is empty (or all contents are ignored)"), nil
	}
	return tools.Ok(resp), nil
}
