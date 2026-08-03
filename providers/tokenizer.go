package providers

import (
	"strings"
	"sync"
	"time"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// tokenizer mirrors opencode's approach to token accounting: instead of a
// chars/4 guess or a network round-trip to each provider's count endpoint, we
// run the model's actual BPE tokenizer locally. openai's tiktoken vocabulary
// is used for OpenAI-compatible providers; the encoding is picked by model
// name and falls back to cl100k_base for anything unrecognised.
//
// The BPE vocabularies are downloaded once (on first use) and cached, so
// subsequent counts are fully offline. If a vocabulary can't be obtained - an
// offline machine before the first fetch, or a hanging network - counting
// degrades to the old len/4 heuristic and stays degraded for the session so a
// stuck download can never freeze the TUI.

var (
	tokenizerMu   sync.Mutex
	tokenizers    = map[string]*tiktoken.Tiktoken{}
	tokenizerFail = map[string]bool{}
)

// modelTokenEncoding maps a model name to the tiktoken encoding that most
// closely matches how that model tokenises text. Newer OpenAI models use the
// o200k vocabulary; everything else in the OpenAI-compatible family (gpt-3.5,
// gpt-4, most Groq/llama.cpp/lm studio models) uses cl100k_base.
func modelTokenEncoding(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "o200k"),
		strings.Contains(m, "gpt-4o"),
		strings.Contains(m, "gpt-4.1"),
		strings.Contains(m, "gpt-4.5"),
		strings.Contains(m, "gpt-5"),
		strings.Contains(m, "o1"),
		strings.Contains(m, "o3"),
		strings.Contains(m, "o4"),
		strings.Contains(m, "chatgpt-4o"):
		return "o200k_base"
	default:
		return "cl100k_base"
	}
}

// countTokensForModel counts how many tokens text would produce under the
// BPE vocabulary matching model.
func countTokensForModel(model, text string) int {
	return countTextTokens(modelTokenEncoding(model), text)
}

// countTextTokens counts tokens of text using the given tiktoken encoding
// name. Unknown encodings or load failures degrade to a chars/4 estimate.
func countTextTokens(encoding, text string) int {
	if text == "" {
		return 0
	}
	tokenizerMu.Lock()
	defer tokenizerMu.Unlock()

	if tokenizerFail[encoding] {
		return len(text)/4 + 1
	}
	enc, ok := tokenizers[encoding]
	if !ok {
		enc = loadEncoding(encoding)
		if enc == nil {
			// Don't retry a failing/hanging download every turn: mark this
			// encoding unavailable for the rest of the session.
			tokenizerFail[encoding] = true
			return len(text)/4 + 1
		}
		tokenizers[encoding] = enc
	}
	return len(enc.Encode(text, nil, nil))
}

// loadEncoding obtains a tiktoken encoding without ever blocking the caller
// indefinitely on a network fetch. tiktoken's own loader uses a bare
// http.Get with no timeout, so a hanging network would otherwise freeze the
// update loop the first time tokens are counted. We bound it here and report
// failure so the caller can fall back.
func loadEncoding(encoding string) *tiktoken.Tiktoken {
	done := make(chan struct{})
	var enc *tiktoken.Tiktoken
	go func() {
		defer close(done)
		e, err := tiktoken.GetEncoding(encoding)
		if err != nil {
			return
		}
		enc = e
	}()
	select {
	case <-done:
		return enc
	case <-time.After(15 * time.Second):
		return nil
	}
}