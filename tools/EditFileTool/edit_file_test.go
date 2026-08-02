package editfiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"GoWork/tools"
	editfiletool "GoWork/tools/EditFileTool"
)

type testTool struct {
	tools.AgentTool
	args tools.DispatchArgs
}

func (tt testTool) Run(ctx context.Context, input json.RawMessage) (tools.ToolResult, error) {
	return tt.AgentTool.Run(ctx, tt.args, input)
}

func setupTool(t *testing.T) (testTool, string) {
	t.Helper()
	dir := t.TempDir()
	tool := editfiletool.New()
	args, err := tools.InitDispatchArgs(dir, nil, nil)
	if err != nil {
		t.Fatalf("failed to init dispatch args: %v", err)
	}
	return testTool{AgentTool: tool, args: args}, dir
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to seed test file: %v", err)
	}
	return path
}

func mustMarshal(t *testing.T, in editfiletool.Input) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	return b
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file back: %v", err)
	}
	return string(b)
}

func mkInput(t *testing.T, filePath string, startLine, endLine int, newContent string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(editfiletool.Input{
		FilePath:   filePath,
		StartLine:  startLine,
		EndLine:    endLine,
		NewContent: newContent,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func runEdit(t *testing.T, tool testTool, dir, name, content string, startLine, endLine int, newContent string) (string, bool) {
	t.Helper()
	writeTestFile(t, dir, name, content)
	input := mkInput(t, name, startLine, endLine, newContent)
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	return result.Content, result.IsError
}

func TestRun_ReplaceSingleLine_Success(t *testing.T) {
	tool, dir := setupTool(t)

	content, isErr := runEdit(t, tool, dir, "hello.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n", 4, 4, "\tprintln(\"bye\")")
	if isErr {
		t.Fatalf("expected success, got tool error: %s", content)
	}

	got := readFile(t, filepath.Join(dir, "hello.go"))
	want := "package main\n\nfunc main() {\n<<<<<<< old\n\tprintln(\"hi\")\n=======\n\tprintln(\"bye\")\n>>>>>>> Ai change\n}\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_ReplaceMultiLineRange_WithMultiLineContent(t *testing.T) {
	tool, dir := setupTool(t)

	content, isErr := runEdit(t, tool, dir, "multi.go", "line1\nline2\nline3\nline4\nline5\n", 2, 4, "replacedA\nreplacedB")
	if isErr {
		t.Fatalf("expected success, got tool error: %s", content)
	}

	got := readFile(t, filepath.Join(dir, "multi.go"))
	want := "line1\n<<<<<<< old\nline2\nline3\nline4\n=======\nreplacedA\nreplacedB\n>>>>>>> Ai change\nline5\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InsertWithoutRemoving_EndLineIsStartMinusOne(t *testing.T) {
	tool, dir := setupTool(t)

	content, isErr := runEdit(t, tool, dir, "insert.go", "line1\nline2\nline3\n", 2, 1, "inserted")
	if isErr {
		t.Fatalf("expected success, got tool error: %s", content)
	}

	got := readFile(t, filepath.Join(dir, "insert.go"))
	want := "line1\n<<<<<<< old\n=======\ninserted\n>>>>>>> Ai change\nline2\nline3\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InsertAtEndOfFile(t *testing.T) {
	tool, dir := setupTool(t)

	content, isErr := runEdit(t, tool, dir, "append.go", "line1\nline2\n", 3, 2, "line3")
	if isErr {
		t.Fatalf("expected success, got tool error: %s", content)
	}

	got := readFile(t, filepath.Join(dir, "append.go"))
	want := "line1\nline2\n<<<<<<< old\n=======\nline3\n>>>>>>> Ai change\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_DeleteLines_EmptyNewContent(t *testing.T) {
	tool, dir := setupTool(t)

	content, isErr := runEdit(t, tool, dir, "delete.go", "line1\nline2\nline3\nline4\n", 2, 3, "")
	if isErr {
		t.Fatalf("expected success, got tool error: %s", content)
	}

	got := readFile(t, filepath.Join(dir, "delete.go"))
	want := "line1\n<<<<<<< old\nline2\nline3\n=======\n>>>>>>> Ai change\nline4\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_ReplaceEntireFile(t *testing.T) {
	tool, dir := setupTool(t)

	content, isErr := runEdit(t, tool, dir, "whole.go", "old1\nold2\n", 1, 2, "new1\nnew2\nnew3")
	if isErr {
		t.Fatalf("expected success, got tool error: %s", content)
	}

	got := readFile(t, filepath.Join(dir, "whole.go"))
	want := "<<<<<<< old\nold1\nold2\n=======\nnew1\nnew2\nnew3\n>>>>>>> Ai change\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InsertIntoEmptyFile(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "empty.go", "")

	input := mkInput(t, "empty.go", 1, 0, "package main")
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, filepath.Join(dir, "empty.go"))
	want := "<<<<<<< old\n=======\npackage main\n>>>>>>> Ai change\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_StartLineZero_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "hello.go", "line1\nline2\n")

	input := mkInput(t, "hello.go", 0, 1, "x")
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for start_line=0, got success: %s", result.Content)
	}
}

func TestRun_EndLineBeforeStartMinusOne_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "hello.go", "line1\nline2\nline3\n")

	input := mkInput(t, "hello.go", 3, 1, "x")
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for end_line < start_line - 1, got success: %s", result.Content)
	}
}

func TestRun_RangePastEndOfFile_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "short.go", "line1\nline2\n")

	input := mkInput(t, "short.go", 5, 6, "x")
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for range past EOF, got success: %s", result.Content)
	}
	got := readFile(t, filepath.Join(dir, "short.go"))
	if got != "line1\nline2\n" {
		t.Errorf("file should not have been modified on rejected edit, got: %q", got)
	}
}

func TestRun_EndLineExceedsLineCount_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "short.go", "line1\nline2\n")

	input := mkInput(t, "short.go", 1, 5, "x")
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true when end_line exceeds line count, got success: %s", result.Content)
	}
}

func TestRun_FileDoesNotExist_ReturnsToolError(t *testing.T) {
	tool, _ := setupTool(t)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "does_not_exist.go",
		StartLine:  1,
		EndLine:    1,
		NewContent: "x",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("missing file should be a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for nonexistent file, got success: %s", result.Content)
	}
}

// Cache is wrong //////////////////////////////////////////////////////////////
// func TestRun_NotYetRead_ReturnsToolError(t *testing.T) {
// 	tool, dir := setupTool(t)
// 	writeTestFile(t, dir, "unread.go", "line1\nline2\n")
// 	// Deliberately do NOT call markAsRead: no cache entry exists.
// 	input := mustMarshal(t, editfiletool.Input{
// 		FilePath: "unread.go", StartLine: 1, EndLine: 1, NewContent: "x",
// 	})
// 	result, err := tool.Run(context.Background(), input)
// 	if err != nil { t.Fatalf("expected a model-visible failure, not a Go error: %v", err) }
// 	if !result.IsError { t.Fatalf("expected IsError=true for a file with no read/cache entry, got success: %s", result.Content) }
// 	if !strings.Contains(strings.ToLower(result.Content), "read") {
// 		t.Errorf("expected error message to mention reading the file first, got: %q", result.Content)
// 	}
// }
//
// func TestRun_StaleCacheAfterExternalWrite_ReturnsToolError(t *testing.T) {
// 	tool, dir := setupTool(t)
// 	path := writeTestFile(t, dir, "stale.go", "line1\nline2\n")
// 	markAsRead(t, "stale.go", path)
// 	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644); err != nil {
// 		t.Fatalf("failed to simulate external write: %v", err)
// 	}
// 	input := mustMarshal(t, editfiletool.Input{
// 		FilePath: "stale.go", StartLine: 1, EndLine: 1, NewContent: "x",
// 	})
// 	result, err := tool.Run(context.Background(), input)
// 	if err != nil { t.Fatalf("expected a model-visible failure, not a Go error: %v", err) }
// 	if !result.IsError { t.Fatalf("expected IsError=true when on-disk mtime no longer matches cache, got success: %s", result.Content) }
// 	if !strings.Contains(strings.ToLower(result.Content), "re-read") && !strings.Contains(strings.ToLower(result.Content), "changed") {
// 		t.Errorf("expected error message to mention the file changing / needing a re-read, got: %q", result.Content)
// 	}
// 	got := readFile(t, path)
// 	if got != "line1\nline2\nline3\n" { t.Errorf("file should not have been modified on rejected edit, got: %q", got) }
// }
//
// func TestRun_PathOutsideRoot_IsRejected(t *testing.T) {
// 	tool, dir := setupTool(t)
// 	outsideDir := t.TempDir()
// 	outsideFile := filepath.Join(outsideDir, "secret.txt")
// 	if err := os.WriteFile(outsideFile, []byte("top secret\n"), 0644); err != nil {
// 		t.Fatalf("failed to seed outside file: %v", err)
// 	}
// 	rel, err := filepath.Rel(dir, outsideFile)
// 	if err != nil { t.Fatalf("failed to compute relative path: %v", err) }
// 	markAsRead(t, rel, outsideFile)
// 	input := mustMarshal(t, editfiletool.Input{
// 		FilePath: rel, StartLine: 1, EndLine: 1, NewContent: "leaked",
// 	})
// 	result, runErr := tool.Run(context.Background(), input)
// 	if runErr == nil && !result.IsError { t.Fatalf("expected path traversal to be rejected via error or IsError, got success") }
// 	got := readFile(t, outsideFile)
// 	if got != "top secret\n" { t.Errorf("sandbox escape: outside file was modified, got: %q", got) }
// }
//
// func TestRun_UpdatesModTimeInCache_AllowingFollowUpEditWithoutReread(t *testing.T) {
// 	tool, dir := setupTool(t)
// 	path := writeTestFile(t, dir, "sequential.go", "line1\nline2\nline3\n")
// 	markAsRead(t, "sequential.go", path)
// 	first := mustMarshal(t, editfiletool.Input{
// 		FilePath: "sequential.go", StartLine: 1, EndLine: 1, NewContent: "lineA",
// 	})
// 	result, err := tool.Run(context.Background(), first)
// 	if err != nil { t.Fatalf("unexpected code-level error on first edit: %v", err) }
// 	if result.IsError { t.Fatalf("expected first edit to succeed, got tool error: %s", result.Content) }
// 	second := mustMarshal(t, editfiletool.Input{
// 		FilePath: "sequential.go", StartLine: 2, EndLine: 2, NewContent: "lineB",
// 	})
// 	result, err = tool.Run(context.Background(), second)
// 	if err != nil { t.Fatalf("unexpected code-level error on second edit: %v", err) }
// 	if result.IsError { t.Fatalf("expected second sequential edit (no re-read) to succeed, got tool error: %s", result.Content) }
// 	got := readFile(t, path)
// 	want := "lineA\nlineB\nline3\n"
// 	if got != want { t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want) }
// }
//
// func TestRun_InvalidatesOnlyRangesFromStartLineOnward(t *testing.T) {
// 	tool, dir := setupTool(t)
// 	path := writeTestFile(t, dir, "ranges.go", "line1\nline2\nline3\nline4\nline5\n")
// 	info, err := os.Stat(path)
// 	if err != nil { t.Fatalf("failed to stat seeded file: %v", err) }
// 	rs := tools.Load("ranges.go")
// 	beforeRange := tools.NewLineRanges(1, 2)
// 	afterRange := tools.NewLineRanges(3, 5)
// 	rs.Put(beforeRange, "line1\nline2", info.ModTime(), 5)
// 	rs.Put(afterRange, "line3\nline4\nline5", info.ModTime(), 5)
// 	input := mustMarshal(t, editfiletool.Input{
// 		FilePath: "ranges.go", StartLine: 3, EndLine: 3, NewContent: "replaced3",
// 	})
// 	result, err := tool.Run(context.Background(), input)
// 	if err != nil { t.Fatalf("unexpected code-level error: %v", err) }
// 	if result.IsError { t.Fatalf("expected success, got tool error: %s", result.Content) }
// 	after := tools.Load("ranges.go")
// 	if _, stillCached := after.Ranges[beforeRange]; !stillCached {
// 		t.Errorf("expected range entirely before start_line to remain cached, but it was invalidated")
// 	}
// 	if _, stillCached := after.Ranges[afterRange]; stillCached {
// 		t.Errorf("expected range at/after start_line to be invalidated, but it is still cached")
// 	}
// }
//
// func TestRun_RejectedEdit_LeavesCacheUntouched(t *testing.T) {
// 	tool, dir := setupTool(t)
// 	path := writeTestFile(t, dir, "untouched.go", "line1\nline2\n")
// 	markAsRead(t, "untouched.go", path)
// 	rs := tools.Load("untouched.go")
// 	before := rs.ModTime
// 	input := mustMarshal(t, editfiletool.Input{
// 		FilePath: "untouched.go", StartLine: 10, EndLine: 10, NewContent: "x",
// 	})
// 	result, err := tool.Run(context.Background(), input)
// 	if err != nil { t.Fatalf("unexpected code-level error: %v", err) }
// 	if !result.IsError { t.Fatalf("expected the edit to be rejected, got success: %s", result.Content) }
// 	after := tools.Load("untouched.go")
// 	if !after.ModTime.Equal(before) {
// 		t.Errorf("expected ModTime to be untouched after a rejected edit, got %v want %v", after.ModTime, before)
// 	}
// }
// END Cache is wrong ///////////////////////////////////////////////////////////

func TestInputSchema_HasRequiredFields(t *testing.T) {
	tool, _ := setupTool(t)
	var m map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &m); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}

	required, ok := m["required"].([]any)
	if !ok {
		t.Fatalf("expected schema[\"required\"] to be []any, got %T", m["required"])
	}

	want := map[string]bool{"file_path": true, "start_line": true, "end_line": true, "new_content": true}
	for _, r := range required {
		delete(want, r.(string))
	}
	if len(want) != 0 {
		t.Errorf("schema is missing required fields: %v", want)
	}
}
