package providers

import (
	"encoding/json"
	"testing"
)

func TestParseTextToolCalls(t *testing.T) {
	InitToolsDef([]ToolDef{
		{Name: "create_file"},
		{Name: "read_file"},
		{Name: "edit_file"},
	})

	cases := []struct {
		name        string
		content     string
		wantCalls   int
		wantName    string
		wantClean   string
	}{
		{
			name: "plain fenced json blob (the qwen2.5-coder:3b case)",
			content: "```json\n{\n  \"name\": \"create_file\",\n  \"arguments\": {\n    \"path\": \"src/hello.c\",\n    \"content\": {\"type\": \"string\", \"value\": \"#include <stdio.h>\"}\n  }\n}\n```",
			wantCalls: 1,
			wantName:  "create_file",
			wantClean: "",
		},
		{
			name:      "prose then json then prose",
			content:   "I'll create it:\n{\"name\": \"create_file\", \"arguments\": {\"path\": \"a.c\", \"content\": \"x\"}}\nDone.",
			wantCalls: 1,
			wantName:  "create_file",
			wantClean: "I'll create it:\n\nDone.",
		},
		{
			name:      "input alias",
			content:   `{"name": "read_file", "input": {"path": "main.go"}}`,
			wantCalls: 1,
			wantName:  "read_file",
			wantClean: "",
		},
		{
			name:      "unknown tool is not a call",
			content:   `{"name": "not_a_real_tool", "arguments": {"path": "x"}}`,
			wantCalls: 0,
			wantName:  "",
			wantClean: `{"name": "not_a_real_tool", "arguments": {"path": "x"}}`,
		},
		{
			name:      "legit json answer untouched",
			content:   `{"config": {"name": "app", "arguments": []}}`,
			wantCalls: 0,
			wantName:  "",
			wantClean: `{"config": {"name": "app", "arguments": []}}`,
		},
		{
			name:      "plain text no json",
			content:   "hello world",
			wantCalls: 0,
			wantName:  "",
			wantClean: "hello world",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls, clean := ParseTextToolCalls(c.content)
			if len(calls) != c.wantCalls {
				t.Fatalf("got %d calls, want %d (clean=%q)", len(calls), c.wantCalls, clean)
			}
			if c.wantCalls > 0 {
				if calls[0].Tool_name != c.wantName {
					t.Errorf("call name = %q, want %q", calls[0].Tool_name, c.wantName)
				}
				if calls[0].Tool_call_id == "" {
					t.Error("fabricated call id is empty")
				}
			}
			if clean != c.wantClean {
				t.Errorf("cleaned content = %q, want %q", clean, c.wantClean)
			}
		})
	}
}

func TestNormalizeToolArgsUnwrapsStringObject(t *testing.T) {
	got := normalizeToolArgs(json.RawMessage(`{"path":"a.c","content":{"type":"string","value":"#include <stdio.h>"}}`))
	var gotObj, wantObj map[string]string
	if err := json.Unmarshal(got, &gotObj); err != nil {
		t.Fatalf("normalized output is not a string map: %v (%s)", err, got)
	}
	want := `{"content":"#include <stdio.h>","path":"a.c"}`
	if err := json.Unmarshal([]byte(want), &wantObj); err != nil {
		t.Fatalf("bad test want: %v", err)
	}
	if gotObj["content"] != wantObj["content"] || gotObj["path"] != wantObj["path"] {
		t.Errorf("normalized = %s, want %s", got, want)
	}
	if len(gotObj) != 2 {
		t.Errorf("normalized has %d keys, want 2: %s", len(gotObj), got)
	}
}
