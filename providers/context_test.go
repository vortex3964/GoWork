package providers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// repeat returns s repeated n times (used to blow past token budgets).
func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}

func msgTokens(model string, msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateMessageTokensForModel(m, model)
	}
	return total
}

func TestTrimContextUnderBudgetIsUnchanged(t *testing.T) {
	InitToolsDef(nil)
	msgs := []Message{
		{Role: "user", Content: "the task"},
		{Role: "assistant", Content: "sure"},
	}
	got := TrimContext(msgs, 1<<20, "gpt-4o")
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
	got := TrimContext(msgs, 1024, "gpt-4o")
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
	total := msgTokens("gpt-4o", msgs)

	// Window sized so the allowance (window-overhead) fits exactly
	// first+tail, but not the full history - the middle tool group drops.
	overhead := EstimateOverheadTokensForModel("gpt-4o")
	wantKeep := EstimateMessageTokensForModel(first, "gpt-4o") + EstimateMessageTokensForModel(tail1, "gpt-4o") + EstimateMessageTokensForModel(tail2, "gpt-4o")
	window := overhead + wantKeep + 50
	allowance := window - overhead
	if allowance >= total || allowance < wantKeep {
		t.Fatalf("bad test window: allowance=%d must be < total=%d and >= wantKeep=%d", allowance, total, wantKeep)
	}

	got := TrimContext(msgs, window, "gpt-4o")
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

func TestShouldCompactThreshold(t *testing.T) {
	// Unknown window or zero usage never compacts.
	if ShouldCompact(0, 5000) {
		t.Error("ShouldCompact(0, 5000) = true, want false")
	}
	if ShouldCompact(100000, 0) {
		t.Error("ShouldCompact(100000, 0) = true, want false")
	}
	// Large window: 20k reserve - compact only past window-20000.
	if ShouldCompact(100000, 79000) {
		t.Error("ShouldCompact(100000, 79000) = true, want false (still under reserve)")
	}
	if !ShouldCompact(100000, 81000) {
		t.Error("ShouldCompact(100000, 81000) = false, want true")
	}
	// Small window: reserve never drops below 4k, so 8k ctx compacts at 4k.
	if ShouldCompact(8192, 3900) {
		t.Error("ShouldCompact(8192, 3900) = true, want false")
	}
	if !ShouldCompact(8192, 4100) {
		t.Error("ShouldCompact(8192, 4100) = false, want true")
	}
}

func TestCompactContextKeepsRecentTurns(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "old task 1"},
		{Role: "assistant", Content: "old answer 1"},
		{Role: "user", Content: "old task 2"},
		{Role: "assistant", Content: "old answer 2"},
		{Role: "user", Content: "recent task"},
		{Role: "assistant", Content: "recent answer"},
	}
	got := CompactContext(msgs, "SUMMARY HERE", 1)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3 (summary + last turn): %+v", len(got), got)
	}
	if got[0].Role != "user" || !strings.Contains(got[0].Content, "SUMMARY HERE") {
		t.Errorf("first message should carry the summary, got %+v", got[0])
	}
	if got[1].Content != "recent task" || got[2].Content != "recent answer" {
		t.Errorf("recent turn lost: %+v", got[1:])
	}
}

func TestCompactContextDefaultKeepsTwoTurns(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", Content: "answer 1"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", Content: "answer 2"},
		{Role: "user", Content: "turn 3"},
		{Role: "assistant", Content: "answer 3"},
	}
	got := CompactContext(msgs, "S", 0)
	if len(got) != 5 {
		t.Fatalf("got %d messages, want 5 (summary + last 2 turns)", len(got))
	}
	if got[1].Content != "turn 2" || got[4].Content != "answer 3" {
		t.Errorf("last two turns not preserved: %+v", got[1:])
	}
}

func TestCompactContextKeepsToolGroupsTogether(t *testing.T) {
	call := Message{
		Role:      "assistant",
		ToolCalls: []ToolCall{{Tool_call_id: "c1", Tool_name: "read_file", Input: []byte(`{"path":"x"}`)}},
	}
	res := Message{Role: "tool", ToolCallID: "c1", Content: "result"}
	// 3 user turns: compaction keeps the last 2 verbatim, dropping the first.
	msgs := []Message{
		{Role: "user", Content: "drop me"},
		{Role: "assistant", Content: "dropped"},
		{Role: "user", Content: "task"},
		{Role: "assistant", Content: "answer"},
		call,
		res,
		{Role: "user", Content: "newest"},
	}
	got := CompactContext(msgs, "S", 2)
	// summary + [task,answer,call,res] + newest = 6 messages
	if len(got) != 6 {
		t.Fatalf("got %d messages, want 6: %+v", len(got), got)
	}
	if got[0].Role != "user" || !strings.Contains(got[0].Content, "S") {
		t.Errorf("first message should carry the summary, got %+v", got[0])
	}
	if strings.Contains(got[0].Content, "drop me") || strings.Contains(got[0].Content, "dropped") {
		t.Errorf("oldest turn should be replaced by the summary")
	}
	// The tool call and its result must stay glued together mid-history:
	// call at index 3, its tool result at index 4, newest user at 5.
	if got[3].Role != "assistant" || len(got[3].ToolCalls) == 0 || got[3].ToolCalls[0].Tool_call_id != "c1" {
		t.Errorf("tool call lost: %+v", got[3])
	}
	if got[4].Role != "tool" || got[4].ToolCallID != "c1" {
		t.Errorf("tool result lost: %+v", got[4])
	}
	if got[5].Role != "user" || got[5].Content != "newest" {
		t.Errorf("newest turn lost: %+v", got[5])
	}
}

func TestCompactContextEmptySummaryIsNoOp(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
	got := CompactContext(msgs, "  ", 2)
	if len(got) != 2 || got[0].Content != "a" {
		t.Errorf("empty summary should leave messages untouched, got %+v", got)
	}
}

func TestWithoutSystemOmitsPromptAndTools(t *testing.T) {
	InitSystemPrompt("SYSTEM PROMPT")
	InitToolsDef([]ToolDef{{Name: "read_file", Description: "reads", InputSchema: json.RawMessage(`{"type":"object"}`)}})
	defer InitSystemPrompt("")
	defer InitToolsDef(nil)

	msgs := []Message{{Role: "user", Content: "hi"}}
	full := openAICompatMessages(context.Background(), msgs)
	if len(full) != 2 || full[0]["role"] != "system" {
		t.Fatalf("expected system prompt on normal requests, got %v", full)
	}
	compact := openAICompatMessages(WithoutSystem(context.Background()), msgs)
	if len(compact) != 1 {
		t.Fatalf("expected no system message on compaction requests, got %v", compact)
	}
	if tools := openAICompatTools(context.Background()); len(tools) != 1 {
		t.Fatalf("expected tools on normal requests, got %v", tools)
	}
	if tools := openAICompatTools(WithoutSystem(context.Background())); len(tools) != 0 {
		t.Fatalf("expected no tools on compaction requests, got %v", tools)
	}
}

func TestCompactionMessagesTruncatesToolOutput(t *testing.T) {
	InitToolsDef(nil)
	InitCompactionPrompt("Condense this.")
	big := strings.Repeat("x", compactToolOutputMax*2)
	msgs := []Message{
		{Role: "user", Content: "task"},
		{Role: "assistant", ToolCalls: []ToolCall{{Tool_call_id: "c1", Tool_name: "read_file"}}},
		{Role: "tool", ToolCallID: "c1", Content: big},
	}
	got := CompactionMessages(msgs, 1<<20)
	last := got[len(got)-1]
	if last.Role != "user" || last.Content != "Condense this." {
		t.Errorf("compaction prompt not appended as final user message: %+v", last)
	}
	for _, m := range got {
		if m.Role == "tool" && len(m.Content) > compactToolOutputMax+64 {
			t.Errorf("tool output not truncated: %d chars", len(m.Content))
		}
	}
}
