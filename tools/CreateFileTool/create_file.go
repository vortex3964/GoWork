// DESC: Package createfile implements the create_file tool: creates a file with
// given content, creating any missing parent directories. Passing a path that
// ends in "/" creates just the directory structure with no file.
package createfiletool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"GoWork/tools"
	cl "GoWork/Tui/Components/ChangesList"
)

type Input struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Tool struct {}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface 
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "create_file" }

func (t *Tool) Description() string {
	return `Creates a file with the given content, relative to the project root, creating any missing parent directories. To create an empty directory without a file, pass a "path" ending in "/" (e.g. "src/newdir/").`
}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `Path of the file to create, relative to the project root (e.g. "src/foo.go"). End with "/" to create only a directory, with no file.`,
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write into the file. Ignored if path ends with \"/\".",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindWrite }

func wrapInMarkers(content string) []byte {
	var b strings.Builder
	b.WriteString("<<<<<<< old\n")
	b.WriteString("=======\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(">>>>>>> Ai change\n")
	return []byte(b.String())
}
 
func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("create_file: invalid input: %w", err)
	}
	if input.Path == "" {
		return tools.Errf("path is required"), nil
	}
 
	if strings.HasSuffix(input.Path, "/") || strings.HasSuffix(input.Path, "\\") {
		dir := filepath.Clean(input.Path)
		if err := tools.MkdirAllInRoot(args.Root, dir); err != nil {
			return tools.Errf("error creating directories: %s", err), nil
		}
		return tools.Ok(fmt.Sprintf("successfully created directory %s", filepath.Join(args.RootPath, dir))), nil
	}
 
	dir := filepath.Dir(input.Path)
	if err := tools.MkdirAllInRoot(args.Root, dir); err != nil {
		return tools.Errf("error creating directories: %s", err), nil
	}
 
	marked := wrapInMarkers(input.Content)
 
	file, err := args.Root.Create(input.Path)
	if err != nil {
		return tools.Errf("error writing file: %s", err), nil
	}
	defer file.Close()
 
	if _, err := file.Write(marked); err != nil {
		return tools.Errf("error writing file: %s", err), nil
	}
 
	args.WatchList.Add(input.Path)
	args.WatchList.Changeslist[input.Path] = cl.ChangeList{
		Changes: cl.GetDiffsBytes(marked, input.Path),
	}
 
	return tools.Ok(fmt.Sprintf("successfully created file at %s", filepath.Join(args.RootPath, input.Path))), nil
}
