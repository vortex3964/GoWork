package readfiletool

import (
	"context"
	"encoding/json"
	"fmt"
	filepath "path/filepath"
	"strings"

	"GoWork/tools"
)

type Input struct {
	Path string `json:"path"`
	Start int `json:"starting_line"`
	Offset int `json:"offset_lines"`
}

type Tool struct {}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface 
func New() tools.AgentTool { return &Tool{} }

func (t* Tool) Name() string { return "read_file" }

func (t *Tool) Description() string {
	return `Read a range of lines from a text file.

Returns the requested lines with 1-indexed line numbers prefixed, so you can reference exact locations in later edits. Large files are not returned in full use starting_line and offset_lines to page through them.

Parameters:
- path: relative or absolute path to the file.
- starting_line: 1-indexed line number to start reading from. Omit or set to 1 to start at the beginning of the file.
- offset_lines: number of lines to return starting from starting_line. If omitted, reads to the end of the file (capped internally for very large files you'll be told if output was truncated, and can page further with a later call).

If the file has changed since you last read it, the returned content reflects the current state on disk`

}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `Path of the file to read, relative to the project root (e.g. "src/foo.go").`,
			},
			"starting_line": map[string]any{
				"type":        "integer",
				"description": "1-indexed line number to start reading from. Defaults to 1 if omitted.",
			},
			"offset_lines": map[string]any{
				"type":        "integer",
				"description": "Number of lines to read starting from starting_line. Defaults to the rest of the file if omitted.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindRead }

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {

}
