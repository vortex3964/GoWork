package providers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// waitForTokenizerIdle blocks until the background loader for name (if any)
// has published its result.
func waitForTokenizerIdle(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tokenizerMu.Lock()
		loading := tokenizerLoading[name]
		tokenizerMu.Unlock()
		if !loading {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tokenizer load for %q did not finish", name)
}

// waitForAllTokenizersIdle keeps a test's loader stub from intercepting a
// fetch started by an earlier test.
func waitForAllTokenizersIdle(t *testing.T) {
	t.Helper()
	// Real vocabulary loads are bounded by tokenizerLoadTimeout, so allow one
	// full download in case an earlier test started it on a cold cache.
	deadline := time.Now().Add(tokenizerLoadTimeout + 5*time.Second)
	for time.Now().Before(deadline) {
		tokenizerMu.Lock()
		loading := len(tokenizerLoading)
		tokenizerMu.Unlock()
		if loading == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("tokenizer loads did not finish")
}

// resetTokenizerForTest clears any state for name and removes it again once
// the test (and its loader goroutine) is done.
func resetTokenizerForTest(t *testing.T, name string) {
	t.Helper()
	tokenizerMu.Lock()
	delete(tokenizers, name)
	delete(tokenizerFail, name)
	delete(tokenizerLoading, name)
	tokenizerMu.Unlock()
	t.Cleanup(func() {
		tokenizerMu.Lock()
		delete(tokenizers, name)
		delete(tokenizerFail, name)
		delete(tokenizerLoading, name)
		tokenizerMu.Unlock()
	})
}

// newTokenizerTestEncoding builds a tiny real *tiktoken.Tiktoken that
// tokenises the word "hello" as a single token, so tests can exercise the
// cached path without any vocabulary download.
func newTokenizerTestEncoding(t *testing.T) *tiktoken.Tiktoken {
	t.Helper()
	special := map[string]int{"<|endoftext|>": 1}
	bpe, err := tiktoken.NewCoreBPE(
		map[string]int{"hello": 0},
		special,
		`\p{L}+`,
	)
	if err != nil {
		t.Fatalf("build test BPE: %v", err)
	}
	return tiktoken.NewTiktoken(bpe, &tiktoken.Encoding{
		Name:          "test",
		SpecialTokens: special,
	}, map[string]any{"<|endoftext|>": true})
}

func TestCountTextTokensDoesNotWaitForEncodingLoad(t *testing.T) {
	waitForAllTokenizersIdle(t)

	const name = "test-slow-load-encoding"
	resetTokenizerForTest(t, name)

	// Seed an already-loaded encoding so the test can prove a slow load does
	// not serialize counts for a different encoding either.
	const cachedName = "test-cached-encoding"
	cached := newTokenizerTestEncoding(t)
	tokenizerMu.Lock()
	tokenizers[cachedName] = cached
	tokenizerMu.Unlock()
	t.Cleanup(func() {
		tokenizerMu.Lock()
		delete(tokenizers, cachedName)
		tokenizerMu.Unlock()
	})

	var calls atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	orig := getEncoding
	getEncoding = func(encoding string) (*tiktoken.Tiktoken, error) {
		if encoding != name {
			return orig(encoding)
		}
		calls.Add(1)
		<-release
		return nil, errors.New("simulated offline vocabulary load")
	}
	// Restore the hook even if a wait below fails; this defer runs last.
	defer func() { getEncoding = orig }()
	defer func() {
		unblock()
		waitForTokenizerIdle(t, name)
	}()

	// The first count for a new encoding must return the chars/4 fallback
	// immediately instead of waiting on the (blocked) loader.
	start := time.Now()
	if got := countTextTokens(name, "abcdefgh"); got != 3 {
		t.Fatalf("first count = %d, want fallback 3", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("first count blocked for %v on the vocabulary load", elapsed)
	}

	// A count for an encoding that is already cached must not wait behind the
	// slow loader for a different encoding.
	start = time.Now()
	if got := countTextTokens(cachedName, "hello"); got != 1 {
		t.Fatalf("cached count = %d, want 1", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cached encoding count blocked for %v behind another load", elapsed)
	}

	// Repeated concurrent counts must also return quickly and must not start
	// another loader while the first is still in flight.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := countTextTokens(name, strings.Repeat("x", 40)); got != 11 {
				t.Errorf("concurrent count = %d, want fallback 11", got)
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent counts blocked on the vocabulary load")
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader ran %d times, want exactly 1", got)
	}

	// A failed load is remembered: fallback stays in use and is not retried.
	unblock()
	waitForTokenizerIdle(t, name)
	tokenizerMu.Lock()
	failed := tokenizerFail[name]
	tokenizerMu.Unlock()
	if !failed {
		t.Fatal("failed load should mark the encoding unavailable")
	}
	if got := countTextTokens(name, "abcdefgh"); got != 3 {
		t.Fatalf("count after failed load = %d, want fallback 3", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("failed encoding was retried: loader ran %d times", got)
	}
}

func TestCachedBpeLoaderDownloadsOnce(t *testing.T) {
	t.Setenv("TIKTOKEN_CACHE_DIR", t.TempDir())

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("aGVsbG8= 42\n")) // base64("hello") + rank
	}))
	defer srv.Close()

	loader := &cachedBpeLoader{client: srv.Client()}
	url := srv.URL + "/bpe.tiktoken"
	for i := 0; i < 2; i++ {
		ranks, err := loader.LoadTiktokenBpe(url)
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		if got := ranks["hello"]; got != 42 {
			t.Fatalf("load %d: rank = %d, want 42", i, got)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hit %d times, want 1 (second load should use cache)", got)
	}
}

func TestCountTextTokensUsesBackgroundLoadedEncoding(t *testing.T) {
	waitForAllTokenizersIdle(t)

	const name = "test-successful-load-encoding"
	resetTokenizerForTest(t, name)

	loaded := newTokenizerTestEncoding(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	orig := getEncoding
	getEncoding = func(encoding string) (*tiktoken.Tiktoken, error) {
		if encoding != name {
			return orig(encoding)
		}
		<-release
		return loaded, nil
	}
	// Restore the hook even if the wait below fails; this defer runs last.
	defer func() { getEncoding = orig }()
	defer func() {
		unblock()
		waitForTokenizerIdle(t, name)
	}()

	// Before the background load finishes, the estimate is the fallback.
	if got := countTextTokens(name, "hello"); got != 2 {
		t.Fatalf("count while loading = %d, want fallback 2", got)
	}

	unblock()
	waitForTokenizerIdle(t, name)

	tokenizerMu.Lock()
	failed := tokenizerFail[name]
	cached := tokenizers[name]
	tokenizerMu.Unlock()
	if failed || cached != loaded {
		t.Fatal("successful background load was not published")
	}
	if got := countTextTokens(name, "hello"); got != 1 {
		t.Fatalf("count after load = %d, want 1 (tokenised)", got)
	}
}

func TestCountTextTokensFallbackForUnknownEncoding(t *testing.T) {
	const name = "definitely-not-a-tiktoken-encoding"
	resetTokenizerForTest(t, name)

	text := "unknown encodings must still produce a usable estimate"
	want := len(text)/4 + 1
	if got := countTextTokens(name, text); got != want {
		t.Fatalf("count before load settles = %d, want fallback %d", got, want)
	}

	waitForTokenizerIdle(t, name)
	tokenizerMu.Lock()
	failed := tokenizerFail[name]
	tokenizerMu.Unlock()
	if !failed {
		t.Fatal("unknown encoding should be marked unavailable")
	}
	if got := countTextTokens(name, text); got != want {
		t.Fatalf("count after failed load = %d, want fallback %d", got, want)
	}
}
