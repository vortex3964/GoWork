package websearchtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GoWork/tools"
	websearchtool "GoWork/tools/WebSearchTool"
)

type ddgResult struct {
	title, href, snippet string
}

// ddgPage renders a minimal duckduckgo-lite-shaped results page so we dont
// depend on the real network to test the parser.
func ddgPage(results []ddgResult) string {
	var sb strings.Builder
	sb.WriteString("<html><body><table>")
	for _, r := range results {
		fmt.Fprintf(&sb, `<tr><td><a rel="nofollow" href="%s" class="result-link">%s</a></td></tr>`, r.href, r.title)
		fmt.Fprintf(&sb, `<tr><td class="result-snippet">%s</td></tr>`, r.snippet)
	}
	sb.WriteString("</table></body></html>")
	return sb.String()
}

func run(t *testing.T, tool tools.AgentTool, input any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Run(context.Background(), tools.DispatchArgs{}, raw)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	return res.Content, res.IsError
}

func TestWebSearch_ParsesResults(t *testing.T) {
	page := ddgPage([]ddgResult{
		{"Example Page One", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage1&rut=x", "Snippet for page one."},
		{"Example Page Two", "https://example.com/page2", "Snippet for page two."},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(page))
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	content, isErr := run(t, tool, websearchtool.Input{Query: "test"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if !strings.Contains(content, "Example Page One") || !strings.Contains(content, "https://example.com/page1") {
		t.Errorf("expected decoded result one in output, got: %s", content)
	}
	if !strings.Contains(content, "Example Page Two") || !strings.Contains(content, "https://example.com/page2") {
		t.Errorf("expected result two in output, got: %s", content)
	}
}

func TestWebSearch_ClampsToHardCap(t *testing.T) {
	var results []ddgResult
	for i := 1; i <= 15; i++ {
		results = append(results, ddgResult{
			title:   fmt.Sprintf("Result %d", i),
			href:    fmt.Sprintf("https://example.com/%d", i),
			snippet: "snippet",
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage(results)))
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	content, isErr := run(t, tool, websearchtool.Input{Query: "test", MaxResults: 999})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if strings.Contains(content, "Result 11") {
		t.Errorf("expected result count clamped to the hard cap, but found an 11th result: %s", content)
	}
	if !strings.Contains(content, "Result 10") {
		t.Errorf("expected exactly the hard cap of results, missing the 10th: %s", content)
	}
}

func TestWebSearch_DefaultLimitAppliedWhenUnset(t *testing.T) {
	var results []ddgResult
	for i := 1; i <= 8; i++ {
		results = append(results, ddgResult{
			title: fmt.Sprintf("Result %d", i), href: fmt.Sprintf("https://example.com/%d", i), snippet: "s",
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage(results)))
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	content, isErr := run(t, tool, websearchtool.Input{Query: "test"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if strings.Contains(content, "Result 6") {
		t.Errorf("expected the default cap of 5 results, but found a 6th: %s", content)
	}
}

func TestWebSearch_TruncatesLongSnippets(t *testing.T) {
	longSnippet := strings.Repeat("word ", 200) // well past maxSnippetChars
	page := ddgPage([]ddgResult{{"Result", "https://example.com/x", longSnippet}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(page))
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	content, isErr := run(t, tool, websearchtool.Input{Query: "test"})
	if isErr {
		t.Fatalf("unexpected error result: %s", content)
	}
	if strings.Contains(content, longSnippet) {
		t.Errorf("expected the snippet to be truncated, but the full untruncated snippet was returned")
	}
	if !strings.Contains(content, "…") {
		t.Errorf("expected a truncation marker in the output")
	}
}

func TestWebSearch_NoResultsFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><p>nothing here</p></body></html>"))
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	content, isErr := run(t, tool, websearchtool.Input{Query: "test"})
	if isErr {
		t.Fatalf("expected a non-error 'no results' message, got an error result: %s", content)
	}
	if !strings.Contains(content, "No results") {
		t.Errorf("expected a no-results message, got: %s", content)
	}
}

func TestWebSearch_EmptyQueryRejected(t *testing.T) {
	tool := websearchtool.New()
	_, isErr := run(t, tool, websearchtool.Input{Query: "   "})
	if !isErr {
		t.Error("expected an error result for an empty/whitespace query")
	}
}

func TestWebSearch_NonOKStatusIsToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	content, isErr := run(t, tool, websearchtool.Input{Query: "test"})
	if !isErr {
		t.Error("expected an error result for a non-200 response")
	}
	if !strings.Contains(content, "search failed") {
		t.Errorf("expected error content to mention the failure, got: %s", content)
	}
}

func TestWebSearch_MalformedInputIsGoError(t *testing.T) {
	tool := websearchtool.New()
	_, err := tool.Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{not valid`))
	if err == nil {
		t.Error("expected a go error for malformed input json")
	}
}

func TestWebSearch_CanceledContextIsToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage(nil)))
	}))
	defer server.Close()

	tool := websearchtool.NewWithClient(server.Client(), server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	raw, _ := json.Marshal(websearchtool.Input{Query: "test"})
	res, err := tool.Run(ctx, tools.DispatchArgs{}, raw)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result for a canceled context")
	}
}
