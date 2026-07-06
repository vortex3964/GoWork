package writefiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	writefiletool "GoWork/tools/WriteFileTool" )

func setupTool(t *testing.T) (*writefiletool.Tool, string) {
	t.Helper()
	dir := t.TempDir()
	tool, err := writefiletool.New(dir)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return tool, dir
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to seed test file: %v", err)
	}
	return path
}

func mustMarshal(t *testing.T, in writefiletool.Input) json.RawMessage {
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

func TestRun_SingleReplace_Success(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "hello.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "hello.go",
		Old:      `println("hi")`,
		New:      `println("bye")`,
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	got := readFile(t, filepath.Join(dir, "hello.go"))
	want := "package main\n\nfunc main() {\n\tprintln(\"bye\")\n}\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_OldStringNotFound_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "hello.go", "package main\n")

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "hello.go",
		Old:      "this text does not exist",
		New:      "replacement",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected this to be an expected/model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true when old_string is not found, got success: %s", result.Content)
	}
	if !strings.Contains(strings.ToLower(result.Content), "not found") {
		t.Errorf("expected error message to mention 'not found', got: %q", result.Content)
	}
}

func TestRun_MultipleMatchesWithoutReplaceAll_ReturnsToolError(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "dup.go", "foo\nfoo\nfoo\n")

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "dup.go",
		Old:      "foo",
		New:      "bar",
		// ReplaceAll intentionally left false
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("expected an expected/model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for ambiguous match count, got success: %s", result.Content)
	}

	// File should be untouched since the edit was rejected.
	got := readFile(t, filepath.Join(dir, "dup.go"))
	if got != "foo\nfoo\nfoo\n" {
		t.Errorf("file should not have been modified on rejected edit, got: %q", got)
	}
}

func TestRun_ReplaceAll_ReplacesEveryOccurrence(t *testing.T) {
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "dup.go", "foo\nfoo\nfoo\n")

	input := mustMarshal(t, writefiletool.Input{
		FilePath:   "dup.go",
		Old:        "foo",
		New:        "bar",
		ReplaceAll: true,
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success with replace_all, got tool error: %s", result.Content)
	}

	got := readFile(t, filepath.Join(dir, "dup.go"))
	want := "bar\nbar\nbar\n"
	if got != want {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_FileDoesNotExist_ReturnsToolError(t *testing.T) {
	tool, _ := setupTool(t)

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "does_not_exist.go",
		Old:      "foo",
		New:      "bar",
	})

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("missing file should be a model-visible failure, not a Go error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true for nonexistent file, got success: %s", result.Content)
	}
}

func TestRun_PathOutsideRoot_IsRejected(t *testing.T) {
	tool, dir := setupTool(t)

	// A sibling directory to the sandbox root, i.e. genuinely outside it.
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("top secret"), 0644); err != nil {
		t.Fatalf("failed to seed outside file: %v", err)
	}

	rel, err := filepath.Rel(dir, outsideFile)
	if err != nil {
		t.Fatalf("failed to compute relative path: %v", err)
	}

	input := mustMarshal(t, writefiletool.Input{
		FilePath: rel, // e.g. "../../<tmp>/secret.txt"
		Old:      "top secret",
		New:      "leaked",
	})

	// NOTE: os.Root rejecting a traversal typically surfaces as a Go `error`
	// from the underlying Open/OpenFile call (a PathError), not a model-level
	// ToolResult — this is arguably a code-level/security failure rather than
	// something the model should just "try again" on. Adjust this assertion
	// once you decide how Run translates that error.
	result, runErr := tool.Run(context.Background(), input)
	if runErr == nil && !result.IsError {
		t.Fatalf("expected path traversal to be rejected via error or IsError, got success")
	}

	// The outside file must remain untouched no matter what.
	got := readFile(t, outsideFile)
	if got != "top secret" {
		t.Errorf("sandbox escape: outside file was modified, got: %q", got)
	}
}

func TestRun_OldEqualsNew_NoOp(t *testing.T) {
	// TODO: decide the actual policy (success no-op vs IsError rejection)
	// and tighten this assertion once decided.
	tool, dir := setupTool(t)
	writeTestFile(t, dir, "same.go", "package main\n")

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "same.go",
		Old:      "package main\n",
		New:      "package main\n",
	})

	_, _ = tool.Run(context.Background(), input)
}

func TestInputSchema_HasRequiredFields(t *testing.T) {
	tool, _ := setupTool(t)
	schema := tool.InputSchema()

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected schema[\"required\"] to be []string, got %T", schema["required"])
	}

	want := map[string]bool{"file_path": true, "old_string": true, "new_string": true}
	for _, r := range required {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("schema is missing required fields: %v", want)
	}
}

