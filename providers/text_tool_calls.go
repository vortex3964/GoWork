//DESC: text-based tool call fallback for models that fail to emit native
//tool_calls (common with small local models). When such a model "calls" a
//tool, it often prints the call as a JSON blob in its text reply instead:
//
//	{"name": "create_file", "arguments": {"path": "...", "content": "..."}}
//
//This parses that shape back into a real ToolCall so the agentic loop can
//execute it, and only matches registered tool names so ordinary JSON answers
//aren't misread as tool calls.
//
//Small models rarely produce clean JSON here - they wrap string values in
//backticks, forget to close strings, leave trailing commas, etc. This parser
//is deliberately forgiving: once it sees a recognized tool name it makes a
//best-effort call rather than dropping it, so the agentic loop stays alive
//and the model can react to the tool's output (or error) on the next turn.

package providers

import (
	"encoding/json"
	"fmt"
	"regexp"
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

		if call, ok := parseCallChunk(chunk); ok {
			// Keep the prose before the blob, drop the blob itself.
			kept.WriteString(text[:start])
			calls = append(calls, call)
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

// parseCallChunk turns a single candidate JSON blob into a ToolCall. It
// tries a strict parse first; when that fails it falls back to a forgiving
// extraction (backtick strings, missing commas, truncated objects) so a
// recognizable tool call is still executed instead of dropped.
func parseCallChunk(chunk string) (ToolCall, bool) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
	}
	if json.Valid([]byte(chunk)) && json.Unmarshal([]byte(chunk), &call) == nil {
		name := strings.TrimSpace(call.Name)
		if name != "" && knownTool(name) {
			args := call.Input
			if len(args) == 0 {
				args = call.Arguments
			}
			return ToolCall{
				Tool_call_id: nextTextCallID(),
				Tool_name:    name,
				Input:        normalizeToolArgs(args),
			}, true
		}
	}

	name := extractName(chunk)
	if name == "" || !knownTool(name) {
		return ToolCall{}, false
	}
	return ToolCall{
		Tool_call_id: nextTextCallID(),
		Tool_name:    name,
		Input:        normalizeToolArgs(extractArguments(chunk)),
	}, true
}

var (
	nameRe        = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
	argumentsKeyRe = regexp.MustCompile(`"(arguments|input)"\s*:`)
)

// extractName pulls the tool name out of a (possibly malformed) call blob.
func extractName(chunk string) string {
	m := nameRe.FindStringSubmatch(chunk)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// extractArguments best-effort-extracts the arguments object from a call
// blob. It locates the "arguments"/"input" key and grabs its brace-balanced
// region, then repairs common malformations. If nothing usable comes out it
// returns {} - the call still executes (and the tool reports the error the
// model can react to) rather than being silently lost.
func extractArguments(chunk string) json.RawMessage {
	m := argumentsKeyRe.FindStringSubmatchIndex(chunk)
	if len(m) != 4 {
		return json.RawMessage("{}")
	}
	open := strings.IndexByte(chunk[m[3]:], '{')
	if open == -1 {
		return json.RawMessage("{}")
	}
	open += m[3]

	end := -1
	if strings.ContainsRune(chunk[m[3]:], '`') {
		// Backtick content can contain quotes/braces that fool matchBrace;
		// grab down to the last '}' instead and let repair rebalance it.
		end = strings.LastIndexByte(chunk, '}')
	} else {
		end = matchBrace(chunk, open)
		if end == -1 {
			end = strings.LastIndexByte(chunk, '}')
		}
	}
	if end < open {
		return json.RawMessage("{}")
	}

	raw := chunk[open : end+1]
	for _, candidate := range []string{
		raw,
		repairBacktickStrings(raw),
		removeTrailingCommas(repairBacktickStrings(raw)),
	} {
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate)
		}
		// Repair may have restored brace balance (e.g. unclosed backtick
		// strings now quote their content) - re-clip to the real end.
		if e := matchBrace(candidate, 0); e != -1 && e != len(candidate)-1 {
			if json.Valid([]byte(candidate[:e+1])) {
				return json.RawMessage(candidate[:e+1])
			}
		}
	}
	return json.RawMessage("{}")
}

// repairBacktickStrings rewrites backtick-delimited string values (a very
// common local-model habit: `multi line\n text` instead of "...") into
// properly quoted JSON strings. Closed pairs are rewritten in place; an
// unclosed trailing backtick is closed just before the last line-starting
// '}' so the object's closing braces survive.
func repairBacktickStrings(s string) string {
	var out strings.Builder
	i := 0
	inStr := false
	for i < len(s) {
		c := s[i]
		if inStr {
			if c == '"' && !escapedAt(s, i) {
				inStr = false
			}
			out.WriteByte(c)
			i++
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			i++
			continue
		}
		if c != '`' {
			out.WriteByte(c)
			i++
			continue
		}
		// Backtick opens a template string: collect to the next backtick.
		end := -1
		for j := i + 1; j < len(s); j++ {
			if s[j] == '`' && !escapedAt(s, j) {
				end = j
				break
			}
		}
		if end == -1 {
			// Unclosed: close before the last line-starting '}' (the object
			// it belongs to is typically terminated by one or more of them).
			closeAt := lastLineClosingBrace(s, i+1)
			if closeAt == -1 {
				closeAt = len(s)
			}
			out.WriteString(strconvQuote(s[i+1 : closeAt]))
			// closeAt points at a structural '}' - start again there so it
			// (and whatever closes the object) is copied, not swallowed.
			i = closeAt
			continue
		}
		out.WriteString(strconvQuote(s[i+1 : end]))
		i = end + 1
	}
	return out.String()
}

var lineClosingBraceRe = regexp.MustCompile(`\n[ \t]*\}`)

// lastLineClosingBrace returns the index of the '}' to close an unclosed
// backtick string before. The arguments object region typically ends with the
// arguments' closing brace then the root object's closing brace, so the string
// should swallow everything up to the second-to-last line-starting '}',
// leaving at least the root close outside. Falls back to the last one.
func lastLineClosingBrace(s string, start int) int {
	locs := lineClosingBraceRe.FindAllStringIndex(s[start:], -1)
	if len(locs) == 0 {
		return -1
	}
	idx := len(locs) - 2
	if idx < 0 {
		idx = 0
	}
	return start + locs[idx][0] + 1
}

// escapedAt reports whether the character at i is preceded by an odd number
// of backslashes (i.e. it's escaped).
func escapedAt(s string, i int) bool {
	bs := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		bs++
	}
	return bs%2 == 1
}

// strconvQuote JSON-escapes a raw string (the content of a backtick value).
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// removeTrailingCommas strips commas that precede a closing brace/bracket
// while respecting quoted strings, so `"a":1,}` parses.
func removeTrailingCommas(s string) string {
	var out strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			out.WriteByte(c)
			if c == '"' && !escapedAt(s, i) {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue // drop the comma
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

// matchBrace returns the index of the '}' that closes the object starting at
// start, honoring nesting and string literals, or -1.
func matchBrace(s string, start int) int {
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '"' && !escapedAt(s, i) {
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
