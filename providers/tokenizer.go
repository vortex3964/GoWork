package providers

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// tokenizerLoadTimeout bounds the one-time vocabulary download. tiktoken's
// default BPE loader uses a bare http.Get with no timeout, so its goroutine
// (and tiktoken's internal encoding lock) could otherwise hang forever on a
// stalled network.
const tokenizerLoadTimeout = 15 * time.Second

var (
	tokenizerMu      sync.Mutex
	tokenizers       = map[string]*tiktoken.Tiktoken{}
	tokenizerFail    = map[string]bool{}
	tokenizerLoading = map[string]bool{}

	// getEncoding is tiktoken.GetEncoding, swapped out by tests so they can
	// simulate a slow or failing vocabulary load without touching the network.
	getEncoding = tiktoken.GetEncoding
)

func init() {
	// Install a loader whose HTTP fetch is bounded by a client timeout. The
	// default loader reads/writes the same on-disk cache, so an already
	// downloaded vocabulary still works offline.
	tiktoken.SetBpeLoader(&cachedBpeLoader{
		client: &http.Client{Timeout: tokenizerLoadTimeout},
	})
}

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
//
// The vocabulary is fetched in the background the first time an encoding is
// needed, so a slow or offline network can never stall the caller (which runs
// on the TUI update path). Until the fetch finishes, counts use the fallback
// estimate; once it succeeds they use the real vocabulary.
func countTextTokens(encoding, text string) int {
	if text == "" {
		return 0
	}

	tokenizerMu.Lock()
	enc := tokenizers[encoding]
	if enc == nil && !tokenizerFail[encoding] && !tokenizerLoading[encoding] {
		// One background loader per encoding; repeated counts while it is in
		// flight reuse the guard instead of spawning duplicates.
		tokenizerLoading[encoding] = true
		go loadEncoding(encoding)
	}
	tokenizerMu.Unlock()

	if enc == nil {
		return len(text)/4 + 1
	}
	return len(enc.Encode(text, nil, nil))
}

// loadEncoding fetches one encoding in the background and publishes the
// result under tokenizerMu. The loader goroutine owns every write to shared
// state, so no caller ever waits on (or receives a post-return write from) a
// fetch it has abandoned. A failed or timed-out load is remembered and not
// retried for the rest of the session, keeping the chars/4 fallback in use.
func loadEncoding(encoding string) {
	enc, err := getEncoding(encoding)

	tokenizerMu.Lock()
	defer tokenizerMu.Unlock()
	delete(tokenizerLoading, encoding)
	if err != nil || enc == nil {
		tokenizerFail[encoding] = true
		return
	}
	tokenizers[encoding] = enc
}

// cachedBpeLoader mirrors tiktoken's default BPE loader (on-disk cache plus
// one HTTP fetch) but bounds the download with an http.Client timeout, so the
// loader goroutine is guaranteed to finish even when the network hangs.
type cachedBpeLoader struct {
	client *http.Client
}

func (l *cachedBpeLoader) LoadTiktokenBpe(blobPath string) (map[string]int, error) {
	data, err := l.read(blobPath)
	if err != nil {
		return nil, err
	}
	ranks := make(map[string]int)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			return nil, fmt.Errorf("tiktoken: malformed vocabulary line %q", line)
		}
		token, err := base64.StdEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, err
		}
		rank, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
		ranks[string(token)] = rank
	}
	return ranks, nil
}

// read returns a vocabulary from disk, or downloads it once and caches it
// using the same cache directory/naming convention as tiktoken's loader.
func (l *cachedBpeLoader) read(blobPath string) ([]byte, error) {
	if !strings.HasPrefix(blobPath, "http://") && !strings.HasPrefix(blobPath, "https://") {
		return os.ReadFile(blobPath)
	}

	cachePath := bpeCachePath(blobPath)
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}

	resp, err := l.client.Get(blobPath)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tiktoken: GET %s: %s", blobPath, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".tiktoken-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	return data, nil
}

// bpeCachePath matches tiktoken's cache location: TIKTOKEN_CACHE_DIR, then
// DATA_GYM_CACHE_DIR, then <tmp>/data-gym-cache, keyed by the URL's SHA-1.
func bpeCachePath(blobPath string) string {
	cacheDir := strings.TrimSpace(os.Getenv("TIKTOKEN_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = strings.TrimSpace(os.Getenv("DATA_GYM_CACHE_DIR"))
	}
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "data-gym-cache")
	}
	return filepath.Join(cacheDir, fmt.Sprintf("%x", sha1.Sum([]byte(blobPath))))
}
