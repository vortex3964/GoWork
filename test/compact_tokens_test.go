package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	gowork "GoWork/internal/gowork"
	"GoWork/providers"
)

// compactionProvider returns a fixed summary + provider-reported usage, like a
// real compaction call would.
type compactionProvider struct {
	summary string
	usage   providers.Usage
	err     error
}

func (p compactionProvider) Generate(context.Context, []providers.Message) (providers.GenerateResult, error) {
	if p.err != nil {
		return providers.GenerateResult{}, p.err
	}
	return providers.GenerateResult{Content: p.summary, Usage: p.usage}, nil
}

func (p compactionProvider) GenerateStream(ctx context.Context, msgs []providers.Message, onDelta providers.StreamFunc) (providers.GenerateResult, error) {
	res, err := p.Generate(ctx, msgs)
	if err == nil && res.Content != "" {
		onDelta(res.Content)
	}
	return res, err
}

func (compactionProvider) Info(context.Context, string) (providers.ModelInfo, error) {
	return providers.ModelInfo{}, nil
}

func (compactionProvider) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}

// TestCompactionReusesProviderPromptTokens checks the post-compaction token
// accounting end to end: the statusline/compaction trigger must come from the
// compaction call's provider-reported summary usage plus a cheap tail
// estimate, never from recounting the (much larger) compressed transcript.
func TestCompactionReusesProviderPromptTokens(t *testing.T) {
	const (
		model  = "gpt-4o"
		window = 100000
	)
	m := newModeTestModel()
	m.SetContextWindow(window)
	m.MessageArea.SetSize(80, 24)

	// Two old turns dominate the history; the last two user turns are kept
	// verbatim by compaction.
	big := strings.Repeat("x", 20000)
	m.Context = []providers.Message{
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: "recent task"},
		{Role: "assistant", Content: "recent answer"},
		{Role: "user", Content: "newest task"},
		{Role: "assistant", Content: "newest answer"},
	}
	// The provider's summary body is enormous, but its reported completion
	// usage says it condenses to 25 tokens. A local recount of the compacted
	// transcript would count those 400k characters; reusing the provider
	// usage must not.
	summary := strings.Repeat("s", 400000)
	usage := providers.Usage{PromptTokens: 99000, CompletionTokens: 25, TotalTokens: 99025}
	msg := gowork.RunCompaction(compactionProvider{summary: summary, usage: usage}, m.Context, window, model)

	updated, _ := m.Update(msg)
	got := updated.(gowork.Model)

	if len(got.Context) != 5 {
		t.Fatalf("compacted context has %d messages, want 5 (summary + 4 retained)", len(got.Context))
	}
	if !strings.Contains(got.Context[0].Content, "summary") {
		t.Fatalf("first context message is not the summary")
	}

	// The summary part is exact (provider-reported); the retained tail is
	// tiny, so the whole estimate stays near the provider's 25 tokens
	// regardless of which tokenizer happens to be loaded.
	if got.PromptTokens() < usage.CompletionTokens {
		t.Fatalf("prompt tokens = %d, want at least the provider summary usage %d", got.PromptTokens(), usage.CompletionTokens)
	}
	if got.PromptTokens() > 5000 {
		t.Fatalf("prompt tokens = %d looks like a full recount of the compressed history", got.PromptTokens())
	}
	// The compaction request itself was ~99k tokens; that describes the
	// pre-summary history, not the new context, so it must not leak into
	// the statusline or immediately re-trigger compaction.
	if got.PromptTokens() >= usage.PromptTokens {
		t.Fatalf("prompt tokens = %d reused the pre-summary request size %d", got.PromptTokens(), usage.PromptTokens)
	}
	if got.ShouldCompact() {
		t.Fatalf("compacted context still reports shouldCompact at %d/%d tokens", got.PromptTokens(), window)
	}
	if got.SessionTokens() != usage.TotalTokens {
		t.Fatalf("session tokens = %d, want %d", got.SessionTokens(), usage.TotalTokens)
	}

	status := got.StatusLineText()
	if strings.Contains(status, "85% ctx") || strings.Contains(status, "99% ctx") {
		t.Fatalf("statusline kept the pre-compaction fill: %q", status)
	}
	wantPct := got.PromptTokens() * 100 / window
	if !strings.Contains(status, fmt.Sprintf("%d%% ctx", wantPct)) {
		t.Fatalf("statusline = %q, want %d%% ctx", status, wantPct)
	}
}

// TestCompactionNoOpKeepsPreviousAccounting covers a summary that cannot
// replace anything (the whole history is recent): the context and the last
// known prompt-token count stay as they were.
func TestCompactionNoOpKeepsPreviousAccounting(t *testing.T) {
	const window = 100000
	m := newModeTestModel()
	m.SetContextWindow(window)
	m.MessageArea.SetSize(80, 24)
	m.Context = []providers.Message{
		{Role: "user", Content: "only task"},
		{Role: "assistant", Content: "only answer"},
	}
	before := providers.EstimateContextTokens(m.Context, "gpt-4o")
	msg := gowork.RunCompaction(compactionProvider{
		summary: "summary",
		usage:   providers.Usage{PromptTokens: before, CompletionTokens: 5, TotalTokens: before + 5},
	}, m.Context, window, "gpt-4o")

	updated, _ := m.Update(msg)
	got := updated.(gowork.Model)

	if len(got.Context) != len(m.Context) {
		t.Fatalf("no-op compaction changed the context: %d -> %d messages", len(m.Context), len(got.Context))
	}
	if got.PromptTokens() != 0 {
		t.Fatalf("prompt tokens = %d, want the previous value (0 in this fresh model) untouched", got.PromptTokens())
	}
}
