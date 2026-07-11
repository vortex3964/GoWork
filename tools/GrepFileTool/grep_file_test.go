package grepfiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	grepfiletool "GoWork/tools/GrepFileTool"
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
	args, err := tools.InitDispatchArgs(dir)
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

func run(t *testing.T, tool tools.AgentTool, args tools.DispatchArgs, input grepfiletool.Input) tools.ToolResult {
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
	tool := grepfiletool.New()

	if got := tool.Name(); got != "grep_file" {
		t.Errorf("Name() = %q, want %q", got, "grep_file")
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if tool.Kind() != tools.KindSearch {
		t.Errorf("Kind() = %v, want %v", tool.Kind(), tools.KindSearch)
	}
	schema := tool.InputSchema()
	if schema["type"] != "object" {
		t.Errorf(`InputSchema()["type"] = %v, want "object"`, schema["type"])
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "pattern" {
		t.Errorf(`InputSchema()["required"] = %v, want ["pattern"]`, schema["required"])
	}
}

func TestMissingPattern(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: ""})
	if !res.IsError {
		t.Fatalf("expected IsError for empty pattern, got: %+v", res)
	}
}

func TestAbsolutePathRejected(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "foo", Path: "/etc/passwd"})
	if !res.IsError {
		t.Fatalf("expected IsError for absolute path, got: %+v", res)
	}
}

func TestParentTraversalRejected(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "foo", Path: "../outside"})
	if !res.IsError {
		t.Fatalf("expected IsError for '..' path, got: %+v", res)
	}
}

func TestNonexistentPath(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "foo", Path: "does/not/exist.go"})
	if !res.IsError {
		t.Fatalf("expected IsError for nonexistent path, got: %+v", res)
	}
	if !strings.Contains(res.Content, "does not exist") {
		t.Errorf("expected 'does not exist' in error message, got: %q", res.Content)
	}
}

func TestBasicMatch(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "func main"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go:3: func main() {") {
		t.Errorf("expected match line, got:\n%s", res.Content)
	}
}

func TestNoMatches(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "nonexistentPattern12345"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "No matches found" {
		t.Errorf("Content = %q, want %q", res.Content, "No matches found")
	}
}

func TestIgnoreCase(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\n// TODO: fix this\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	// Without ignore_case, lowercase "todo" shouldn't match "TODO".
	res := run(t, tool, args, grepfiletool.Input{Pattern: "todo"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "No matches found" {
		t.Errorf("expected no case-sensitive match, got:\n%s", res.Content)
	}

	res = run(t, tool, args, grepfiletool.Input{Pattern: "todo", IgnoreCase: true})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "TODO: fix this") {
		t.Errorf("expected case-insensitive match, got:\n%s", res.Content)
	}
}

func TestLiteralMode(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "if x == 1 {\n\tdoStuff()\n}\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	// "(" is a regex metacharacter; as a literal it should match
	// doStuff( exactly, but as a regex it's an unmatched paren and would
	// cause rg to error out unless treated literally.
	res := run(t, tool, args, grepfiletool.Input{Pattern: "doStuff(", Literal: true})
	if res.IsError {
		t.Fatalf("unexpected error for literal search: %s", res.Content)
	}
	if !strings.Contains(res.Content, "doStuff()") {
		t.Errorf("expected literal match, got:\n%s", res.Content)
	}
}

func TestRegexMode(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "var a1 int\nvar b2 int\nvar cc int\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: `var [a-z]\d`})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a1 int") || !strings.Contains(res.Content, "b2 int") {
		t.Errorf("expected both regex matches, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "cc int") {
		t.Errorf("did not expect 'cc int' to match \\d, got:\n%s", res.Content)
	}
}

func TestIncludeGlobFilter(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "needle here\n")
	writeFile(t, dir, "main.txt", "needle here\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle", Include: "*.go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go:") {
		t.Errorf("expected main.go in results, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "main.txt:") {
		t.Errorf("did not expect main.txt in results, got:\n%s", res.Content)
	}
}

func TestContextLines(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "line1\nline2\nMATCHME\nline4\nline5\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "MATCHME", ContextLines: 1})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	// Match line uses ':' separators, context lines use '-'.
	if !strings.Contains(res.Content, "main.go:3: MATCHME") {
		t.Errorf("expected match line with ':' separators, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go-2- line2") {
		t.Errorf("expected context line before match with '-' separators, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go-4- line4") {
		t.Errorf("expected context line after match with '-' separators, got:\n%s", res.Content)
	}
}

func TestContextLinesBlockBreak(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	// Two matches far enough apart that their single-line context
	// windows don't overlap, so rg emits them as separate blocks and
	// the tool should print "--" between the non-contiguous groups.
	lines := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		if i == 3 {
			lines = append(lines, "MATCHME")
		} else if i == 15 {
			lines = append(lines, "MATCHME")
		} else {
			lines = append(lines, "filler")
		}
	}
	writeFile(t, dir, "main.go", strings.Join(lines, "\n")+"\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "MATCHME", ContextLines: 1})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "--\n") {
		t.Errorf("expected '--' separator between non-contiguous match blocks, got:\n%s", res.Content)
	}
}

func TestMultipleFilesBlankLineSeparator(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "needle\n")
	writeFile(t, dir, "b.go", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go:1: needle") || !strings.Contains(res.Content, "b.go:1: needle") {
		t.Errorf("expected matches from both files, got:\n%s", res.Content)
	}
	// A blank line should separate blocks from different files (rg's
	// file order isn't guaranteed, so just check a blank line exists
	// between two non-empty lines rather than asserting exact order).
	if !strings.Contains(res.Content, "\n\n") {
		t.Errorf("expected blank line separating files, got:\n%s", res.Content)
	}
}

func TestSubdirectoryRelativePaths(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "pkg/sub/file.go", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	// Path should be root-relative and forward-slashed, not absolute.
	if !strings.Contains(res.Content, "pkg/sub/file.go:1: needle") {
		t.Errorf("expected forward-slashed relative path, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, dir) {
		t.Errorf("did not expect absolute path in output, got:\n%s", res.Content)
	}
}

func TestSearchScopedToPath(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "pkg/a.go", "needle\n")
	writeFile(t, dir, "other/b.go", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle", Path: "pkg"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "pkg/a.go:1: needle") {
		t.Errorf("expected match in scoped dir, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "other/b.go") {
		t.Errorf("did not expect match outside scoped dir, got:\n%s", res.Content)
	}
}

func TestMatchLimit(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("needle\n")
	}
	writeFile(t, dir, "main.go", sb.String())
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle", Limit: 3})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	got := strings.Count(res.Content, "needle")
	if got != 3 {
		t.Errorf("expected 3 matched lines under limit=3, got %d in:\n%s", got, res.Content)
	}
	if !strings.Contains(res.Content, "matches limit reached") {
		t.Errorf("expected limit-reached notice, got:\n%s", res.Content)
	}
}

func TestDefaultLimitApplied(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 150; i++ {
		sb.WriteString("needle\n")
	}
	writeFile(t, dir, "main.go", sb.String())
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	// No explicit Limit -> should fall back to DEFAULT_LIMIT (100) and
	// still report truncation since the file has 150 matching lines.
	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "matches limit reached") {
		t.Errorf("expected default-limit notice, got tail:\n%s", tail(res.Content, 200))
	}
}

func TestLineLengthTruncation(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	longLine := "needle " + strings.Repeat("x", 1000)
	writeFile(t, dir, "main.go", longLine+"\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "...") {
		t.Errorf("expected truncated line to end with '...', got tail:\n%s", tail(res.Content, 200))
	}
	if !strings.Contains(res.Content, "truncated to") {
		t.Errorf("expected line-truncation notice, got tail:\n%s", tail(res.Content, 200))
	}
	if strings.Contains(res.Content, strings.Repeat("x", 1000)) {
		t.Errorf("expected line to be shortened, but full 1000-char run is present")
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
	writeFile(t, dir, "ignored.go", "needle\n")
	writeFile(t, dir, "kept.go", "needle\n")

	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.Contains(res.Content, "ignored.go:") {
		t.Errorf("expected ignored.go to be skipped, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "kept.go:") {
		t.Errorf("expected kept.go to be searched, got:\n%s", res.Content)
	}
}

func TestAgentignoreRespected(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, ".agentignore", "secret.go\n")
	writeFile(t, dir, "secret.go", "needle\n")
	writeFile(t, dir, "kept.go", "needle\n")

	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.Contains(res.Content, "secret.go:") {
		t.Errorf("expected secret.go to be skipped via .agentignore, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "kept.go:") {
		t.Errorf("expected kept.go to be searched, got:\n%s", res.Content)
	}
}

func TestHiddenFilesSearched(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	// Tool passes --hidden, so dotfiles not covered by an ignore file
	// should still be searched.
	writeFile(t, dir, ".env", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, ".env:1: needle") {
		t.Errorf("expected hidden file to be searched, got:\n%s", res.Content)
	}
}

func TestContextCancellation(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Run

	raw, err := json.Marshal(grepfiletool.Input{Pattern: "needle"})
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
	tool := grepfiletool.New()

	_, err := tool.Run(context.Background(), args, json.RawMessage(`{not valid json`))
	if err == nil {
		t.Fatal("expected an error for invalid JSON input, got nil")
	}
}

func TestDefaultPathSearchesWholeProject(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "top.go", "needle\n")
	writeFile(t, dir, "nested/deep/file.go", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	// No Path specified -> defaults to ".", i.e. the whole project root.
	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "top.go:1: needle") {
		t.Errorf("expected top-level match, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "nested/deep/file.go:1: needle") {
		t.Errorf("expected nested match, got:\n%s", res.Content)
	}
}

func TestSingleFileAsPath(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "needle\n")
	writeFile(t, dir, "b.go", "needle\n")
	args := newDispatchArgs(t, dir)
	tool := grepfiletool.New()

	res := run(t, tool, args, grepfiletool.Input{Pattern: "needle", Path: "a.go"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go:1: needle") {
		t.Errorf("expected match in a.go, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "b.go") {
		t.Errorf("did not expect b.go to be searched when Path targets a.go, got:\n%s", res.Content)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

func TestToolsPackageReadStateModTime(t *testing.T) {
	rs := &tools.ReadState{ModTime: time.Now()}
	if rs.ModTime.IsZero() {
		t.Fatal("expected non-zero ModTime")
	}
}
