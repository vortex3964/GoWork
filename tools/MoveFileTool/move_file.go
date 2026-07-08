// DESC: package MoveFile implements the move_file tool: moving  a file from one
// path to another within the project root. If the destination is in the same
// directory as the source but has a different filename, this performs a
// rename. Missing destination directories are created automatically, and if
// the move leaves the source's immediate parent directory empty, that parent
// is deleted too (one level only — mirrors delete_file's cleanup rule).
package movefiletool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"GoWork/tools"
)

type Input struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
}

type Tool struct {}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface 
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "move_file" }

func (t *Tool) Description() string {
	return "Moves a file from one path to another, relative to the project root. If the source and destination are in the same directory but have different filenames, this performs a rename. Missing destination directories are created automatically. Overwrites the destination file if one already exists."
}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"source_path": map[string]any{
				"type":        "string",
				"description": "Path to the file to move, relative to the project root (e.g. \"src/foo.go\").",
			},
			"destination_path": map[string]any{
				"type":        "string",
				"description": "New path for the file, relative to the project root (e.g. \"src/bar.go\"). Missing parent directories are created automatically.",
			},
		},
		"required": []string{"source_path", "destination_path"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindWrite }

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs,rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("move_file: invalid input: %w", err)
	}
	if input.SourcePath == "" || input.DestinationPath == "" {
		return tools.Errf("both source_path and destination_path are required"), nil
	}
	if input.SourcePath == "." || input.DestinationPath == "." {
		return tools.Errf("source_path and destination_path must point to a file, not the project root"), nil
	}
	if filepath.Clean(input.SourcePath) == filepath.Clean(input.DestinationPath) {
		return tools.Errf("source_path and destination_path are the same"), nil
	}

	// Read the source file fully before touching the destination, so a failed
	// write never leaves us in a state where the source has already been removed.
	file, err := args.Root.Open(input.SourcePath)
	if err != nil {
		return tools.Errf("error reading source file: %s", err), nil
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		return tools.Errf("error reading source file: %s", err), nil
	}

	destDir := filepath.Dir(input.DestinationPath)
	if destDir != "." {
		if err := tools.MkdirAllInRoot(args.Root, destDir); err != nil {
			return tools.Errf("error creating destination directories: %s", err), nil
		}
	}

	dest, err := args.Root.Create(input.DestinationPath)
	if err != nil {
		return tools.Errf("error writing destination file: %s", err), nil
	}
	if _, err := dest.Write(data); err != nil {
		dest.Close()
		return tools.Errf("error writing destination file: %s", err), nil
	}
	if err := dest.Close(); err != nil {
		return tools.Errf("error writing destination file: %s", err), nil
	}

	if err := args.Root.Remove(input.SourcePath); err != nil {
		return tools.Errf("moved file but failed to remove original at %s: %s", input.SourcePath, err), nil
	}

	extra := ""
	srcParent := filepath.Dir(input.SourcePath)
	// srcParent == "." means the source file was at the project root itself; never remove the root.
	if srcParent != "." {
		if entries, err := fs.ReadDir(args.Root.FS(), srcParent); err == nil && len(entries) == 0 {
			if err := args.Root.Remove(srcParent); err == nil {
				extra = " also deleted now-empty directory " + srcParent
			}
		}
	}

	return tools.Ok(fmt.Sprintf("successfully moved %s to %s%s", input.SourcePath, input.DestinationPath, extra)), nil
}
