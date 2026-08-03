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

// TestParseTextToolCallsMalformed covers the messy JSON small local models
// actually emit - the whole reason the parser has a forgiving fallback. In
// every case a recognized call must still be produced (spilling into an
// error the model can react to) rather than silently dropped.
func TestParseTextToolCallsMalformed(t *testing.T) {
	InitToolsDef([]ToolDef{
		{Name: "create_file"},
		{Name: "read_file"},
		{Name: "edit_file"},
		{Name: "delete_file"},
		{Name: "write_file"},
	})

	cases := []struct {
		name       string
		content    string
		wantName   string
		wantPath   string
	}{
		{
			name:     "unclosed backtick string value",
			content:  "```json\n{\n  \"name\": \"edit_file\",\n  \"arguments\": {\n    \"file_path\": \"main.go\",\n    \"start_line\": 3,\n    \"new_content\": `\n      type Cache struct {\n        items map[string]string\n      }\n      \n      func main() {}\n  }\n}\n```",
			wantName: "edit_file",
			wantPath: "main.go",
		},
		{
			name:     "closed backtick string value",
			content:  "{\n  \"name\": \"write_file\",\n  \"arguments\": {\n    \"path\": \"a.txt\",\n    \"content\": `\nhello world\n`\n  }\n}",
			wantName: "write_file",
			wantPath: "a.txt",
		},
		{
			name:     "trailing comma before closing brace",
			content:  "{\n  \"name\": \"delete_file\",\n  \"arguments\": {\"path\": \"tmp.go\",}\n}",
			wantName: "delete_file",
			wantPath: "tmp.go",
		},
		{
			name:     "clean valid call still works",
			content:  "{\n  \"name\": \"read_file\",\n  \"arguments\": {\"path\": \"main.go\"}\n}",
			wantName: "read_file",
			wantPath: "main.go",
		},
	}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				calls, _ := ParseTextToolCalls(c.content)
				if len(calls) != 1 {
					t.Fatalf("got %d calls, want 1", len(calls))
				}
				if calls[0].Tool_name != c.wantName {
					t.Errorf("call name = %q, want %q", calls[0].Tool_name, c.wantName)
				}

				var argObj map[string]json.RawMessage
				if err := json.Unmarshal(calls[0].Input, &argObj); err != nil {
					t.Fatalf("Input isn't a JSON object: %v (%s)", err, calls[0].Input)
				}
				raw, ok := argObj["path"]
				if !ok {
					raw, ok = argObj["file_path"]
				}
				if !ok {
					t.Fatalf("Input missing path/file_path: %s", calls[0].Input)
				}
				var p string
				if err := json.Unmarshal(raw, &p); err != nil {
					t.Fatalf("path value isn't a string: %v (%s)", err, raw)
				}
				if p != c.wantPath {
					t.Errorf("path = %q, want %q", p, c.wantPath)
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
