package providers

import (
	"encoding/json"
	"strings"
	"testing"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// preserveToolState snapshots the process-wide prompt and tool registry so a
// test can mutate them freely and restore the original values afterwards.
func preserveToolState(t *testing.T) {
	t.Helper()
	prompt := currentSystemPrompt()
	defs := toolDefs()
	t.Cleanup(func() {
		InitSystemPrompt(prompt)
		InitToolsDef(defs)
	})
}

// newOverheadTestEncoding builds a tokenizer that encodes every byte as one
// token, so arbitrary text can be counted without downloading a vocabulary.
// When helloRank >= 0 the word "hello" is a single token with that rank;
// otherwise it falls back to its five letter tokens.
func newOverheadTestEncoding(t *testing.T, helloRank int) *tiktoken.Tiktoken {
	t.Helper()
	ranks := make(map[string]int, 257)
	for i := 0; i < 256; i++ {
		ranks[string([]byte{byte(i)})] = i
	}
	if helloRank >= 0 {
		ranks["hello"] = helloRank
	}
	special := map[string]int{"<|endoftext|>": 1000}
	bpe, err := tiktoken.NewCoreBPE(ranks, special, `\p{L}+`)
	if err != nil {
		t.Fatalf("build test BPE: %v", err)
	}
	return tiktoken.NewTiktoken(bpe, &tiktoken.Encoding{
		Name:          "test",
		SpecialTokens: special,
	}, map[string]any{"<|endoftext|>": true})
}

// seedOverheadTestEncoding installs enc for encoding until the test ends and
// invalidates cached overhead values so the new tokenizer takes effect.
func seedOverheadTestEncoding(t *testing.T, encoding string, enc *tiktoken.Tiktoken) {
	t.Helper()
	tokenizerMu.Lock()
	prevEnc := tokenizers[encoding]
	prevFail := tokenizerFail[encoding]
	prevLoading := tokenizerLoading[encoding]
	tokenizers[encoding] = enc
	delete(tokenizerFail, encoding)
	delete(tokenizerLoading, encoding)
	tokenizerMu.Unlock()

	t.Cleanup(func() {
		tokenizerMu.Lock()
		if prevEnc != nil {
			tokenizers[encoding] = prevEnc
		} else {
			delete(tokenizers, encoding)
		}
		if prevFail {
			tokenizerFail[encoding] = true
		} else {
			delete(tokenizerFail, encoding)
		}
		if prevLoading {
			tokenizerLoading[encoding] = true
		} else {
			delete(tokenizerLoading, encoding)
		}
		tokenizerMu.Unlock()
		invalidateOverheadCache()
	})
	invalidateOverheadCache()
}

func TestEstimateOverheadTokensForModelReusesCache(t *testing.T) {
	waitForAllTokenizersIdle(t)
	preserveToolState(t)
	seedOverheadTestEncoding(t, "cl100k_base", newOverheadTestEncoding(t, 256))

	InitSystemPrompt("hello")
	InitToolsDef(nil)

	first := EstimateOverheadTokensForModel("gpt-4")
	if want := 512 + 1; first != want {
		t.Fatalf("first estimate = %d, want %d", first, want)
	}

	overheadCacheMu.Lock()
	entry, ok := overheadCache["gpt-4"]
	overheadCacheMu.Unlock()
	if !ok || entry.version != overheadVersion.Load() || entry.tokens != first {
		t.Fatalf("cache entry = %+v (present=%v), want tokens=%d at current version %d", entry, ok, first, overheadVersion.Load())
	}

	// Remove the vocabulary without bumping the version. A cache hit must
	// still return the remembered count; recomputing would fall back to
	// len(text)/4+1 = 2 tokens and yield a different result.
	tokenizerMu.Lock()
	delete(tokenizers, "cl100k_base")
	tokenizerFail["cl100k_base"] = true
	tokenizerMu.Unlock()

	if got := EstimateOverheadTokensForModel("gpt-4"); got != first {
		t.Fatalf("second estimate = %d, want cached %d", got, first)
	}
}

func TestEstimateOverheadCacheInvalidatedByPromptAndTools(t *testing.T) {
	waitForAllTokenizersIdle(t)
	preserveToolState(t)
	seedOverheadTestEncoding(t, "cl100k_base", newOverheadTestEncoding(t, 256))

	InitSystemPrompt("hello")
	InitToolsDef(nil)
	base := EstimateOverheadTokensForModel("gpt-4")

	longPrompt := strings.Repeat("hello ", 10)
	InitSystemPrompt(longPrompt)
	withPrompt := EstimateOverheadTokensForModel("gpt-4")
	if withPrompt <= base {
		t.Fatalf("prompt change did not invalidate cache: before=%d after=%d", base, withPrompt)
	}

	InitToolsDef([]ToolDef{{
		Name:        "hello",
		Description: strings.Repeat("hello ", 20),
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})
	withTools := EstimateOverheadTokensForModel("gpt-4")
	if withTools <= withPrompt {
		t.Fatalf("tool registration did not invalidate cache: before=%d after=%d", withPrompt, withTools)
	}

	// The skills tab swaps the skill tool's description at runtime; that must
	// invalidate the memoized overhead too.
	UpdateToolDef("hello", ToolDef{
		Name:        "hello",
		Description: strings.Repeat("hello ", 40),
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})
	withUpdatedTool := EstimateOverheadTokensForModel("gpt-4")
	if withUpdatedTool <= withTools {
		t.Fatalf("UpdateToolDef did not invalidate cache: before=%d after=%d", withTools, withUpdatedTool)
	}
}

func TestEstimateOverheadCacheIsPerModel(t *testing.T) {
	waitForAllTokenizersIdle(t)
	preserveToolState(t)
	seedOverheadTestEncoding(t, "cl100k_base", newOverheadTestEncoding(t, 256))
	// o200k has no "hello" entry, so it renders as five letter tokens.
	seedOverheadTestEncoding(t, "o200k_base", newOverheadTestEncoding(t, -1))

	InitSystemPrompt("hello")
	InitToolsDef(nil)

	cl100k := EstimateOverheadTokensForModel("gpt-4")
	o200k := EstimateOverheadTokensForModel("gpt-4o")
	if cl100k == o200k {
		t.Fatalf("models using different vocabularies got the same estimate %d", cl100k)
	}

	overheadCacheMu.Lock()
	clEntry, clOK := overheadCache["gpt-4"]
	oEntry, oOK := overheadCache["gpt-4o"]
	overheadCacheMu.Unlock()
	if !clOK || !oOK {
		t.Fatalf("expected separate cache entries per model, got cl100k=%v o200k=%v", clOK, oOK)
	}
	if clEntry.tokens != cl100k || oEntry.tokens != o200k {
		t.Fatalf("cache entries = %+v/%+v, want %d/%d", clEntry, oEntry, cl100k, o200k)
	}
}
