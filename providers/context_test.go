package providers

import (
	"strings"
	"testing"
)

// repeat returns s repeated n times (used to blow past token budgets).
func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}

func msgTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateMessageTokens(m)
	}
	return total
}

func TestTrimContextUnderBudgetIsUnchanged(t *testing.T) {
	InitToolsDef(nil)
	msgs := []Message{
		{Role: "user", Content: "the task"},
		{Role: "assistant", Content: "sure"},
	}
	got := TrimContext(msgs, 1<<20)
	if len(got) != len(msgs) {
		t.Fatalf("got %d messages, want %d", len(got), len(msgs))
	}
	for i := range msgs {
		if got[i].Content != msgs[i].Content {
			t.Errorf("message %d changed", i)
		}
	}
}

func TestTrimContextAlwaysKeepsFirstUserMessage(t *testing.T) {
	InitToolsDef(nil)
	msgs := []Message{
		{Role: "user", Content: repeat("x", 10000)}, // ~2500 tokens
		{Role: "assistant", Content: repeat("y", 10000)},
		{Role: "user", Content: repeat("z", 10000)},
	}
	got := TrimContext(msgs, 1024)
	if len(got) != 1 {
		t.Fatalf("expected just the first message, got %d: %+v", len(got), got)
	}
	if got[0].Content != msgs[0].Content {
		t.Errorf("first message (the task) was dropped")
	}
}

func TestTrimContextKeepsToolGroupsIntact(t *testing.T) {
	InitToolsDef(nil)
	first := Message{Role: "user", Content: repeat("a", 2000)} // ~516 tokens
	// tool exchange: assistant call + tool result (must never be split)
	callMsg := Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: []ToolCall{{Tool_call_id: "a1", Tool_name: "read_file", Input: []byte(`{"path":"x"}`)}},
	}
	toolMsg := Message{Role: "tool", ToolCallID: "a1", Content: repeat("b", 2000)} // ~516 tokens
	// recent messages to keep at the tail
	tail1 := Message{Role: "assistant", Content: repeat("c", 2000)}
	tail2 := Message{Role: "user", Content: repeat("d", 2000)}

	msgs := []Message{first, callMsg, toolMsg, tail1, tail2}
	total := msgTokens(msgs)

	// Window sized so the allowance (window-overhead) fits exactly
	// first+tail, but not the full history - the middle tool group drops.
	overhead := EstimateOverheadTokens()
	wantKeep := EstimateMessageTokens(first) + EstimateMessageTokens(tail1) + EstimateMessageTokens(tail2)
	window := overhead + wantKeep + 50
	allowance := window - overhead
	if allowance >= total || allowance < wantKeep {
		t.Fatalf("bad test window: allowance=%d must be < total=%d and >= wantKeep=%d", allowance, total, wantKeep)
	}

	got := TrimContext(msgs, window)
	// The tool group must either be fully present or fully absent - never
	// a tool message without its assistant call.
	sawTool := false
	for _, m := range got {
		if m.Role == "tool" {
			sawTool = true
		}
		if m.Role == "tool" && m.ToolCallID != "a1" {
			t.Errorf("dangling tool message without its call: %+v", m)
		}
	}
	hasCall := false
	for _, m := range got {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			hasCall = true
		}
	}
	if sawTool != hasCall {
		t.Errorf("tool group split: sawTool=%v hasCall=%v", sawTool, hasCall)
	}
	// First message always present.
	if got[0].Content != first.Content {
		t.Errorf("first message dropped")
	}
}
