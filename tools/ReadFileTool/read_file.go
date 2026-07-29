package readfiletool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"io"
	"path/filepath"
	"strings"

	"GoWork/tools"
	cl "GoWork/Tui/Components/ChangesList"
)

const DEFAULT_MAX_BYTES int = 50 * 1024
const DEFAULT_MAX_LINES int = 1800

type Input struct {
	Path   string `json:"path"`
	Start  int    `json:"starting_line"`
	Offset int    `json:"offset_lines"`
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "read_file" }

func (t *Tool) Description() string {
	return `Read a range of lines from a text file.

Returns the requested lines with 1-indexed line numbers prefixed, so you can reference exact locations in later edits. Large files are not returned in full use starting_line and offset_lines to page through them.`
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
				"description": fmt.Sprintf("Number of lines to read starting from starting_line. Defaults to the rest of the file if omitted, capped at %d lines.", DEFAULT_MAX_LINES),
			},
		},
		"required": []string{"path"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindRead }

// readRange copies relPath from the sandboxed root into a temporary file,
// applies any pending changes from the WatchList, and scans forward through
// the copy to return the requested line range.
func readRange(root *os.Root, wl *cl.WatchList, relPath string, start, offset int) (content string, truncated bool, ok bool, lastLine int, err error) {
	//Open the original source file via the sandboxed root
	src, err := root.Open(relPath)
	if err != nil {
		return "", false, false, 0, fmt.Errorf("opening source %s: %w", relPath, err)
	}

	//Create the temp file and ensure disk cleanup
	tempFile, err := os.CreateTemp("", "sample-*")
	if err != nil {
		src.Close()
		return "", false, false, 0, fmt.Errorf("creating temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath) // Automatically clean up from disk when function exits

	//Copy contents from original file to temp file
	_, copyErr := io.Copy(tempFile, src)
	src.Close()      // No longer need original file open
	tempFile.Close() // CLOSE TEMP HANDLE immediately so os.WriteFile can safely modify it

	if copyErr != nil {
		return "", false, false, 0, fmt.Errorf("copying to temp file: %w", copyErr)
	}

	//Safely accept all pending changes on disk
	if wl != nil {
		cl.Accept_all_changes(tempPath, wl.GetChanges(relPath))
	}

	//Open a fresh read-only handle to the cleaned temp file (starts at byte 0 automatically)
	cleanFile, err := os.Open(tempPath)
	if err != nil {
		return "", false, false, 0, fmt.Errorf("opening cleaned temp file: %w", err)
	}
	defer cleanFile.Close()

	//Scan the requested line range from the cleaned temp file
	scanner := bufio.NewScanner(cleanFile)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // Allow up to 10MB per line

	end := start + offset - 1
	var sb strings.Builder
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if lineNo > end {
			break
		}

		line := fmt.Sprintf("%d.%s\n", lineNo, scanner.Text())
		if sb.Len()+len(line) > DEFAULT_MAX_BYTES {
			truncated = true
			break
		}
		sb.WriteString(line)
	}

	if err := scanner.Err(); err != nil {
		return "", false, false, 0, fmt.Errorf("reading temp file %s: %w", tempPath, err)
	}

	if lineNo < start {
		return "", false, false, lineNo, nil
	}

	return sb.String(), truncated, true, lineNo, nil
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}

	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("read_file: invalid input: %w", err)
	}
	if input.Path == "" {
		return tools.Errf("path is required"), nil
	} else if input.Path == "." {
		return tools.Errf("path must point to a file"), nil
	}

	relPath := filepath.Clean(input.Path)
	if filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return tools.Errf("path must be relative to the project root, got %q", input.Path), nil
	}

	//check if the file exists first if not we need to tell the llm to create it
	//NOTE: this goes through args.Root rather than os.Stat directly, so a
	// ".." or symlink escape gets rejected by the sandbox instead of us
	// touching a file outside the project
	info, err := args.Root.Stat(relPath)
	if errors.Is(err, os.ErrNotExist) {
		return tools.Errf("file in path %v does not exist use the create_file tool to create it first", input.Path), nil
	}
	if err != nil {
		return tools.Errf("checking %v: %v", input.Path, err), nil
	}
	if info.IsDir() {
		return tools.Errf("path %v is a directory, not a file", input.Path), nil
	}
	if info.Size() == 0 {
		return tools.Ok("(file is empty)"), nil
	}

	start := input.Start
	if start < 1 {
		start = 1
	}

	offset := input.Offset
	if offset <= 0 {
		offset = DEFAULT_MAX_LINES
	}
	offset = min(offset, DEFAULT_MAX_LINES)

	content, truncated, ok, lastLine, err := readRange(args.Root, args.WatchList ,relPath, start, offset)
	if err != nil {
		return tools.Errf("reading %v: %v", input.Path, err), nil
	}
	if !ok {
		return tools.Errf("starting_line %d is past the end of the file (%d lines)", start, lastLine), nil
	}
	if truncated {
		content += fmt.Sprintf("\n...output truncated at %d bytes, raise starting_line to keep paging\n", DEFAULT_MAX_BYTES)
	}

	return tools.Ok(content), nil
}
