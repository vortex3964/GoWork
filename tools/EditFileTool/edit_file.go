//DESC: used to make surgical, line-precise edits to an already existing file
// prefer this over write_file when you know the exact line numbers to change
// and prefer the grep_file tool first before using this one

package editfiletool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"GoWork/tools"
)

type Input struct {
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	NewContent string `json:"new_content"`
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "edit_file" }

func (t *Tool) Kind() tools.Kind { return tools.KindWrite }

func (t *Tool) Description() string {
	return `Replaces a specific range of lines in an existing file with new content, addressed by line number.

Before using this tool:
1. The file must already exist and must have been read (with a read tool) since its last on-disk change. If the file was modified since your last read (by you, the user, or anything else), you must re-read it before editing it.
2. start_line and end_line are 1-indexed and inclusive, and describe the range of existing lines being replaced.
3. new_content is the full replacement text for that range. It may contain zero, one, or many lines. To insert text without removing anything, use an empty range (end_line = start_line - 1) at the insertion point. To delete lines, pass an empty new_content.
This tool operates on line numbers, not text matching it does not verify that the lines being replaced contain any particular content, so incorrect line numbers will silently edit the wrong lines.`
}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit, relative to the project root.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "1-indexed line number where the replacement range starts (inclusive).",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "1-indexed line number where the replacement range ends (inclusive). Must be >= start_line - 1. Use end_line = start_line - 1 to insert new lines without replacing any existing ones.",
			},
			"new_content": map[string]any{
				"type":        "string",
				"description": "The text to place at the given line range. Can be empty (to delete the range), a single line, or multiple lines.",
			},
		},
		"required": []string{"file_path", "start_line", "end_line", "new_content"},
	}
}

func splitLines(contents []byte) []string {
	if len(contents) == 0 {
		return nil
	}
	trimmed := contents
	hadTrailingNewline := bytes.HasSuffix(trimmed, []byte("\n"))
	if hadTrailingNewline {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		// contents was just "\n": one empty line.
		return []string{""}
	}
	parts := bytes.Split(trimmed, []byte("\n"))
	lines := make([]string, len(parts))
	for i, p := range parts {
		lines[i] = string(p)
	}
	return lines
}

func joinLines(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(joinWithNewline(lines) + "\n")
}

func joinWithNewline(lines []string) string {
	out := lines[0]
	for _, l := range lines[1:] {
		out += "\n" + l
	}
	return out
}

func replaceLineRange(contents []byte, startLine, endLine int, newContent string) ([]byte, int) {
	lines := splitLines(contents)

	var replacement []string
	if newContent == "" {
		replacement = nil // deleting the range / inserting nothing
	} else {
		replacement = strings.Split(newContent, "\n")
	}

	before := lines[:startLine-1]
	var after []string
	if endLine >= startLine {
		after = lines[endLine:]
	} else {
		// insertion point, nothing removed
		after = lines[startLine-1:]
	}

	result := make([]string, 0, len(before)+len(replacement)+len(after))
	result = append(result, before...)
	result = append(result, replacement...)
	result = append(result, after...)

	return joinLines(result), len(result)
}

func validateRange(startLine, endLine, lineCount int) error {
	if startLine < 1 {
		return fmt.Errorf("start_line must be >= 1, got %d", startLine)
	}
	if endLine < startLine-1 {
		return fmt.Errorf("end_line (%d) must be >= start_line - 1 (%d)", endLine, startLine-1)
	}
	if startLine > lineCount+1 {
		return fmt.Errorf("start_line %d is past the end of the file, which has %d lines", startLine, lineCount)
	}
	if endLine > lineCount {
		return fmt.Errorf("end_line %d is past the end of the file, which has %d lines", endLine, lineCount)
	}
	return nil
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: invalid input: %w", err)
	}
	if input.FilePath == "" {
		return tools.Errf("file_path is required"), nil
	}
	if input.FilePath == "." {
		return tools.Errf("file_path must point to a file, not the project root"), nil
	}

	// Open and stat the existing file through the sandboxed root.
	f, err := args.Root.Open(input.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.Errf("file %s does not exist, create it first using the create_file tool", input.FilePath), nil
		}
		return tools.ToolResult{}, fmt.Errorf("edit_file: opening %s: %w", input.FilePath, err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return tools.ToolResult{}, fmt.Errorf("edit_file: stat %s: %w", input.FilePath, err)
	}
	if info.IsDir() {
		f.Close()
		return tools.Errf("%s is a directory, not a file", input.FilePath), nil
	}

	// This tool trusts line numbers instead of matching text, so it's only
	// safe to use against a file we know the current shape of. Require a
	// cache entry whose ModTime still matches the file on disk that means
	// it was read (or previously edited through us) since the last change.
	rs, cached := tools.Cache[input.FilePath]
	if !cached {
		f.Close()
		return tools.Errf("file %s has not been read yet; read it first so line numbers can be verified before editing", input.FilePath), nil
	}
	if !info.ModTime().Equal(rs.ModTime) {
		f.Close()
		return tools.Errf("file %s has changed on disk since it was last read; re-read it before editing so line numbers are accurate", input.FilePath), nil
	}

	contents, err := io.ReadAll(f)
	f.Close() // close the read handle before reopening for write
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: reading %s: %w", input.FilePath, err)
	}

	lineCount := len(splitLines(contents))
	if err := validateRange(input.StartLine, input.EndLine, lineCount); err != nil {
		return tools.Errf("%s", err.Error()), nil
	}

	updated, _ := replaceLineRange(contents, input.StartLine, input.EndLine, input.NewContent)

	// Reopen (truncating) through the sandboxed root and write the new contents.
	wf, err := args.Root.OpenFile(input.FilePath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: opening %s for write: %w", input.FilePath, err)
	}
	defer wf.Close()

	if _, err := wf.Write(updated); err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: writing %s: %w", input.FilePath, err)
	}

	// Unlike write_file, this is a controlled, line-precise change: only
	// cached ranges at or after the edit point are stale (their line numbers
	// may have shifted). Ranges entirely before start_line are still valid.
	rs.InvalidateFrom(input.StartLine)

	// Refresh ModTime to the post-write mtime (through the sandboxed root,
	// same as every other access) so a follow-up edit_file call against the
	// same file, without an intervening read, is still allowed.
	if newInfo, statErr := args.Root.Stat(input.FilePath); statErr == nil {
		rs.ModTime = newInfo.ModTime()
	}

	return tools.Ok(fmt.Sprintf("successfully edited lines %d-%d in %s", input.StartLine, input.EndLine, input.FilePath)), nil
}
