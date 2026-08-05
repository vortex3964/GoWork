package filesearchtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	filesearchtool "GoWork/tools/FileSearchTool"
	"GoWork/tools"
)

func requireRG(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not found on PATH, skipping")
	}
}

func newDispatchArgs(t *testing.T, dir string) tools.DispatchArgs {
	t.Helper()
	args, err := tools.InitDispatchArgs(dir, nil, nil)
	if err != nil {
		t.Fatalf("InitDispatchArgs: %v", err)
	}
	t.Cleanup(func() { _ = args.Root.Close() })
	return args
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func run(t *testing.T, tool tools.AgentTool, args tools.DispatchArgs, input filesearchtool.Input) tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Run(context.Background(), args, raw)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return res
}

func TestInterfaceConformance(t *testing.T) {
	tool := filesearchtool.New()

	if got := tool.Name(); got != "file_search" {
		t.Errorf("Name() = %q, want %q", got, "file_search")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if tool.Kind() != tools.KindSearch {
		t.Errorf("Kind() = %v, want %v", tool.Kind(), tools.KindSearch)
	}
	schema := tool.InputSchema()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}
	if m["type"] != "object" {
		t.Errorf(`InputSchema()["type"] = %v, want "object"`, m["type"])
	}
	required, ok := m["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "pattern" {
		t.Errorf(`InputSchema()["required"] = %v, want ["pattern"]`, m["required"])
	}
}

func TestMissingPattern(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: ""})
	if !res.IsError {
		t.Fatalf("expected IsError for empty pattern, got: %+v", res)
	}
}

func TestAbsolutePathRejected(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "foo", Path: "/etc/passwd"})
	if !res.IsError {
		t.Fatalf("expected IsError for absolute path, got: %+v", res)
	}
}

func TestParentTraversalRejected(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "foo", Path: "../outside"})
	if !res.IsError {
		t.Fatalf("expected IsError for '..' path, got: %+v", res)
	}
}

func TestNonexistentPath(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "foo", Path: "does/not/exist.go"})
	if !res.IsError {
		t.Fatalf("expected IsError for nonexistent path, got: %+v", res)
	}
	if !strings.Contains(res.Content, "does not exist") {
		t.Errorf("expected 'does not exist' in error message, got: %q", res.Content)
	}
}

func TestFindFileByName(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "Tui/todoList.go", "package tui\n")
	writeFile(t, dir, "other.go", "package other\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	// Pattern matches against the whole relative path, so a bare name
	// match like this returns the file's location.
	res := run(t, tool, args, filesearchtool.Input{Pattern: "todoList"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Tui/todoList.go") {
		t.Errorf("expected Tui/todoList.go in results, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "other.go") {
		t.Errorf("did not expect other.go in results, got:\n%s", res.Content)
	}
}

func TestNoMatches(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "nonexistentName12345"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No files found whose path matches") {
		t.Errorf("expected no-match message, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "list_directory") {
		t.Errorf("expected hint to use list_directory, got: %q", res.Content)
	}
}

func TestIgnoreCase(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "TodoList.go", "package tui\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	// Without ignore_case, lowercase "todolist" shouldn't match "TodoList".
	res := run(t, tool, args, filesearchtool.Input{Pattern: "todolist"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No files found") {
		t.Errorf("expected no case-sensitive match, got:\n%s", res.Content)
	}

	res = run(t, tool, args, filesearchtool.Input{Pattern: "todolist", IgnoreCase: true})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "TodoList.go") {
		t.Errorf("expected case-insensitive match, got:\n%s", res.Content)
	}
}

func TestLiteralMode(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "weird[file].txt", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	// "[" is a regex metacharacter; as a regex the pattern is a bracket
	// expression that doesn't match, as a literal it matches exactly.
	res := run(t, tool, args, filesearchtool.Input{Pattern: "weird[file]"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No files found") {
		t.Errorf("expected regex to miss bracket name, got:\n%s", res.Content)
	}

	res = run(t, tool, args, filesearchtool.Input{Pattern: "weird[file]", Literal: true})
	if res.IsError {
		t.Fatalf("unexpected error for literal search: %s", res.Content)
	}
	if !strings.Contains(res.Content, "weird[file].txt") {
		t.Errorf("expected literal path match, got:\n%s", res.Content)
	}
}

func TestRegexMode(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "x\n")
	writeFile(t, dir, "main.py", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: `\.go$`})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go") {
		t.Errorf("expected main.go to match \\.go$, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "main.py") {
		t.Errorf("did not expect main.py to match \\.go$, got:\n%s", res.Content)
	}
}

func TestInvalidRegexPattern(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "("})
	if !res.IsError {
		t.Fatalf("expected IsError for invalid regex, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "invalid pattern") {
		t.Errorf("expected 'invalid pattern' in message, got: %q", res.Content)
	}
}

func TestIncludeGlobFilter(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "x\n")
	writeFile(t, dir, "main.txt", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "main", Include: "*.go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go") {
		t.Errorf("expected main.go in results, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "main.txt") {
		t.Errorf("did not expect main.txt in results, got:\n%s", res.Content)
	}
}

func TestSubdirectoryRelativePaths(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "pkg/sub/file.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "file"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	// Path should be root-relative and forward-slashed, not absolute.
	if !strings.Contains(res.Content, "pkg/sub/file.go") {
		t.Errorf("expected forward-slashed relative path, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, dir) {
		t.Errorf("did not expect absolute path in output, got:\n%s", res.Content)
	}
}

func TestSearchScopedToPath(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "pkg/a.go", "x\n")
	writeFile(t, dir, "other/b.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "go", Path: "pkg"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "pkg/a.go") {
		t.Errorf("expected match in scoped dir, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "other/b.go") {
		t.Errorf("did not expect match outside scoped dir, got:\n%s", res.Content)
	}
}

func TestDirectoryNotListed(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	// An empty directory is not a file, so file_search must not report it.
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, dir, "real.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "empty"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No files found") {
		t.Errorf("did not expect empty directory to be listed, got:\n%s", res.Content)
	}
}

func TestMatchLimit(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	for i := 1; i <= 5; i++ {
		writeFile(t, dir, "a"+string(rune('0'+i))+".txt", "x\n")
	}
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "a", Limit: 2})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if got := strings.Count(res.Content, ".txt"); got != 2 {
		t.Errorf("expected 2 paths under limit=2, got %d in:\n%s", got, res.Content)
	}
	if !strings.Contains(res.Content, "matches limit reached") {
		t.Errorf("expected limit-reached notice, got:\n%s", res.Content)
	}
}

func TestDefaultLimitApplied(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	for i := 1; i <= 150; i++ {
		writeFile(t, dir, fmt.Sprintf("a%03d.txt", i), "x\n")
	}
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	// No explicit Limit -> should fall back to DEFAULT_LIMIT (100) and
	// still report truncation since 150 files match.
	res := run(t, tool, args, filesearchtool.Input{Pattern: "a"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "matches limit reached") {
		t.Errorf("expected default-limit notice, got tail:\n%s", tail(res.Content, 200))
	}
}

func TestGitignoreRespected(t *testing.T) {
	requireRG(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping")
	}
	dir := t.TempDir()

	initCmd := exec.Command("git", "init", "-q", dir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	writeFile(t, dir, ".gitignore", "ignored.go\n")
	writeFile(t, dir, "ignored.go", "x\n")
	writeFile(t, dir, "kept.go", "x\n")

	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.Contains(res.Content, "ignored.go") {
		t.Errorf("expected ignored.go to be skipped, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "kept.go") {
		t.Errorf("expected kept.go to be listed, got:\n%s", res.Content)
	}
}

func TestAgentignoreRespected(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, ".agentignore", "secret.go\n")
	writeFile(t, dir, "secret.go", "x\n")
	writeFile(t, dir, "kept.go", "x\n")

	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.Contains(res.Content, "secret.go") {
		t.Errorf("expected secret.go to be skipped via .agentignore, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "kept.go") {
		t.Errorf("expected kept.go to be listed, got:\n%s", res.Content)
	}
}

func TestHiddenFilesSearchable(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, ".env", "SECRET=1\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: `.env`})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, ".env") {
		t.Errorf("expected hidden file to be findable by name, got:\n%s", res.Content)
	}
}

func TestContextCancellation(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Run

	raw, err := json.Marshal(filesearchtool.Input{Pattern: "main"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	_, err = tool.Run(ctx, args, raw)
	if err == nil {
		t.Fatal("expected an error for a pre-cancelled context, got nil")
	}
}

func TestInvalidJSONInput(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	_, err := tool.Run(context.Background(), args, json.RawMessage(`{not valid json`))
	if err == nil {
		t.Fatal("expected an error for invalid JSON input, got nil")
	}
}

func TestDefaultPathSearchesWholeProject(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "top.go", "x\n")
	writeFile(t, dir, "nested/deep/file.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	// No Path specified -> defaults to ".", i.e. the whole project root.
	res := run(t, tool, args, filesearchtool.Input{Pattern: "go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "top.go") {
		t.Errorf("expected top-level match, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "nested/deep/file.go") {
		t.Errorf("expected nested match, got:\n%s", res.Content)
	}
}

func TestSingleFileAsPath(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "x\n")
	writeFile(t, dir, "b.go", "x\n")
	args := newDispatchArgs(t, dir)
	tool := filesearchtool.New()

	res := run(t, tool, args, filesearchtool.Input{Pattern: "a", Path: "a.go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Errorf("expected a.go in results, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "b.go") {
		t.Errorf("did not expect b.go when Path targets a.go, got:\n%s", res.Content)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
