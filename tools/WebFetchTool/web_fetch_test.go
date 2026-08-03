package webfetchtool_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GoWork/providers"
	"GoWork/tools"
	webfetchtool "GoWork/tools/WebFetchTool"
)

// fakeProvider is a minimal providers.Provider stand-in so tests never make
// a real model call - it just returns canned content, or a canned error to
// exercise the graceful-degradation path.
type fakeProvider struct {
	content string
	err     error
	calls   int
}

func (f *fakeProvider) Generate(_ context.Context, _ []providers.Message) (providers.GenerateResult, error) {
	f.calls++
	if f.err != nil {
		return providers.GenerateResult{}, f.err
	}
	return providers.GenerateResult{Content: f.content}, nil
}
func (f *fakeProvider) GenerateStream(_ context.Context, _ []providers.Message, _ providers.StreamFunc) (providers.GenerateResult, error) {
	f.calls++
	if f.err != nil {
		return providers.GenerateResult{}, f.err
	}
	return providers.GenerateResult{Content: f.content}, nil
}
func (f *fakeProvider) EstimateTokens(_ context.Context, _ []providers.Message) (int, error) {
	return 0, nil
}
func (f *fakeProvider) Info(_ context.Context, _ string) (providers.ModelInfo, error) {
	return providers.ModelInfo{}, nil
}
func (f *fakeProvider) ListModels(_ context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}

func run(t *testing.T, tool tools.AgentTool, args tools.DispatchArgs, input any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Run(context.Background(), args, raw)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	return res.Content, res.IsError
}

func rootArgs(t *testing.T) tools.DispatchArgs {
	t.Helper()
	return rootArgsWithProvider(t, nil)
}

// rootArgsWithProvider builds dispatch args whose provider getter returns p.
// p may be nil to simulate "no provider configured".
func rootArgsWithProvider(t *testing.T, p providers.Provider) tools.DispatchArgs {
	t.Helper()
	args, err := tools.InitDispatchArgs(t.TempDir(), nil, func() providers.Provider { return p })
	if err != nil {
		t.Fatalf("init dispatch args: %v", err)
	}
	t.Cleanup(func() { args.Root.Close() })
	return args
}

func longHTML() string {
	return "<html><body><p>" + strings.Repeat("word ", 2000) + "</p></body></html>"
}

func TestWebFetch_ShortPageReturnsInline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><p>Hello from a small page.</p></body></html>"))
	}))
	defer server.Close()

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, rootArgs(t), webfetchtool.Input{URL: server.URL})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "Hello from a small page.") {
		t.Errorf("expected page text inline, got: %s", content)
	}
	if strings.Contains(content, "saved to") {
		t.Errorf("short page should not be persisted to disk: %s", content)
	}
}

func TestWebFetch_LongPageWithoutProviderPersistsToDisk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(longHTML()))
	}))
	defer server.Close()

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, rootArgs(t), webfetchtool.Input{URL: server.URL})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "saved to") {
		t.Errorf("expected the long page to be persisted with a note, got: %s", content)
	}
}

func TestWebFetch_LongPageWithProviderSummarizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(longHTML()))
	}))
	defer server.Close()

	fp := &fakeProvider{content: "the page is just repeated words"}
	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, rootArgsWithProvider(t, fp), webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if fp.calls != 1 {
		t.Errorf("expected exactly one summarization call, got %d", fp.calls)
	}
	if !strings.Contains(content, "the page is just repeated words") {
		t.Errorf("expected the summary in the result, got: %s", content)
	}
	if strings.Contains(content, "saved to") {
		t.Errorf("a successful summary should not also persist to disk: %s", content)
	}
}

func TestWebFetch_SummarizationFailureFallsBackToPersist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(longHTML()))
	}))
	defer server.Close()

	fp := &fakeProvider{err: errors.New("rate limited")}
	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, rootArgsWithProvider(t, fp), webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "saved to") {
		t.Errorf("expected a fallback to disk persistence when summarization fails, got: %s", content)
	}
}

func TestWebFetch_NoRootFallsBackToTruncatedInline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(longHTML()))
	}))
	defer server.Close()

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: server.URL})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "truncated") {
		t.Errorf("expected a truncation note when no project root is available, got: %s", content)
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	tool := webfetchtool.New()
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: "ftp://example.com/file"})
	if !isErr {
		t.Errorf("expected an error result for a non-http(s) scheme, got: %s", content)
	}
}

func TestWebFetch_RejectsEmptyURL(t *testing.T) {
	tool := webfetchtool.New()
	_, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: "   "})
	if !isErr {
		t.Error("expected an error result for an empty url")
	}
}

func TestWebFetch_RejectsPrivateHostsByDefault(t *testing.T) {
	tool := webfetchtool.New() // blockPrivateNetworks defaults to true
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: "http://127.0.0.1:9/"})
	if !isErr {
		t.Errorf("expected a private/loopback url to be rejected, got: %s", content)
	}
}

func TestWebFetch_NonOKStatusIsToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: server.URL})
	if !isErr {
		t.Errorf("expected an error result for a non-200 response, got: %s", content)
	}
	if !strings.Contains(content, "fetch failed") {
		t.Errorf("expected error content to mention the failure, got: %s", content)
	}
}

func TestWebFetch_NoReadableTextIsToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><script>var x = 1;</script><style>.a{}</style></head><body></body></html>"))
	}))
	defer server.Close()

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: server.URL})
	if !isErr {
		t.Errorf("expected an error result for a page with no readable text, got: %s", content)
	}
}

func TestWebFetch_MalformedInputIsGoError(t *testing.T) {
	tool := webfetchtool.New()
	_, err := tool.Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{not valid`))
	if err == nil {
		t.Error("expected a go error for malformed input json")
	}
}

func longPageServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(longHTML()))
	}))
	t.Cleanup(server.Close)
	return server
}

// TestWebFetch_ProviderResolvedAtCallTime proves the provider is fetched from
// the getter at Run time, not captured when the tool is constructed. The
// provider is swapped in between constructing the tool and calling Run, and
// the summary must come from the new one.
func TestWebFetch_ProviderResolvedAtCallTime(t *testing.T) {
	server := longPageServer(t)

	var current providers.Provider
	args, err := tools.InitDispatchArgs(t.TempDir(), nil, func() providers.Provider { return current })
	if err != nil {
		t.Fatalf("init dispatch args: %v", err)
	}
	t.Cleanup(func() { args.Root.Close() })

	tool := webfetchtool.NewWithClient(server.Client(), false)

	current = &fakeProvider{content: "summary from the newly selected provider"}

	content, isErr := run(t, tool, args, webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "summary from the newly selected provider") {
		t.Errorf("expected the freshly selected provider to be used, got: %s", content)
	}
	if strings.Contains(content, "saved to") {
		t.Errorf("a successful summary should not persist to disk: %s", content)
	}
}

// TestWebFetch_ProviderSwitchBetweenCalls proves a mid-session provider switch
// is honored on subsequent tool calls.
func TestWebFetch_ProviderSwitchBetweenCalls(t *testing.T) {
	server := longPageServer(t)

	current := providers.Provider(&fakeProvider{content: "first provider summary"})
	args, err := tools.InitDispatchArgs(t.TempDir(), nil, func() providers.Provider { return current })
	if err != nil {
		t.Fatalf("init dispatch args: %v", err)
	}
	t.Cleanup(func() { args.Root.Close() })

	tool := webfetchtool.NewWithClient(server.Client(), false)

	content1, isErr := run(t, tool, args, webfetchtool.Input{URL: server.URL, Prompt: "summarize"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content1)
	}
	if !strings.Contains(content1, "first provider summary") {
		t.Errorf("expected first provider summary, got: %s", content1)
	}

	current = &fakeProvider{content: "second provider summary"}

	content2, isErr := run(t, tool, args, webfetchtool.Input{URL: server.URL, Prompt: "summarize"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content2)
	}
	if !strings.Contains(content2, "second provider summary") {
		t.Errorf("expected second provider summary after switching, got: %s", content2)
	}
	if strings.Contains(content2, "first provider summary") {
		t.Errorf("stale provider leaked into the second call: %s", content2)
	}
}

// TestWebFetch_NoProviderGetterPersists proves a long page still falls back to
// disk persistence when the getter is nil, even if a prompt is supplied.
func TestWebFetch_NoProviderGetterPersists(t *testing.T) {
	server := longPageServer(t)

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, rootArgs(t), webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "saved to") {
		t.Errorf("expected a fallback to disk persistence when no provider getter is set, got: %s", content)
	}
}

// TestWebFetch_NilProviderFromGetterPersists proves a getter returning nil (no
// provider selected yet) degrades to disk persistence, not a panic.
func TestWebFetch_NilProviderFromGetterPersists(t *testing.T) {
	server := longPageServer(t)

	args, err := tools.InitDispatchArgs(t.TempDir(), nil, func() providers.Provider { return nil })
	if err != nil {
		t.Fatalf("init dispatch args: %v", err)
	}
	t.Cleanup(func() { args.Root.Close() })

	tool := webfetchtool.NewWithClient(server.Client(), false)
	content, isErr := run(t, tool, args, webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "saved to") {
		t.Errorf("expected a fallback to disk persistence when the getter returns nil, got: %s", content)
	}
}
