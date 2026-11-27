package gowork

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	permisionbox "GoWork/Tui/Components/PermisionBox"
)

func writeImpactFixture(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

func impactRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal impact input: %v", err)
	}
	return b
}

func TestExplainWriteFileImpact(t *testing.T) {
	root := t.TempDir()
	path := writeImpactFixture(t, root, "main.go", "alpha\nbeta\ngamma\n")

	impact := explainToolImpact("write_file", impactRaw(t, map[string]any{
		"file_path":  "main.go",
		"old_string": "beta",
		"new_string": "BETA\nmore",
	}), root)

	for _, want := range []string{"main.go", "replaces 1 occurrence", "1 line(s) -> 2 line(s)", "3 line(s)"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("summary missing %q: %s", want, impact.Summary)
		}
	}
	for _, want := range []string{"- beta", "+ BETA", "+ more"} {
		if !strings.Contains(impact.Preview, want) {
			t.Errorf("preview missing %q:\n%s", want, impact.Preview)
		}
	}

	impact = explainToolImpact("write_file", impactRaw(t, map[string]any{
		"file_path":  "main.go",
		"old_string": "missing-snippet",
		"new_string": "x",
	}), root)
	if !strings.Contains(impact.Summary, "does not appear") || !strings.Contains(impact.Summary, "fail") {
		t.Errorf("missing-snippet summary = %s", impact.Summary)
	}

	impact = explainToolImpact("write_file", impactRaw(t, map[string]any{
		"file_path":  "nope.go",
		"old_string": "a",
		"new_string": "b",
	}), root)
	if !strings.Contains(impact.Summary, "does not exist") {
		t.Errorf("missing-file summary = %s", impact.Summary)
	}

	if data, err := os.ReadFile(path); err != nil || string(data) != "alpha\nbeta\ngamma\n" {
		t.Errorf("explaining write_file mutated the target: %q, err=%v", data, err)
	}
}

func TestExplainEditFileImpact(t *testing.T) {
	root := t.TempDir()
	path := writeImpactFixture(t, root, "pkg/a.go", "one\ntwo\nthree\nfour\n")

	impact := explainToolImpact("edit_file", impactRaw(t, map[string]any{
		"file_path":   "pkg/a.go",
		"start_line":  2,
		"end_line":    3,
		"new_content": "TWO\nTHREE",
	}), root)
	for _, want := range []string{"Replaces lines 2–3", "2 line(s)", "pkg/a.go", "4 line(s)"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("summary missing %q: %s", want, impact.Summary)
		}
	}
	for _, want := range []string{"- two", "- three", "+ TWO", "+ THREE"} {
		if !strings.Contains(impact.Preview, want) {
			t.Errorf("preview missing %q:\n%s", want, impact.Preview)
		}
	}

	impact = explainToolImpact("edit_file", impactRaw(t, map[string]any{
		"file_path":   "pkg/a.go",
		"start_line":  2,
		"end_line":    1,
		"new_content": "inserted",
	}), root)
	if !strings.Contains(impact.Summary, "Inserts 1 line(s) at line 2") {
		t.Errorf("insertion summary = %s", impact.Summary)
	}
	if !strings.Contains(impact.Preview, "+ inserted") {
		t.Errorf("insertion preview = %s", impact.Preview)
	}

	impact = explainToolImpact("edit_file", impactRaw(t, map[string]any{
		"file_path":   "pkg/a.go",
		"start_line":  99,
		"end_line":    99,
		"new_content": "x",
	}), root)
	if !strings.Contains(impact.Summary, "past the end") || !strings.Contains(impact.Summary, "fail") {
		t.Errorf("invalid range summary = %s", impact.Summary)
	}

	if data, err := os.ReadFile(path); err != nil || string(data) != "one\ntwo\nthree\nfour\n" {
		t.Errorf("explaining edit_file mutated the target: %q, err=%v", data, err)
	}
}

func TestExplainCreateFileImpact(t *testing.T) {
	root := t.TempDir()

	impact := explainToolImpact("create_file_tool", impactRaw(t, map[string]any{
		"path":    "new/file.txt",
		"content": "hello\nworld",
	}), root)
	for _, want := range []string{"Creates new file new/file.txt", "2 line(s)", "parent directories"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("create summary missing %q: %s", want, impact.Summary)
		}
	}
	if !strings.Contains(impact.Preview, "+ hello") || !strings.Contains(impact.Preview, "+ world") {
		t.Errorf("create preview = %s", impact.Preview)
	}

	writeImpactFixture(t, root, "existing.txt", "old content\n")
	impact = explainToolImpact("create_file_tool", impactRaw(t, map[string]any{
		"path":    "existing.txt",
		"content": "new content",
	}), root)
	for _, want := range []string{"already exists", "refuses to overwrite", "will fail"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("existing-target summary missing %q: %s", want, impact.Summary)
		}
	}
	if !strings.Contains(impact.Preview, "- old content") || !strings.Contains(impact.Preview, "+ new content") {
		t.Errorf("existing-target preview = %s", impact.Preview)
	}

	impact = explainToolImpact("create_file_tool", impactRaw(t, map[string]any{
		"path": "newdir/",
	}), root)
	if !strings.Contains(impact.Summary, "Creates directory newdir/") {
		t.Errorf("directory summary = %s", impact.Summary)
	}

	impact = explainToolImpact("create_file_tool", impactRaw(t, map[string]any{
		"path":    "empty.txt",
		"content": "   ",
	}), root)
	if !strings.Contains(impact.Summary, "content is empty") || !strings.Contains(impact.Summary, "refuse") {
		t.Errorf("empty-content summary = %s", impact.Summary)
	}
}

func TestExplainMoveFileImpact(t *testing.T) {
	root := t.TempDir()
	writeImpactFixture(t, root, "src.txt", "source line\n")
	writeImpactFixture(t, root, "dst.txt", "old destination\n")

	impact := explainToolImpact("move_file", impactRaw(t, map[string]any{
		"source_path":      "src.txt",
		"destination_path": "dst.txt",
	}), root)
	for _, want := range []string{"Moves src.txt to dst.txt", "already exists", "overwritten"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("move summary missing %q: %s", want, impact.Summary)
		}
	}
	if !strings.Contains(impact.Preview, "- old destination") || !strings.Contains(impact.Preview, "+ source line") {
		t.Errorf("move preview = %s", impact.Preview)
	}

	impact = explainToolImpact("move_file", impactRaw(t, map[string]any{
		"source_path":      "missing.txt",
		"destination_path": "dst.txt",
	}), root)
	if !strings.Contains(impact.Summary, "source does not exist") || !strings.Contains(impact.Summary, "fail") {
		t.Errorf("missing-source summary = %s", impact.Summary)
	}

	impact = explainToolImpact("move_file", impactRaw(t, map[string]any{
		"source_path":      "src.txt",
		"destination_path": "newdir/dst.txt",
	}), root)
	if !strings.Contains(impact.Summary, "Missing destination directories") {
		t.Errorf("new-destination summary = %s", impact.Summary)
	}
}

func TestExplainDeleteFileImpact(t *testing.T) {
	root := t.TempDir()
	writeImpactFixture(t, root, "gone.txt", "line1\nline2\n")

	impact := explainToolImpact("delete_file", impactRaw(t, map[string]any{"path": "gone.txt"}), root)
	for _, want := range []string{"Deletes gone.txt", "2 line(s)", "parent directory"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("delete summary missing %q: %s", want, impact.Summary)
		}
	}
	if !strings.Contains(impact.Preview, "- line1") || !strings.Contains(impact.Preview, "- line2") {
		t.Errorf("delete preview = %s", impact.Preview)
	}

	impact = explainToolImpact("delete_file", impactRaw(t, map[string]any{"path": "nope.txt"}), root)
	if !strings.Contains(impact.Summary, "does not exist") || !strings.Contains(impact.Summary, "fail") {
		t.Errorf("missing delete summary = %s", impact.Summary)
	}
}

func TestExplainBashImpact(t *testing.T) {
	command := "echo hi && pwd"
	impact := explainToolImpact("bash", impactRaw(t, map[string]any{
		"command": command,
	}), "")
	for _, want := range []string{"project root", "stdout/stderr", "exit code"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("bash summary missing %q: %s", want, impact.Summary)
		}
	}
	if !strings.Contains(impact.Preview, command) {
		t.Errorf("bash preview missing exact command: %s", impact.Preview)
	}

	impact = explainToolImpact("bash", impactRaw(t, map[string]any{
		"command":     "ls",
		"working_dir": "sub",
	}), "")
	if !strings.Contains(impact.Summary, `"sub" under the project root`) {
		t.Errorf("bash working_dir summary = %s", impact.Summary)
	}

	impact = explainToolImpact("bash", impactRaw(t, map[string]any{
		"command":           "sleep 100",
		"run_in_background": true,
	}), "")
	if !strings.Contains(impact.Summary, "background job") {
		t.Errorf("bash background summary = %s", impact.Summary)
	}
}

func TestExplainWebToolsImpact(t *testing.T) {
	impact := explainToolImpact("web_fetch", impactRaw(t, map[string]any{
		"url":    "https://example.com/pricing",
		"prompt": "extract the prices",
	}), "")
	for _, want := range []string{"https://example.com/pricing", "summarized", "extract the prices", ".agent-cache/fetch/"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("web_fetch summary missing %q: %s", want, impact.Summary)
		}
	}
	if !strings.Contains(impact.Preview, "GET https://example.com/pricing") {
		t.Errorf("web_fetch preview = %s", impact.Preview)
	}

	impact = explainToolImpact("web_search", impactRaw(t, map[string]any{
		"query":       "golang generics",
		"max_results": 3,
	}), "")
	for _, want := range []string{"golang generics", "up to 3 results", "summary"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("web_search summary missing %q: %s", want, impact.Summary)
		}
	}
}

func TestExplainSearchToolsImpact(t *testing.T) {
	impact := explainToolImpact("grep_search", impactRaw(t, map[string]any{
		"pattern":       "needle",
		"path":          "src",
		"include":       "*.go",
		"ignore_case":   true,
		"context_lines": 2,
	}), "")
	for _, want := range []string{"src", "needle", "100 matching line(s)", "Read-only"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("grep summary missing %q: %s", want, impact.Summary)
		}
	}

	impact = explainToolImpact("file_search", impactRaw(t, map[string]any{
		"pattern": "main",
		"path":    "pkg",
		"limit":   20,
	}), "")
	for _, want := range []string{"20 file path(s)", "pkg", "main"} {
		if !strings.Contains(impact.Summary, want) {
			t.Errorf("file_search summary missing %q: %s", want, impact.Summary)
		}
	}
}

func TestExplainPreviewIsBounded(t *testing.T) {
	root := t.TempDir()
	writeImpactFixture(t, root, "big.txt", "")
	impact := explainToolImpact("edit_file", impactRaw(t, map[string]any{
		"file_path":   "big.txt",
		"start_line":  1,
		"end_line":    0,
		"new_content": strings.Repeat("new line\n", 100),
	}), root)

	lines := strings.Split(impact.Preview, "\n")
	if len(lines) > 8 {
		t.Errorf("preview has %d lines, want at most 8: %q", len(lines), impact.Preview)
	}
	if !strings.Contains(impact.Preview, "…") {
		t.Errorf("expected truncation marker in bounded preview:\n%s", impact.Preview)
	}
}

func TestExplainPreviewLineWidthIsBounded(t *testing.T) {
	root := t.TempDir()
	writeImpactFixture(t, root, "target.txt", "old target line\n")
	longLine := strings.Repeat("X", 200_000)

	check := func(name string, impact permisionbox.Impact) {
		t.Helper()
		if len(impact.Preview) > 1200 {
			t.Errorf("%s: preview is %d bytes, want a bounded preview", name, len(impact.Preview))
		}
		for _, line := range strings.Split(impact.Preview, "\n") {
			if width := utf8.RuneCountInString(line); width > impactPreviewLineWidth+3 {
				t.Errorf("%s: preview line width = %d, want <= %d: %q", name, width, impactPreviewLineWidth+3, line)
			}
		}
		if !strings.Contains(impact.Preview, "…") {
			t.Errorf("%s: expected an explicit truncation marker in preview", name)
		}
	}

	// Oversized new content.
	check("new_content", explainToolImpact("edit_file", impactRaw(t, map[string]any{
		"file_path":   "target.txt",
		"start_line":  1,
		"end_line":    1,
		"new_content": longLine,
	}), root))

	// Oversized single line read from the target file itself.
	writeImpactFixture(t, root, "longline.txt", longLine+"\n")
	check("target file", explainToolImpact("delete_file", impactRaw(t, map[string]any{
		"path": "longline.txt",
	}), root))
}

func TestExplainOversizedTargetReadIsCapped(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "huge.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create huge file: %v", err)
	}
	if err := f.Truncate(impactReadLimit + 4096); err != nil {
		f.Close()
		t.Fatalf("truncate huge file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close huge file: %v", err)
	}

	data, _, truncated, err := impactReadFile(root, "huge.txt")
	if err != nil {
		t.Fatalf("impactReadFile: %v", err)
	}
	if !truncated {
		t.Fatal("expected impactReadFile to report the file as truncated")
	}
	if len(data) > impactReadLimit {
		t.Errorf("read %d bytes, cap is %d", len(data), impactReadLimit)
	}
	if len(data) != impactReadLimit {
		t.Errorf("read %d bytes, want exactly the cap %d", len(data), impactReadLimit)
	}

	impact := explainToolImpact("delete_file", impactRaw(t, map[string]any{"path": "huge.txt"}), root)
	if !strings.Contains(impact.Summary, "partial") || !strings.Contains(impact.Summary, "larger than") {
		t.Errorf("large-file delete summary does not disclose the cap: %s", impact.Summary)
	}

	impact = explainToolImpact("write_file", impactRaw(t, map[string]any{
		"file_path": "huge.txt", "old_string": "needle", "new_string": "x",
	}), root)
	if !strings.Contains(impact.Summary, "partial") && !strings.Contains(impact.Summary, "larger") {
		t.Errorf("large-file write summary does not disclose the cap: %s", impact.Summary)
	}
}

func TestExplainUnknownToolImpact(t *testing.T) {
	impact := explainToolImpact("mystery_tool", json.RawMessage(`{"x":1}`), "")
	if !strings.Contains(impact.Summary, "mystery_tool") {
		t.Errorf("unknown tool summary = %s", impact.Summary)
	}

	// The registered questionnaire tool name must take the read-only path,
	// not the generic unknown-tool fallback.
	impact = explainToolImpact("questions_tool", json.RawMessage(`{"questions":[]}`), "")
	if !strings.Contains(impact.Summary, "questions_tool") || !strings.Contains(impact.Summary, "Read-only") {
		t.Errorf("questions_tool summary = %s", impact.Summary)
	}
}

// TestExplainNeverMutates proves the explainer is read-only across every
// file-tool category.
func TestExplainNeverMutates(t *testing.T) {
	root := t.TempDir()
	srcPath := writeImpactFixture(t, root, "src.txt", "source\n")
	dstPath := writeImpactFixture(t, root, "dst.txt", "destination\n")
	delPath := writeImpactFixture(t, root, "del.txt", "delete me\n")

	before := map[string]string{}
	for _, p := range []string{srcPath, dstPath, delPath} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read before: %v", err)
		}
		before[p] = string(data)
	}

	_ = explainToolImpact("write_file", impactRaw(t, map[string]any{
		"file_path": "src.txt", "old_string": "source", "new_string": "changed",
	}), root)
	_ = explainToolImpact("edit_file", impactRaw(t, map[string]any{
		"file_path": "src.txt", "start_line": 1, "end_line": 1, "new_content": "changed",
	}), root)
	_ = explainToolImpact("create_file_tool", impactRaw(t, map[string]any{
		"path": "src.txt", "content": "changed",
	}), root)
	_ = explainToolImpact("move_file", impactRaw(t, map[string]any{
		"source_path": "src.txt", "destination_path": "dst.txt",
	}), root)
	_ = explainToolImpact("delete_file", impactRaw(t, map[string]any{"path": "del.txt"}), root)

	for p, want := range before {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read after %s: %v", p, err)
		}
		if string(data) != want {
			t.Errorf("explainer mutated %s: got %q, want %q", p, data, want)
		}
	}
}
