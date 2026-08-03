package messagearea_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	messagearea "GoWork/Tui/Components/MessageArea"
)

// stripANSI removes terminal escape sequences so assertions can match the
// literal, human-readable text of a rendered view.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestSummarizeToolInput(t *testing.T) {
	cases := []struct {
		name  string
		input json.RawMessage
		want  string
	}{
		{"file path preferred", json.RawMessage(`{"file_path":"src/foo.go","start_line":1}`), "src/foo.go"},
		{"path preferred", json.RawMessage(`{"path":"README.md","offset_lines":10}`), "README.md"},
		{"url preferred", json.RawMessage(`{"url":"https://example.com","prompt":"docs"}`), "https://example.com"},
		{"first string fallback", json.RawMessage(`{"pattern":"foo","dir":"src"}`), "dir=src"},
		{"empty object", json.RawMessage(`{}`), ""},
		{"nil input", nil, ""},
		{"invalid json", json.RawMessage(`nope`), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := messagearea.SummarizeToolInput(c.input); got != c.want {
				t.Errorf("SummarizeToolInput(%s) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestToolMessageLifecycle(t *testing.T) {
	m := messagearea.New()
	m.SetSize(120, 30)
	idx := m.AppendTool("read_file", "main.go")
	if idx != 0 {
		t.Fatalf("first tool message index = %d, want 0", idx)
	}

	// running state header
	run := stripANSI(m.View())
	if !strings.Contains(run, "● read_file main.go") {
		t.Errorf("running tool message missing header, got:\n%s", run)
	}

	// done state shows a check mark and the truncated result
	m.UpdateToolMessage(idx, messagearea.ToolDone, "1. line one\n2. line two")
	done := stripANSI(m.View())
	if !strings.Contains(done, "✓ read_file main.go") {
		t.Errorf("done tool message missing check header, got:\n%s", done)
	}
	if !strings.Contains(done, "line one") {
		t.Errorf("done tool message missing result body, got:\n%s", done)
	}

	// error state shows an x and the error text
	m.UpdateToolMessage(idx, messagearea.ToolError, "boom")
	errView := stripANSI(m.View())
	if !strings.Contains(errView, "× read_file main.go") {
		t.Errorf("error tool message missing x header, got:\n%s", errView)
	}
	if !strings.Contains(errView, "boom") {
		t.Errorf("error tool message missing error body, got:\n%s", errView)
	}

	// out-of-range updates are ignored, not crashes
	m.UpdateToolMessage(99, messagearea.ToolDone, "nope")
	m.UpdateToolMessage(-1, messagearea.ToolDone, "nope")
}
