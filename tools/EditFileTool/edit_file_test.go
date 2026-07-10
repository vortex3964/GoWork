package editfiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	args, err := tools.InitDispatchArgs(dir)
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

func markAsRead(t *testing.T, relKey, absPath string) {
	t.Helper()
	info, err := os.Stat(absPath)
	if err != nil {
		t.Fatalf("failed to stat seeded file %s: %v", absPath, err)
	}
	rs := tools.Load(relKey)
	rs.Put(tools.NewLineRanges(1, -1), "", info.ModTime(), 0)
}

func TestRun_ReplaceSingleLine_Success(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "hello.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	markAsRead(t, "hello.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "hello.go",
		StartLine:  4,
		EndLine:    4,
		NewContent: "\tprintln(\"bye\")",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "package main\n\nfunc main() {\n\tprintln(\"bye\")\n}\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_ReplaceMultiLineRange_WithMultiLineContent(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "multi.go", "line1\nline2\nline3\nline4\nline5\n")
	markAsRead(t, "multi.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "multi.go",
		StartLine:  2,
		EndLine:    4,
		NewContent: "replacedA\nreplacedB",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "line1\nreplacedA\nreplacedB\nline5\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InsertWithoutRemoving_EndLineIsStartMinusOne(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "insert.go", "line1\nline2\nline3\n")
	markAsRead(t, "insert.go", path)

	// Insert a new line between line1 and line2, removing nothing.
	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "insert.go",
		StartLine:  2,
		EndLine:    1,
		NewContent: "inserted",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "line1\ninserted\nline2\nline3\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InsertAtEndOfFile(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "append.go", "line1\nline2\n")
	markAsRead(t, "append.go", path)

	// File has 2 lines; append after the last one.
	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "append.go",
		StartLine:  3,
		EndLine:    2,
		NewContent: "line3",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "line1\nline2\nline3\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_DeleteLines_EmptyNewContent(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "delete.go", "line1\nline2\nline3\nline4\n")
	markAsRead(t, "delete.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "delete.go",
		StartLine:  2,
		EndLine:    3,
		NewContent: "",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "line1\nline4\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_ReplaceEntireFile(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "whole.go", "old1\nold2\n")
	markAsRead(t, "whole.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "whole.go",
		StartLine:  1,
		EndLine:    2,
		NewContent: "new1\nnew2\nnew3",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "new1\nnew2\nnew3\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InsertIntoEmptyFile(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "empty.go", "")
	markAsRead(t, "empty.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "empty.go",
		StartLine:  1,
		EndLine:    0,
		NewContent: "package main",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "package main\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_StartLineZero_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "hello.go", "line1\nline2\n")
	markAsRead(t, "hello.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "hello.go",
		StartLine:  0,
		EndLine:    1,
		NewContent: "x",
	})

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
	path := writeTestFile(t, dir, "hello.go", "line1\nline2\nline3\n")
	markAsRead(t, "hello.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "hello.go",
		StartLine:  3,
		EndLine:    1, // invalid: less than start_line - 1 (2)
		NewContent: "x",
	})

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
	path := writeTestFile(t, dir, "short.go", "line1\nline2\n")
	markAsRead(t, "short.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "short.go",
		StartLine:  5,
		EndLine:    6,
		NewContent: "x",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for range past EOF, got success: %s", result.Content)
	}

	// File should be untouched.
	got := readFile(t, path)
	if got != "line1\nline2\n" {
		t.Errorf("file should not have been modified on rejected edit, got: %q", got)
	}
}

func TestRun_EndLineExceedsLineCount_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "short.go", "line1\nline2\n")
	markAsRead(t, "short.go", path)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "short.go",
		StartLine:  1,
		EndLine:    5, // file only has 2 lines
		NewContent: "x",
	})

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

func TestRun_NotYetRead_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "unread.go", "line1\nline2\n")
	// Deliberately do NOT call markAsRead: no cache entry exists.

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "unread.go",
		StartLine:  1,
		EndLine:    1,
		NewContent: "x",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for a file with no read/cache entry, got success: %s", result.Content)
	}
	if !strings.Contains(strings.ToLower(result.Content), "read") {
		t.Errorf("expected error message to mention reading the file first, got: %q", result.Content)
	}
}

func TestRun_StaleCacheAfterExternalWrite_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "stale.go", "line1\nline2\n")
	markAsRead(t, "stale.go", path)

	// Simulate an external modification (e.g. the user editing the file,
	// or write_file touching it) after our "read" was cached.
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatalf("failed to simulate external write: %v", err)
	}

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "stale.go",
		StartLine:  1,
		EndLine:    1,
		NewContent: "x",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true when on-disk mtime no longer matches cache, got success: %s", result.Content)
	}
	if !strings.Contains(strings.ToLower(result.Content), "re-read") && !strings.Contains(strings.ToLower(result.Content), "changed") {
		t.Errorf("expected error message to mention the file changing / needing a re-read, got: %q", result.Content)
	}

	// File should be untouched by the rejected edit.
	got := readFile(t, path)
	if got != "line1\nline2\nline3\n" {
		t.Errorf("file should not have been modified on rejected edit, got: %q", got)
	}
}

func TestRun_PathOutsideRoot_IsRejected(t *testing.T) {
	tool, dir := setupTool(t)

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("top secret\n"), 0644); err != nil {
		t.Fatalf("failed to seed outside file: %v", err)
	}

	rel, err := filepath.Rel(dir, outsideFile)
	if err != nil {
		t.Fatalf("failed to compute relative path: %v", err)
	}
	markAsRead(t, rel, outsideFile)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   rel, // e.g. "../../<tmp>/secret.txt"
		StartLine:  1,
		EndLine:    1,
		NewContent: "leaked",
	})

	result, runErr := tool.Run(context.Background(), input)
	if runErr == nil && !result.IsError {
		t.Fatalf("expected path traversal to be rejected via error or IsError, got success")
	}

	got := readFile(t, outsideFile)
	if got != "top secret\n" {
		t.Errorf("sandbox escape: outside file was modified, got: %q", got)
	}
}

func TestRun_UpdatesModTimeInCache_AllowingFollowUpEditWithoutReread(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "sequential.go", "line1\nline2\nline3\n")
	markAsRead(t, "sequential.go", path)

	first := mustMarshal(t, editfiletool.Input{
		FilePath:   "sequential.go",
		StartLine:  1,
		EndLine:    1,
		NewContent: "lineA",
	})
	result, err := tool.Run(context.Background(), first)
	if err != nil {
		t.Fatalf("unexpected code-level error on first edit: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected first edit to succeed, got tool error: %s", result.Content)
	}

	// A second edit_file call against the same file, with no read tool
	// call in between, should still succeed because Run refreshed ModTime.
	second := mustMarshal(t, editfiletool.Input{
		FilePath:   "sequential.go",
		StartLine:  2,
		EndLine:    2,
		NewContent: "lineB",
	})
	result, err = tool.Run(context.Background(), second)
	if err != nil {
		t.Fatalf("unexpected code-level error on second edit: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected second sequential edit (no re-read) to succeed, got tool error: %s", result.Content)
	}

	got := readFile(t, path)
	want := "lineA\nlineB\nline3\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_InvalidatesOnlyRangesFromStartLineOnward(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "ranges.go", "line1\nline2\nline3\nline4\nline5\n")

	// Seed the cache with two distinct ranges: one entirely before the edit
	// point, one at/after it.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat seeded file: %v", err)
	}
	rs := tools.Load("ranges.go")
	beforeRange := tools.NewLineRanges(1, 2)
	afterRange := tools.NewLineRanges(3, 5)
	rs.Put(beforeRange, "line1\nline2", info.ModTime(), 5)
	rs.Put(afterRange, "line3\nline4\nline5", info.ModTime(), 5)

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "ranges.go",
		StartLine:  3,
		EndLine:    3,
		NewContent: "replaced3",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	after := tools.Load("ranges.go")
	if _, stillCached := after.Ranges[beforeRange]; !stillCached {
		t.Errorf("expected range entirely before start_line to remain cached, but it was invalidated")
	}
	if _, stillCached := after.Ranges[afterRange]; stillCached {
		t.Errorf("expected range at/after start_line to be invalidated, but it is still cached")
	}
}

func TestRun_RejectedEdit_LeavesCacheUntouched(t *testing.T) {
	tool, dir := setupTool(t)
	path := writeTestFile(t, dir, "untouched.go", "line1\nline2\n")
	markAsRead(t, "untouched.go", path)

	rs := tools.Load("untouched.go")
	before := rs.ModTime

	input := mustMarshal(t, editfiletool.Input{
		FilePath:   "untouched.go",
		StartLine:  10, // out of range, will be rejected
		EndLine:    10,
		NewContent: "x",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected the edit to be rejected, got success: %s", result.Content)
	}

	after := tools.Load("untouched.go")
	if !after.ModTime.Equal(before) {
		t.Errorf("expected ModTime to be untouched after a rejected edit, got %v want %v", after.ModTime, before)
	}
}

func TestInputSchema_HasRequiredFields(t *testing.T) {
	tool, _ := setupTool(t)
	schema := tool.InputSchema()

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected schema[\"required\"] to be []string, got %T", schema["required"])
	}

	want := map[string]bool{"file_path": true, "start_line": true, "end_line": true, "new_content": true}
	for _, r := range required {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("schema is missing required fields: %v", want)
	}
}
