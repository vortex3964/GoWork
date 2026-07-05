// Package fileinfo implements the get_files_info tool: listing the
// immediate (non-recursive) contents of a directory.
package fileinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"GoWork/tools"
)

type Input struct {
	Path string `json:"path"`
}

// Tool implements tools.AgentTool for listing directory contents.
type Tool struct {
	root     *os.Root // sandboxed handle — operations cannot escape this, even via symlinks
	rootPath string    // absolute path on disk, needed only for LoadIgnores
}

// New creates a get_files_info tool sandboxed to projectRoot.
func New(projectRoot string) (*Tool, error) {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving project root: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("opening project root: %w", err)
	}
	return &Tool{root: root, rootPath: abs}, nil
}

func (t *Tool) Name() string { return "get_files_info" }

func (t *Tool) Description() string {
	return "Lists the immediate (non-recursive) contents of a directory: whether each entry is a file or a directory, and file size in bytes. Respects .gitignore and .agentignore."
}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `Directory to inspect, relative to the project root. Use "." for the root itself.`,
			},
		},
		"required": []string{"path"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindRead }

func (t *Tool) Run(ctx context.Context, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("get_files_info: invalid input: %w", err)
	}

	dir := input.Path

	if dir == "" {
		dir = "."
	}

	entries, err := fs.ReadDir(t.root.FS(), dir)
	if err != nil {
		return tools.Errf("error reading directory: %s", err), nil
	}

	ignores := tools.LoadIgnores(t.rootPath)

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
