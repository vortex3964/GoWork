package test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	db "GoWork/Db"
	gowork "GoWork/internal/gowork"
	"GoWork/providers"
)

// assertValidToolContext fails when msgs is not a conversation a provider
// would accept: every assistant tool call must be answered by exactly one
// following tool result, before the next non-tool message, and no tool result
// may appear without a call.
func assertValidToolContext(t *testing.T, msgs []providers.Message) {
	t.Helper()

	var pending []string
	for i, msg := range msgs {
		switch msg.Role {
		case "assistant":
			if len(pending) > 0 {
				t.Fatalf("message %d (assistant): tool calls %v still have no results", i, pending)
			}
			for _, tc := range msg.ToolCalls {
				pending = append(pending, tc.Tool_call_id)
			}
		case "tool":
			match := -1
			for j, id := range pending {
				if id == msg.ToolCallID {
					match = j
					break
				}
			}
			if match < 0 {
				t.Fatalf("message %d: orphan tool result for call %q", i, msg.ToolCallID)
			}
			pending = append(pending[:match], pending[match+1:]...)
		default:
			if len(pending) > 0 {
				t.Fatalf("message %d (%s): tool calls %v still have no results", i, msg.Role, pending)
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("context ends with unanswered tool calls %v", pending)
	}
}

// TestMaxStepsClosesToolCalls covers the max-agent-steps guardrail: the
// assistant message with its tool calls is kept (prose and visible transcript
// stay intact), but every call is paired with a synthetic result explaining
// the limit, so the next request is valid instead of dangling.
func TestMaxStepsClosesToolCalls(t *testing.T) {
	m := newModeTestModel()
	m.MessageArea.SetSize(80, 24)
	m.Context = []providers.Message{{Role: "user", Content: "inspect the repo"}}
	m.SetMaxSteps(1)
	m.SetStepCount(1) // the next tool turn is step 2, over the cap
	m.MessageArea.LiveAssistantStart()

	calls := []providers.ToolCall{
		{Tool_call_id: "c1", Tool_name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)},
		{Tool_call_id: "c2", Tool_name: "grep_search", Input: json.RawMessage(`{"pattern":"TODO"}`)},
	}
	updated, _ := m.Update(gowork.StreamEnd(providers.GenerateResult{
		Content:   "Let me look at both files.",
		ToolCalls: calls,
	}, nil))
	got := updated.(gowork.Model)

	assertValidToolContext(t, got.Context)

	if len(got.Context) != 4 {
		t.Fatalf("context has %d messages, want 4 (user, assistant, 2 tool results): %+v", len(got.Context), got.Context)
	}
	if got.Context[1].Role != "assistant" || got.Context[1].Content != "Let me look at both files." {
		t.Fatalf("assistant prose was not preserved: %+v", got.Context[1])
	}
	if len(got.Context[1].ToolCalls) != 2 {
		t.Fatalf("assistant tool calls = %d, want 2", len(got.Context[1].ToolCalls))
	}
	for i, wantID := range []string{"c1", "c2"} {
		res := got.Context[2+i]
		if res.Role != "tool" || res.ToolCallID != wantID {
			t.Fatalf("result %d = %+v, want tool result for %q", i, res, wantID)
		}
		if !strings.Contains(res.Content, "maximum") || !strings.Contains(res.Content, "agent steps") {
			t.Fatalf("result %d does not explain the step limit: %q", i, res.Content)
		}
	}

	// The repaired context is already valid, so the safety net before the
	// next request must have nothing left to do.
	if _, changed := gowork.RepairContext(got.Context); changed {
		t.Fatal("max-steps context still needed repair before the next request")
	}
}

// TestEndTurnRepairsAbandonedToolBatch covers a batch abandoned while it was
// held up (for example a permission prompt the turn never got a decision for):
// ending the turn must repair the dangling call, not just reset loop state.
func TestEndTurnRepairsAbandonedToolBatch(t *testing.T) {
	m := newModeTestModel()
	m.Context = []providers.Message{
		{Role: "user", Content: "delete the build dir"},
		{Role: "assistant", Content: "Running that.", ToolCalls: []providers.ToolCall{
			{Tool_call_id: "c1", Tool_name: "bash"},
		}},
	}

	m.EndTurn()

	assertValidToolContext(t, m.Context)
	if len(m.Context) != 3 {
		t.Fatalf("context has %d messages, want 3 (user, assistant, synthetic result): %+v", len(m.Context), m.Context)
	}
	res := m.Context[2]
	if res.Role != "tool" || res.ToolCallID != "c1" || !strings.Contains(res.Content, "interrupted") {
		t.Fatalf("abandoned call was not repaired: %+v", res)
	}
}

// TestFlushSessionRepairsAbandonedToolBatch covers quitting mid-batch: some
// results may have arrived, but the rest never will. The save-on-exit path
// must persist a history with synthetic interrupted results for the missing
// calls, not the dangling assistant message.
func TestFlushSessionRepairsAbandonedToolBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("start session: %v", err)
	}

	m := newModeTestModel()
	m.Db = store
	m.Context = []providers.Message{
		{Role: "user", Content: "read both files"},
		{Role: "assistant", Content: "Reading both.", ToolCalls: []providers.ToolCall{
			{Tool_call_id: "c1", Tool_name: "read_file"},
			{Tool_call_id: "c2", Tool_name: "read_file"},
		}},
		// c1 finished, then the user quit while c2 was still pending.
		{Role: "tool", ToolCallID: "c1", Content: "a.go contents"},
	}
	m.FlushSession() // closes the store, like the real quit path

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	ls, err := reopened.LoadSession(1)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if ls == nil {
		t.Fatal("session 1 was not saved")
	}
	assertValidToolContext(t, ls.Messages)

	if len(ls.Messages) != 4 {
		t.Fatalf("saved %d messages, want 4 (user, assistant, real + synthetic result): %+v", len(ls.Messages), ls.Messages)
	}
	if got := ls.Messages[2]; got.ToolCallID != "c1" || got.Content != "a.go contents" {
		t.Fatalf("real result was not preserved: %+v", got)
	}
	if got := ls.Messages[3]; got.ToolCallID != "c2" || !strings.Contains(got.Content, "interrupted") {
		t.Fatalf("missing result for c2 was not repaired: %+v", got)
	}
}

// TestResumeSessionRepairsDanglingToolCalls covers loading a previously saved
// session whose context is already invalid: the restore must repair it before
// the first request uses it.
func TestResumeSessionRepairsDanglingToolCalls(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if err := store.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("start session: %v", err)
	}
	invalid := []providers.Message{
		{Role: "user", Content: "read a and b"},
		{Role: "assistant", Content: "Reading.", ToolCalls: []providers.ToolCall{
			{Tool_call_id: "c1", Tool_name: "read_file"},
			{Tool_call_id: "c2", Tool_name: "read_file"},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "a contents"},
		// c2 never ran: the app was killed mid-batch.
		{Role: "assistant", Content: "Still need c.", ToolCalls: []providers.ToolCall{
			{Tool_call_id: "c3", Tool_name: "read_file"},
		}},
	}
	if err := store.FinalizeSession(invalid, 0); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := store.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("start new session: %v", err)
	}

	m := newModeTestModel()
	m.Db = store
	m.ResumeSession(1)

	assertValidToolContext(t, m.Context)
	if len(m.Context) != 6 {
		t.Fatalf("restored context has %d messages, want 6 (2 synthetic results added): %+v", len(m.Context), m.Context)
	}
	for _, idx := range []int{3, 5} {
		if !strings.Contains(m.Context[idx].Content, "interrupted") {
			t.Fatalf("message %d is not a synthetic interrupted result: %+v", idx, m.Context[idx])
		}
	}
}

// TestRepairContextLeavesValidBatchUntouched makes sure the repair is not
// lossy for the normal tool flow: a well-formed batch comes back byte for
// byte and reports no change.
func TestRepairContextLeavesValidBatchUntouched(t *testing.T) {
	valid := []providers.Message{
		{Role: "user", Content: "read a and b"},
		{Role: "assistant", Content: "Reading both.", ToolCalls: []providers.ToolCall{
			{Tool_call_id: "c1", Tool_name: "read_file", Input: json.RawMessage(`{"path":"a"}`)},
			{Tool_call_id: "c2", Tool_name: "read_file", Input: json.RawMessage(`{"path":"b"}`)},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "a contents"},
		{Role: "tool", ToolCallID: "c2", Content: "b contents"},
		{Role: "assistant", Content: "Both files are fine."},
	}

	got, changed := gowork.RepairContext(valid)
	if changed {
		t.Fatal("valid tool batch was reported as repaired")
	}
	if !reflect.DeepEqual(got, valid) {
		t.Fatalf("valid tool batch changed:\n got %+v\nwant %+v", got, valid)
	}
}
