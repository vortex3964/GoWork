package readfiletool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"GoWork/tools/ReadFileTool"
	"GoWork/tools"
)

// newArgs opens a sandboxed DispatchArgs rooted at dir and closes it when
// the test ends.
func newArgs(t *testing.T, dir string) tools.DispatchArgs {
	t.Helper()
	args, err := tools.InitDispatchArgs(dir, nil, nil)
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

	var m map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &m); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema[\"properties\"] is %T, want map[string]any", m["properties"])
	}
	for _, key := range []string{"path", "starting_line", "offset_lines"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema properties missing %q", key)
		}
	}

	required, ok := m["required"].([]any)
	if !ok {
		t.Fatalf("schema[\"required\"] is %T, want []any", m["required"])
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

// makeStubPDF installs a fake venv python under dir that behaves like the pdf
// parser: it cats its second argument (the pdf path) to stdout. Returns the
// dir root unchanged (dir itself is the sandbox root).
func makeStubPDF(t *testing.T, dir string, fail bool) {
	t.Helper()
	venvBin := filepath.Join(dir, "tools", "Scripts", "venv", "bin")
	if err := os.MkdirAll(venvBin, 0755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	script := filepath.Join(dir, "tools", "Scripts", "read_pdf.py")
	if err := os.WriteFile(script, []byte("# stub\n"), 0644); err != nil {
		t.Fatalf("writing stub read_pdf.py: %v", err)
	}
	stub := "#!/bin/sh\n"
	if fail {
		stub += "echo 'backend exploded' >&2\nexit 1\n"
	} else {
		stub += "cat \"$2\"\n"
	}
	if err := os.WriteFile(filepath.Join(venvBin, "python"), []byte(stub), 0755); err != nil {
		t.Fatalf("writing stub python: %v", err)
	}
}

func TestRun_ImageIsRefusedWithError(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	tool := readfiletool.New()

	// binary-ish content, but the extension alone is enough to refuse
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("not really a png"), 0644); err != nil {
		t.Fatalf("writing png: %v", err)
	}

	for _, ext := range []string{"png", "jpg", "jpeg", "gif", "webp", "svg", "ico"} {
		p := "pic." + ext
		if err := os.WriteFile(filepath.Join(dir, p), []byte("x"), 0644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
		res := run(t, tool, args, readfiletool.Input{Path: p})
		if !res.IsError {
			t.Errorf("%s: expected IsError=true", p)
		}
		if !strings.Contains(res.Content, "cannot read image files") {
			t.Errorf("%s: Content = %q, want it to mention images are unreadable", p, res.Content)
		}
	}
}

func TestRun_PDFFullOutputViaStubParser(t *testing.T) {
	dir := t.TempDir()
	makeStubPDF(t, dir, false)
	args := newArgs(t, dir)
	tool := readfiletool.New()

	// content mimicking parsed markdown, spread over multiple lines
	pdfContent := "# Title\n\nsome body text\nsecond line\n"
	if err := os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte(pdfContent), 0644); err != nil {
		t.Fatalf("writing pdf: %v", err)
	}

	res := run(t, tool, args, readfiletool.Input{Path: "doc.pdf"})

	if res.IsError {
		t.Fatalf("unexpected: error: %s", res.Content)
	}
	// full output, verbatim, no line numbers, no truncation
	if res.Content != pdfContent {
		t.Errorf("Content = %q, want full pdf output %q", res.Content, pdfContent)
	}
}

func TestRun_PDFParserFailureSurfacesStderr(t *testing.T) {
	dir := t.TempDir()
	makeStubPDF(t, dir, true)
	args := newArgs(t, dir)
	tool := readfiletool.New()

	if err := os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("x"), 0644); err != nil {
		t.Fatalf("writing pdf: %v", err)
	}

	res := run(t, tool, args, readfiletool.Input{Path: "doc.pdf"})

	if !res.IsError {
		t.Fatalf("expected an error, got content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "backend exploded") {
		t.Errorf("Content = %q, want it to surface the parser's stderr", res.Content)
	}
}

