package gowork

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"GoWork/providers"
	"GoWork/tools"
)

// loopProvider is a silent provider: its stream ends immediately so the test
// can drive each generation with an explicit streamEndMsg.
type loopProvider struct{}

func (loopProvider) Generate(context.Context, []providers.Message) (providers.GenerateResult, error) {
	return providers.GenerateResult{}, nil
}

func (loopProvider) GenerateStream(context.Context, []providers.Message, providers.StreamFunc) (providers.GenerateResult, error) {
	return providers.GenerateResult{}, nil
}

func (loopProvider) Info(context.Context, string) (providers.ModelInfo, error) {
	return providers.ModelInfo{}, nil
}

func (loopProvider) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}

// TestAgentLoopHasNoTurnCap drives more tool batches than the old 40-step
// limit and checks the loop keeps calling the model instead of ending the
// turn. It also checks the history stays valid after every batch, so an
// uncapped loop can never send a provider request with an unfinished
// assistant tool_calls sequence.
func TestAgentLoopHasNoTurnCap(t *testing.T) {
	m := newQA76Model(t)
	think := true
	m.AiThink = &think
	m.model = &tools.ProviderHolder{}
	m.model.Set(loopProvider{})
	m.Context = []providers.Message{{Role: "user", Content: "keep going"}}

	const batches = 45 // more than the old 40-step cap
	for i := 0; i < batches; i++ {
		call := providers.ToolCall{
			Tool_call_id: fmt.Sprintf("c%d", i),
			Tool_name:    "read_file",
			Input:        json.RawMessage(`{"path":"a.go"}`),
		}
		updated, _ := m.Update(streamEndMsg{result: providers.GenerateResult{
			Content:   fmt.Sprintf("step %d", i),
			ToolCalls: []providers.ToolCall{call},
		}})
		m = updated.(model)

		if !*m.AiThink {
			t.Fatalf("turn ended after %d tool batches; the loop must not cap turns", i)
		}
		if m.stopRequested {
			t.Fatalf("batch %d stopped without user interruption", i)
		}
		if len(m.pendingTools) != 1 {
			t.Fatalf("batch %d has %d pending tools, want 1", i, len(m.pendingTools))
		}

		updated, _ = m.Update(toolResultMsg{index: 0, result: tools.Ok("ok")})
		m = updated.(model)

		// The result has landed and loopAfterBatch has called the model
		// again: by this point the history must be safe to send.
		if repaired, changed := repairContext(m.Context); changed {
			t.Fatalf("batch %d left an unfinished tool call: %+v", i, repaired)
		}
	}

	want := 1 + 2*batches // user + (assistant tool call + result) per batch
	if len(m.Context) != want {
		t.Fatalf("context has %d messages after %d batches, want %d", len(m.Context), batches, want)
	}
	if repaired, changed := repairContext(m.Context); changed {
		t.Fatalf("final context needed repair: %+v", repaired)
	}
}

// TestCtrlIMidBatchKeepsContextValid interrupts a running tool batch and then
// lets the cancelled calls report back. The turn must stop, and the history
// must still pair every assistant tool call with exactly one result - no
// provider request may see an unfinished tool_calls sequence.
func TestCtrlIMidBatchKeepsContextValid(t *testing.T) {
	m := newQA76Model(t)
	think := true
	m.AiThink = &think
	m.Context = []providers.Message{{Role: "user", Content: "run both"}}
	m.MessageArea.LiveAssistantStart()

	updated, _ := m.Update(streamEndMsg{result: providers.GenerateResult{
		Content: "Running both.",
		ToolCalls: []providers.ToolCall{
			{Tool_call_id: "c1", Tool_name: "read_file"},
			{Tool_call_id: "c2", Tool_name: "read_file"},
		},
	}})
	m = updated.(model)
	if len(m.pendingTools) != 2 {
		t.Fatalf("pending tools = %d, want 2", len(m.pendingTools))
	}

	// ctrl+i needs a live cancel func to count as an interrupt.
	m.cancel = func() {}
	updated, _ = m.Update(ctrlKey('i'))
	m = updated.(model)
	if !m.stopRequested {
		t.Fatal("ctrl+i did not mark the turn as stopped")
	}

	// Both calls report back (with cancellation errors); the loop must close
	// the batch, then honour the stop instead of calling the model again.
	updated, _ = m.Update(toolResultMsg{index: 0, result: tools.Errf("cancelled")})
	m = updated.(model)
	updated, _ = m.Update(toolResultMsg{index: 1, result: tools.Errf("cancelled")})
	m = updated.(model)

	if *m.AiThink {
		t.Fatal("turn kept running after ctrl+i")
	}
	if len(m.pendingTools) != 0 || len(m.pendingResults) != 0 {
		t.Fatalf("batch state survived the stop: %d tools, %d results", len(m.pendingTools), len(m.pendingResults))
	}
	if repaired, changed := repairContext(m.Context); changed {
		t.Fatalf("cancelled batch left an unfinished tool call: %+v", repaired)
	}
	if len(m.Context) != 4 {
		t.Fatalf("context has %d messages, want 4 (user, assistant, 2 results): %+v", len(m.Context), m.Context)
	}
}
