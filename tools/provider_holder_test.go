package tools

import (
	"context"
	"sync"
	"testing"

	"GoWork/providers"
)

type stubProvider struct {
	name string
}

func (s *stubProvider) Generate(_ context.Context, _ []providers.Message) (providers.GenerateResult, error) {
	return providers.GenerateResult{Content: s.name}, nil
}
func (s *stubProvider) GenerateStream(_ context.Context, _ []providers.Message, _ providers.StreamFunc) (providers.GenerateResult, error) {
	return providers.GenerateResult{Content: s.name}, nil
}
func (s *stubProvider) EstimateTokens(_ context.Context, _ []providers.Message) (int, error) {
	return 0, nil
}
func (s *stubProvider) Info(_ context.Context, _ string) (providers.ModelInfo, error) {
	return providers.ModelInfo{}, nil
}
func (s *stubProvider) ListModels(_ context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}

func TestProviderHolder_UnsetReturnsNil(t *testing.T) {
	h := &ProviderHolder{}
	if p := h.Get(); p != nil {
		t.Fatalf("expected nil provider before Set, got %v", p)
	}
}

func TestProviderHolder_SetAndGet(t *testing.T) {
	h := &ProviderHolder{}
	first := &stubProvider{name: "first"}
	second := &stubProvider{name: "second"}

	h.Set(first)
	if p := h.Get(); p != first {
		t.Fatalf("expected first provider, got %v", p)
	}

	h.Set(second)
	if p := h.Get(); p != second {
		t.Fatalf("expected second provider after Set, got %v", p)
	}
}

func TestProviderHolder_SetNilClears(t *testing.T) {
	h := &ProviderHolder{}
	h.Set(&stubProvider{name: "x"})
	h.Set(nil)
	if p := h.Get(); p != nil {
		t.Fatalf("expected nil provider after Set(nil), got %v", p)
	}
}

// TestProviderHolder_ConcurrentAccess runs concurrent Set/Get calls so the
// race detector can verify the holder is safe to share between the TUI update
// loop and the tool-call goroutines.
func TestProviderHolder_ConcurrentAccess(t *testing.T) {
	h := &ProviderHolder{}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			h.Set(&stubProvider{name: string(rune('a' + i))})
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = h.Get()
			}
		}()
	}
	wg.Wait()
}
