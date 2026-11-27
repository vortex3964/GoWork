package gowork

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"GoWork/providers"
)

// fastStreamProvider emits all of its deltas synchronously, as fast as the
// consumer will take them. That's the case the old byte-count throttle
// handled badly: a fast model produced one message per 64 bytes.
type fastStreamProvider struct {
	deltas []string
}

func (p fastStreamProvider) Generate(context.Context, []providers.Message) (providers.GenerateResult, error) {
	var b strings.Builder
	for _, d := range p.deltas {
		b.WriteString(d)
	}
	return providers.GenerateResult{Content: b.String()}, nil
}

func (p fastStreamProvider) GenerateStream(_ context.Context, _ []providers.Message, onDelta providers.StreamFunc) (providers.GenerateResult, error) {
	var b strings.Builder
	for _, d := range p.deltas {
		b.WriteString(d)
		onDelta(d)
	}
	return providers.GenerateResult{Content: b.String()}, nil
}

func (fastStreamProvider) Info(context.Context, string) (providers.ModelInfo, error) {
	return providers.ModelInfo{}, nil
}

func (fastStreamProvider) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}

func TestStartStreamingCoalescesRapidDeltas(t *testing.T) {
	const n = 2000
	deltas := make([]string, n)
	for i := range deltas {
		deltas[i] = "x"
	}
	p := fastStreamProvider{deltas: deltas}

	ch := make(chan tea.Msg, n+8)
	startStreaming(context.Background(), p, nil, ch)

	var got []string
	var end *streamEndMsg
	for msg := range ch {
		switch v := msg.(type) {
		case streamDeltaMsg:
			got = append(got, v.delta)
		case streamEndMsg:
			m := v
			end = &m
		}
	}

	if end == nil {
		t.Fatal("stream closed without a streamEndMsg")
	}
	// The old 64-byte threshold would have produced >30 messages here.
	if len(got) > 5 {
		t.Fatalf("got %d delta messages for %d rapid deltas, want coalesced", len(got), n)
	}
	joined := strings.Join(got, "")
	if joined != end.result.Content {
		t.Fatalf("streamed deltas lost text: got %d bytes, want %d", len(joined), len(end.result.Content))
	}
}
