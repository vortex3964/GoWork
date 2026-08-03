//DESC: text-based tool call fallback for models that fail to emit native
//tool_calls (common with small local models). When such a model "calls" a
//tool, it often prints the call as a JSON blob in its text reply instead:
//
//	{"name": "create_file", "arguments": {"path": "...", "content": "..."}}
//
//This parses that shape back into a real ToolCall so the agentic loop can
//execute it, and only matches registered tool names so ordinary JSON answers
//aren't misread as tool calls.

package providers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// textCallSeq gives fabricated tool_call ids to text-parsed calls. It only
// needs to be unique within a session's context, and it's only touched from
// the (single-threaded) update loop.
var textCallSeq uint64

func nextTextCallID() string {
	textCallSeq++
	return fmt.Sprintf("txt-%d", textCallSeq)
}

// knownTool reports whether name is one of the tools registered via
// InitToolsDef - the gate that stops legitimate JSON answers (configs,
// data dumps, ...) from being executed as tool calls.
func knownTool(name string) bool {
	for _, td := range tools_def {
		if td.Name == name {
			return true
		}
	}
	return false
}

// ParseTextToolCalls scans content for JSON objects shaped like tool calls
// and returns them plus the content with those blobs removed (so the UI can
// show just the surrounding prose). It only fires for registered tool names.
func ParseTextToolCalls(content string) ([]ToolCall, string) {
	var calls []ToolCall
	var kept strings.Builder

	text := content
	for len(calls) < 8 {
		start := strings.IndexByte(text, '{')
		if start == -1 {
			break
		}
		end := matchBrace(text, start)
		if end == -1 {
			break
		}
		chunk := text[start : end+1]
		rest := text[end+1:]

		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Input     json.RawMessage `json:"input"`
		}
		valid := json.Valid([]byte(chunk)) &&
			json.Unmarshal([]byte(chunk), &call) == nil
		if valid {
			name := strings.TrimSpace(call.Name)
			valid = name != "" && knownTool(name)
		}

		if valid {
			// Keep the prose before the blob, drop the blob itself.
			kept.WriteString(text[:start])
			args := call.Input
			if len(args) == 0 {
				args = call.Arguments
			}
			calls = append(calls, ToolCall{
				Tool_call_id: nextTextCallID(),
				Tool_name:    strings.TrimSpace(call.Name),
				Input:        normalizeToolArgs(args),
			})
		} else {
			// Not a tool call - keep it verbatim.
			kept.WriteString(text[:end+1])
		}
		text = rest
	}
	kept.WriteString(text)

	if len(calls) == 0 {
		return nil, content
	}
	return calls, trimCodeFences(kept.String())
}

// matchBrace returns the index of the '}' that closes the object starting at
// start, honoring nesting and string literals, or -1.
func matchBrace(s string, start int) int {
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// trimCodeFences strips leftover ```json / ``` wrapper lines that remain
// after the JSON blob they surrounded has been removed.
func trimCodeFences(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// normalizeToolArgs cleans up malformed argument objects small models
// produce - most notably wrapping a plain string argument as
// {"type":"string","value":X} - unwrapping those back to bare strings.
func normalizeToolArgs(raw json.RawMessage) json.RawMessage {
	raw = rawArgs(string(raw))
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return json.RawMessage("{}")
	}
	out := make(map[string]json.RawMessage, len(obj))
	for k, v := range obj {
		out[k] = unwrapStringObject(v)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return b
}

// unwrapStringObject collapses {"type":"string","value":X} into a bare JSON
// string, leaving everything else untouched.
func unwrapStringObject(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 2 {
		return raw
	}
	var typ string
	if t, ok := obj["type"]; ok {
		_ = json.Unmarshal(t, &typ)
	}
	value, hasValue := obj["value"]
	if !hasValue || (typ != "string" && strings.TrimSpace(typ) != "") {
		return raw
	}
	var s string
	if json.Unmarshal(value, &s) == nil {
		b, _ := json.Marshal(s)
		return b
	}
	return value
}
