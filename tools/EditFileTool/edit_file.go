//DESC: used to make surgical, line-precise edits to an already existing file
// prefer this over write_file when you know the exact line numbers to change
// and prefer the grep_search tool first before using this one

package editfiletool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/tools"
)

type Input struct {
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	NewContent string `json:"new_content"`
}

type Tool struct{}

// NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "edit_file" }

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

func (t *Tool) Description() string {
	return `Replaces a specific range of lines in an existing file with new content, addressed by line number.

Before using this tool:
1. The file must already exist. Read it first so your line numbers match its current contents. If this file has a recorded read or write and has changed on disk since then, the edit is rejected until you re-read it with read_file (same stale-read rule as write_file).
2. start_line and end_line are 1-indexed and inclusive, and describe the range of existing lines being replaced.
3. new_content is the full replacement text for that range. It may contain zero, one, or many lines. A single trailing newline is treated as the normal line ending and does not add an extra blank line; additional trailing newlines are preserved as blank lines. To insert text without removing anything, use an empty range (end_line = start_line - 1) at the insertion point. To delete lines, pass an empty new_content.
This tool operates on line numbers, not text matching it does not verify that the lines being replaced contain any particular content, so incorrect line numbers will silently edit the wrong lines.`
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
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
		"required":             []string{"file_path", "start_line", "end_line", "new_content"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

// detectNewline reports the line ending style a file already uses. A single
// CRLF pair is enough to treat the file as CRLF: rewriting it with LF-only
// replacements is what produced the mixed-ending files this guards against.
func detectNewline(contents []byte) string {
	if bytes.Contains(contents, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// splitLines splits contents into lines, treating a single trailing
// newline as the terminator of the last line. CRLF terminators are stripped
// from line contents when the file uses them, so callers can join the lines
// back with the detected style without doubling the carriage return.
func splitLines(contents []byte, newline string) []string {
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
		if newline == "\r\n" {
			p = bytes.TrimSuffix(p, []byte("\r"))
		}
		lines[i] = string(p)
	}
	return lines
}

// joinLines re-joins lines into file bytes using newline between lines,
// adding exactly one trailing newline (or nil for zero lines).
func joinLines(lines []string, newline string) []byte {
	if len(lines) == 0 {
		return nil
	}
	if newline == "" {
		newline = "\n"
	}
	// len(lines)-1 separators plus a final terminator, plus the lines
	// themselves, avoids the string-builder growing past its estimate for
	// typical text.
	n := len(lines) * len(newline)
	for _, l := range lines {
		n += len(l)
	}

	var b strings.Builder
	b.Grow(n)
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString(newline)
		b.WriteString(l)
	}
	b.WriteString(newline)
	return []byte(b.String())
}

// replacementLines converts new_content into the lines that replace a range.
// A single trailing newline is the normal line terminator, not an extra empty
// line, so strip exactly one before splitting. Additional trailing newlines
// are intentional blank lines and are preserved.
func replacementLines(newContent string) []string {
	if newContent == "" {
		return nil // deleting the range / inserting nothing
	}
	// new_content is model-authored and may use either style regardless of
	// the target file; normalise it to LF here, then join with the file's
	// own style.
	newContent = strings.ReplaceAll(newContent, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(newContent, "\n"), "\n")
}

// lineStartOffset returns the byte offset where the 1-indexed line starts.
// A line past the end of the data maps to len(data).
func lineStartOffset(data []byte, line int) int {
	if line <= 1 {
		return 0
	}
	current := 1
	for i, b := range data {
		if b == '\n' {
			current++
			if current == line {
				return i + 1
			}
		}
	}
	return len(data)
}

// lineRangeOffsets returns the byte range [start, end) that the 1-indexed
// inclusive line range [startLine, endLine] covers. For an insertion
// (endLine < startLine) start == end at the insertion point.
func lineRangeOffsets(data []byte, startLine, endLine int) (start, end int) {
	start = lineStartOffset(data, startLine)
	if endLine < startLine {
		return start, start
	}
	return start, lineStartOffset(data, endLine+1)
}

// replaceLineRange replaces the 1-indexed inclusive line range
// [startLine, endLine] with newContent's lines.
func replaceLineRange(lines []string, startLine, endLine int, newContent, newline string) ([]byte, int) {
	replacement := replacementLines(newContent)

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

	return joinLines(result, newline), len(result)
}

// validateRange checks that [startLine, endLine] is a sane, in-bounds
// range (or insertion point) for a file with lineCount lines.
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
			return tools.Errf("file %s does not exist - the path is wrong or the file was never created. Do NOT create it unless the task explicitly asks for a new file: locate the real path with list_directory or file_search.", input.FilePath), nil
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

	src, err := io.ReadAll(f)
	f.Close() // close the read handle before reopening for write
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: reading %s: %w", input.FilePath, err)
	}

	// Refuse to edit a file that changed on disk since the agent last saw
	// it. A stale line-number edit would silently hit the wrong lines.
	if args.ReadState != nil {
		key := args.PathKey(input.FilePath)
		if seen, ok := args.ReadState.SeenAt(key); ok && info.ModTime().After(seen) {
			return tools.Errf("file %s was modified after it was last read - re-read it with read_file to get the current content before editing", input.FilePath), nil
		}
	}

	var changes []cl.Change
	accepted := src
	if args.WatchList != nil {
		changes = args.WatchList.GetChanges(input.FilePath)
		accepted = cl.AcceptAllChangesBytes(src, changes)
	}

	// Keep the file's existing line-ending style. Without this, a CRLF file
	// ends up with LF-only replacement lines (and CRLF leftovers around them)
	// after every edit.
	newline := detectNewline(accepted)
	acceptedLines := splitLines(accepted, newline)
	if err := validateRange(input.StartLine, input.EndLine, len(acceptedLines)); err != nil {
		return tools.Errf("%s", err.Error()), nil
	}

	// The edit replaces a known byte range, so turn it directly into one
	// marker hunk instead of diffing the whole file. Existing pending
	// changes are re-emitted from their stored offsets by the helper.
	var merged []byte
	if len(accepted) > 0 && accepted[len(accepted)-1] != '\n' {
		// This tool always terminates a rewritten file with a newline, which
		// an offset-only replacement cannot express. Keep the old whole-file
		// path for this rare case so the output stays byte-identical.
		updated, _ := replaceLineRange(acceptedLines, input.StartLine, input.EndLine, input.NewContent, newline)
		merged = cl.MergeMarkerBytes(cl.RejectAllChangesBytes(src, changes), updated)
	} else {
		start, end := lineRangeOffsets(accepted, input.StartLine, input.EndLine)
		newText := joinLines(replacementLines(input.NewContent), newline)
		merged = cl.MergeMarkerEdits(src, accepted, changes, []cl.Edit{{
			Start: start,
			End:   end,
			New:   newText,
		}})
	}

	// Reopen (truncating) through the sandboxed root and write the new contents.
	wf, err := args.Root.OpenFile(input.FilePath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: opening %s for write: %w", input.FilePath, err)
	}

	if args.WatchList != nil {
		args.WatchList.Add(input.FilePath)
	}

	if _, err := wf.Write(merged); err != nil {
		wf.Close()
		return tools.ToolResult{}, fmt.Errorf("edit_file: writing %s: %w", input.FilePath, err)
	}
	if err := wf.Close(); err != nil {
		return tools.ToolResult{}, fmt.Errorf("edit_file: closing %s: %w", input.FilePath, err)
	}

	// Record the fresh mtime so an immediate follow-up edit in the same turn
	// isn't mistaken for an external change. Mirrors write_file.
	if args.ReadState != nil {
		if fresh, err := args.Root.Stat(input.FilePath); err == nil {
			args.ReadState.Record(args.PathKey(input.FilePath), fresh.ModTime())
		}
	}

	return tools.Ok(fmt.Sprintf("successfully edited lines %d-%d in %s", input.StartLine, input.EndLine, input.FilePath)), nil
}
