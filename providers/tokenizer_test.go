package providers

import "testing"

func TestCountTextTokens(t *testing.T) {
	// Fixed, stable value for the cl100k_base BPE vocabulary: it should stay
	// stable across runs because the vocabulary itself doesn't change.
	text := "Hello world, this is a token counting test!"
	n := countTextTokens("cl100k_base", text)
	if n != 10 {
		t.Fatalf("cl100k_base tokens = %d, want 10", n)
	}
	n2 := countTextTokens("o200k_base", text)
	if n2 != 10 {
		t.Fatalf("o200k_base tokens = %d, want 10", n2)
	}
}

func TestCountTokensForModelPicksEncoding(t *testing.T) {
	// o200k-using models tokenize differently than cl100k; the classifier
	// should route gpt-4o to o200k_base and gpt-4 to cl100k_base.
	emoji := "hello 👋 world 🌍"
	o200k := countTokensForModel("gpt-4o", emoji)
	cl100k := countTokensForModel("gpt-4", emoji)
	if o200k == cl100k {
		t.Fatalf("expected gpt-4o and gpt-4 to count the emoji text differently, got %d both", o200k)
	}
}

func TestModelTokenEncodingClassification(t *testing.T) {
	cases := map[string]string{
		"gpt-4o":              "o200k_base",
		"gpt-4o-mini":         "o200k_base",
		"gpt-4.1":             "o200k_base",
		"o1-mini":             "o200k_base",
		"o3":                  "o200k_base",
		"chatgpt-4o-latest":   "o200k_base",
		"gpt-4":               "cl100k_base",
		"gpt-3.5-turbo":       "cl100k_base",
		"llama-3.1-70b":       "cl100k_base",
		"mistral-large":       "cl100k_base",
		"unknown-model-model": "cl100k_base",
	}
	for model, want := range cases {
		if got := modelTokenEncoding(model); got != want {
			t.Errorf("modelTokenEncoding(%q) = %q, want %q", model, got, want)
		}
	}
}