package readfiletool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GoWork/tools/ReadFileTool"
	"GoWork/tools"
)

// newArgs opens a sandboxed DispatchArgs rooted at dir and closes it when
// the test ends.
func newArgs(t *testing.T, dir string) tools.DispatchArgs {
	t.Helper()
	args, err := tools.InitDispatchArgs(dir)
	if err != nil {
		t.Fatalf("InitDispatchArgs: %v", err)
	}
	t.Cleanup(func() { _ = args.Root.Close() })
	return args
}

// writeLines writes an n-line file ("line 1".."line n") into dir and
// returns its absolute path.
func writeLines(t *testing.T, dir, name string, n int) string {
	t.Helper()
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// expectedRange builds the "N.line N\n" text read_file should produce for
// [start, start+count-1] over a file built by writeLines.
func expectedRange(start, count int) string {
	var sb strings.Builder
	for i := start; i < start+count; i++ {
		fmt.Fprintf(&sb, "%d.line %d\n", i, i)
	}
	return sb.String()
}

// run marshals input, calls Run, and fails the test on an unexpected Go
// error (as opposed to a ToolResult{IsError:true}, which callers check
// themselves).
func run(t *testing.T, tool tools.AgentTool, args tools.DispatchArgs, input readfiletool.Input) tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Run(context.Background(), args, raw)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	return res
}

func TestMetadata(t *testing.T) {
	tool := readfiletool.New()

	if got := tool.Name(); got != "read_file" {
		t.Errorf("Name() = %q, want %q", got, "read_file")
	}
	if got := tool.Kind(); got != tools.KindRead {
		t.Errorf("Kind() = %v, want %v", got, tools.KindRead)
	}
	if desc := tool.Description(); desc == "" {
		t.Error("Description() is empty")
	}

	schema := tool.InputSchema()

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema[\"properties\"] is %T, want map[string]any", schema["properties"])
	}
	for _, key := range []string{"path", "starting_line", "offset_lines"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema properties missing %q", key)
		}
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema[\"required\"] is %T, want []string", schema["required"])
	}
	if len(required) != 1 || required[0] != "path" {
		t.Errorf("schema required = %v, want [\"path\"]", required)
	}
}

func TestRun_DefaultReadsWholeFile(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 3)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt"})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if want := expectedRange(1, 3); res.Content != want {
		t.Errorf("Content = %q, want %q", res.Content, want)
	}
}

func TestRun_StartAndOffsetSelectSubrange(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 10)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt", Start: 3, Offset: 3})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if want := expectedRange(3, 3); res.Content != want {
		t.Errorf("Content = %q, want %q", res.Content, want)
	}
}

func TestRun_OffsetPastEOFIsClampedNotAnError(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 3)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt", Start: 2, Offset: 100})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if want := expectedRange(2, 2); res.Content != want {
		t.Errorf("Content = %q, want %q", res.Content, want)
	}
}

func TestRun_StartPastEOFIsAnError(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 3)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt", Start: 10})

	if !res.IsError {
		t.Fatalf("expected an error, got content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "past the end of the file") {
		t.Errorf("Content = %q, want it to mention the file is too short", res.Content)
	}
}

func TestRun_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0644); err != nil {
		t.Fatalf("writing empty file: %v", err)
	}

	res := run(t, tool, args, readfiletool.Input{Path: "empty.txt"})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "(file is empty)" {
		t.Errorf("Content = %q, want %q", res.Content, "(file is empty)")
	}
}

func TestRun_StartBelowOneIsClampedToOne(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 3)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt", Start: -5})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if want := expectedRange(1, 3); res.Content != want {
		t.Errorf("Content = %q, want %q", res.Content, want)
	}
}

func TestRun_NonPositiveOffsetDefaultsToWholeFile(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 3)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt", Offset: -1})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if want := expectedRange(1, 3); res.Content != want {
		t.Errorf("Content = %q, want %q", res.Content, want)
	}
}

func TestRun_OffsetIsCappedAtMaxLines(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	// more lines than the cap, but short enough that the byte cap never
	// kicks in first, so this isolates the line-count cap specifically.
	writeLines(t, dir, "f.txt", readfiletool.DEFAULT_MAX_LINES+50)

	res := run(t, tool, args, readfiletool.Input{Path: "f.txt", Offset: 999999})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	lastLine := fmt.Sprintf("%d.line %d\n", readfiletool.DEFAULT_MAX_LINES, readfiletool.DEFAULT_MAX_LINES)
	firstLineOver := fmt.Sprintf("%d.line %d\n", readfiletool.DEFAULT_MAX_LINES+1, readfiletool.DEFAULT_MAX_LINES+1)
	if !strings.HasSuffix(res.Content, lastLine) {
		t.Errorf("Content doesn't end with the %d-th line as expected", readfiletool.DEFAULT_MAX_LINES)
	}
	if strings.Contains(res.Content, firstLineOver) {
		t.Errorf("Content contains line %d, want it capped at %d", readfiletool.DEFAULT_MAX_LINES+1, readfiletool.DEFAULT_MAX_LINES)
	}
	if strings.Contains(res.Content, "truncated") {
		t.Errorf("Content unexpectedly mentions byte truncation for a line-count cap: %q", res.Content)
	}
}

func TestRun_ByteCapTruncatesLongOutputAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()

	// 700 lines of 100 chars each is well past DEFAULT_MAX_BYTES (50KB)
	// but nowhere near DEFAULT_MAX_LINES, isolating the byte cap.
	var sb strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&sb, "%s\n", strings.Repeat("x", 100))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	res := run(t, tool, args, readfiletool.Input{Path: "big.txt"})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Errorf("expected a truncation notice, got content of length %d", len(res.Content))
	}
	if strings.Contains(res.Content, "700.") {
		t.Errorf("expected output to stop well before line 700")
	}
	maxAllowed := readfiletool.DEFAULT_MAX_BYTES + 200 // slack for the trailing notice
	if len(res.Content) > maxAllowed {
		t.Errorf("Content length = %d, want <= ~%d", len(res.Content), maxAllowed)
	}
}

func TestRun_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()

	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeLines(t, dir, "exists.txt", 3)

	cases := []struct {
		name    string
		input   readfiletool.Input
		wantSub string
	}{
		{"empty path", readfiletool.Input{Path: ""}, "path is required"},
		{"dot path", readfiletool.Input{Path: "."}, "must point to a file"},
		{"missing file", readfiletool.Input{Path: "nope.txt"}, "does not exist"},
		{"directory", readfiletool.Input{Path: "subdir"}, "directory"},
		{"escapes root via ..", readfiletool.Input{Path: "../outside.txt"}, "project root"},
		{"absolute path", readfiletool.Input{Path: "/etc/hostname"}, "project root"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := run(t, tool, args, tc.input)
			if !res.IsError {
				t.Fatalf("expected IsError=true, got content=%q", res.Content)
			}
			if !strings.Contains(res.Content, tc.wantSub) {
				t.Errorf("Content = %q, want it to contain %q", res.Content, tc.wantSub)
			}
		})
	}
}

func TestRun_InvalidJSONReturnsAGoError(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()

	_, err := tool.Run(context.Background(), args, json.RawMessage(`{not valid json`))
	if err == nil {
		t.Fatal("expected an error for malformed JSON input, got nil")
	}
}

// TestRun_CacheHitReturnsAlreadyReadNotice proves a repeat read of the same
// range on an unchanged file returns a short "already read" notice instead
// of repeating the file content.
func TestRun_CacheHitReturnsAlreadyReadNotice(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 5)

	first := run(t, tool, args, readfiletool.Input{Path: "f.txt"})
	if first.IsError {
		t.Fatalf("unexpected error: %s", first.Content)
	}
	if want := expectedRange(1, 5); first.Content != want {
		t.Fatalf("first read = %q, want the file content %q", first.Content, want)
	}

	second := run(t, tool, args, readfiletool.Input{Path: "f.txt"})
	if second.IsError {
		t.Fatalf("unexpected error: %s", second.Content)
	}
	if second.Content == first.Content {
		t.Fatalf("expected the second read to return a notice, not the file content again")
	}
	if !strings.Contains(second.Content, "already read") {
		t.Errorf("Content = %q, want it to mention the lines were already read", second.Content)
	}
	if !strings.Contains(second.Content, "f.txt") {
		t.Errorf("Content = %q, want it to name the file", second.Content)
	}
	if !strings.Contains(second.Content, "1-5") {
		t.Errorf("Content = %q, want it to mention the actual line range 1-5 (clamped to the file's length)", second.Content)
	}
	if strings.Contains(second.Content, "line 1\n") {
		t.Errorf("Content = %q, want it to NOT repeat the file content", second.Content)
	}
}

// TestRun_FileChangeInvalidatesCache proves a changed mtime is treated as
// a cache miss: the second read must reflect the new file contents, not
// whatever was cached from the first read.
func TestRun_FileChangeInvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	path := writeLines(t, dir, "f.txt", 3)

	first := run(t, tool, args, readfiletool.Input{Path: "f.txt"})
	if first.IsError {
		t.Fatalf("unexpected error: %s", first.Content)
	}
	if !strings.Contains(first.Content, "line 1") {
		t.Fatalf("first read = %q, want it to contain the original content", first.Content)
	}

	if err := os.WriteFile(path, []byte("changed content\n"), 0644); err != nil {
		t.Fatalf("rewriting file: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	second := run(t, tool, args, readfiletool.Input{Path: "f.txt"})
	if second.IsError {
		t.Fatalf("unexpected error: %s", second.Content)
	}
	if !strings.Contains(second.Content, "changed content") {
		t.Errorf("second read = %q, want the new content", second.Content)
	}
	if strings.Contains(second.Content, "line 1") {
		t.Errorf("second read = %q, served stale cached content after the file changed", second.Content)
	}
}

// TestRun_DistinctRangesCachedSeparately makes sure the cache key includes
// both start and offset, not just the path.
func TestRun_DistinctRangesCachedSeparately(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()
	writeLines(t, dir, "f.txt", 10)

	run(t, tool, args, readfiletool.Input{Path: "f.txt", Start: 1, Offset: 2})
	run(t, tool, args, readfiletool.Input{Path: "f.txt", Start: 3, Offset: 2})

	absPath := filepath.Join(args.RootPath, "f.txt")
	rs := tools.Load(absPath)
	if len(rs.Ranges) != 2 {
		t.Errorf("expected 2 distinct cached ranges, got %d", len(rs.Ranges))
	}
}
