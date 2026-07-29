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
	args, err := tools.InitDispatchArgs(t.TempDir(), nil)
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

	tool := webfetchtool.NewWithClient(server.Client(), nil, false)
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

	tool := webfetchtool.NewWithClient(server.Client(), nil, false)
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
	tool := webfetchtool.NewWithClient(server.Client(), fp, false)
	content, isErr := run(t, tool, rootArgs(t), webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
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
	tool := webfetchtool.NewWithClient(server.Client(), fp, false)
	content, isErr := run(t, tool, rootArgs(t), webfetchtool.Input{URL: server.URL, Prompt: "what is this page about"})
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

	tool := webfetchtool.NewWithClient(server.Client(), nil, false)
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: server.URL})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "truncated") {
		t.Errorf("expected a truncation note when no project root is available, got: %s", content)
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	tool := webfetchtool.New(nil)
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: "ftp://example.com/file"})
	if !isErr {
		t.Errorf("expected an error result for a non-http(s) scheme, got: %s", content)
	}
}

func TestWebFetch_RejectsEmptyURL(t *testing.T) {
	tool := webfetchtool.New(nil)
	_, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: "   "})
	if !isErr {
		t.Error("expected an error result for an empty url")
	}
}

func TestWebFetch_RejectsPrivateHostsByDefault(t *testing.T) {
	tool := webfetchtool.New(nil) // blockPrivateNetworks defaults to true
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

	tool := webfetchtool.NewWithClient(server.Client(), nil, false)
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

	tool := webfetchtool.NewWithClient(server.Client(), nil, false)
	content, isErr := run(t, tool, tools.DispatchArgs{}, webfetchtool.Input{URL: server.URL})
	if !isErr {
		t.Errorf("expected an error result for a page with no readable text, got: %s", content)
	}
}

func TestWebFetch_MalformedInputIsGoError(t *testing.T) {
	tool := webfetchtool.New(nil)
	_, err := tool.Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{not valid`))
	if err == nil {
		t.Error("expected a go error for malformed input json")
	}
}
