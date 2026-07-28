package readfiletool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

Returns the requested lines with 1-indexed line numbers prefixed, so you can reference exact locations in later edits. Large files are not returned in full use starting_line and offset_lines to page through them.
If you already read this exact range and the file hasn't changed since, this returns a short notice telling you that instead of repeating the content reuse what you already have in context.
If the file has changed since you last read it, the returned content reflects the current state on disk so use later code blocks for edits as they are more reliable to not be stale`
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

// readAllLines opens relPath through the sandboxed project root and scans
// every line into memory, along with the file's ModTime so the caller can
// populate the read-state cache after a real disk read.
func readAllLines(root *os.Root, relPath string) ([]string, os.FileInfo, error) {
	f, err := root.Open(relPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", relPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", relPath, err)
	}

	scanner := bufio.NewScanner(f)
	// default max token (line) size is 64KB; bump it so long lines
	// (minified JS, long JSON, etc.) don't trip bufio.ErrTooLong
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // allow up to 10MB per line

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", relPath, err)
	}

	return lines, info, nil
}

// formatRange renders the 1-indexed, inclusive line range
// [start, start+offset-1] with "N." prefixes, stopping early if the
// output would exceed DEFAULT_MAX_BYTES. ok is false when start falls
// past the end of the file.
func formatRange(lines []string, start, offset int) (content string, truncated bool, ok bool) {
	total := len(lines)
	if start > total {
		return "", false, false
	}

	end := start + offset - 1
	if end > total {
		end = total
	}

	var sb strings.Builder
	for i := start; i <= end; i++ {
		line := fmt.Sprintf("%d.%s\n", i, lines[i-1])
		if sb.Len()+len(line) > DEFAULT_MAX_BYTES {
			truncated = true
			break
		}
		sb.WriteString(line)
	}
	return sb.String(), truncated, true
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

	start := input.Start
	if start < 1 {
		start = 1
	}

	offset := input.Offset
	if offset <= 0 {
		offset = DEFAULT_MAX_LINES
	}
	offset = min(offset, DEFAULT_MAX_LINES)

	
	//NOTE: for future there is the non zero posibility that when we read since context window is finite
	//even if in read state we have markded that the llm read one range maybe the conversation was long
	//and the file was not toutched in a while so when we see that the context window is
	//almost full or a percent full then maybe we should allow the read to pass investigate further when we start dealling properly with the context window

	lines, _, err := readAllLines(args.Root, relPath)
	if err != nil {
		return tools.Errf("reading %v: %v", input.Path, err), nil
	}

	if len(lines) == 0 {
		return tools.Ok("(file is empty)"), nil
	}

	content, truncated, ok := formatRange(lines, start, offset)
	if !ok {
		return tools.Errf("starting_line %d is past the end of the file (%d lines)", start, len(lines)), nil
	}
	if truncated {
		content += fmt.Sprintf("\n...output truncated at %d bytes, raise starting_line to keep paging\n", DEFAULT_MAX_BYTES)
	}

	return tools.Ok(content), nil
}
