// DESC: web_search tool - searches the web via duckduckgo's "lite" endpoint
// (no api key needed) and returns a short list of title/url/snippet results.
//
// WARN: this tool is deliberately capped so a single call cant blow
// the context window or hammer duckduckgo:
//   - max_results is clamped to [1, maxResultsHardCap]
//   - every snippet is truncated to maxSnippetChars
//   - the whole formatted result is truncated to maxOutputChars as a backstop
//   - calls are spaced out by a small jittered delay thats context-aware,
//     so a canceled run doesn't sit blocked on the sleep
package websearchtool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"GoWork/tools"
)

const (
	defaultMaxResults = 5
	maxResultsHardCap = 10
	maxSnippetChars   = 280
	maxOutputChars    = 3000 // hard backstop on the whole formatted result
	requestTimeout    = 15 * time.Second
	minRequestGap     = 500 * time.Millisecond
	jitterGap         = 1500 * time.Millisecond
	maxResponseBytes  = 2 << 20 // 2MB - way more than a lite results page needs
)

type Input struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type result struct {
	Title   string
	Link    string
	Snippet string
}

type Tool struct {
	client  *http.Client
	baseURL string

	mu       sync.Mutex
	lastCall time.Time
}

// New builds the tool against the real duckduckgo lite endpoint.
func New() tools.AgentTool {
	return &Tool{
		client:  &http.Client{Timeout: requestTimeout},
		baseURL: "https://lite.duckduckgo.com/lite/",
	}
}

// NewWithClient lets callers (tests, or anyone who wants a custom transport
// or a mirror endpoint) inject their own http client + base url.
func NewWithClient(client *http.Client, baseURL string) tools.AgentTool {
	return &Tool{client: client, baseURL: baseURL}
}

func (t *Tool) Name() string { return "web_search" }

func (t *Tool) Description() string {
	return fmt.Sprintf(`Searches the web and returns a short list of results (title, url, snippet).

Results are capped at %d by default and never more than %d, and every snippet is trimmed to keep the response small - this is a discovery tool, not a way to read full pages. Use the web_fetch tool on a specific url from the results if you need the actual content of a page.`, defaultMaxResults, maxResultsHardCap)
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Max results to return, 1-%d. Defaults to %d.", maxResultsHardCap, defaultMaxResults),
			},
		},
		"required":               []string{"query"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindNotAllowed }

func (t *Tool) Run(ctx context.Context, _ tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("web_search: invalid input: %w", err)
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return tools.Errf("query is required"), nil
	}

	max := input.MaxResults
	if max <= 0 {
		max = defaultMaxResults
	}
	if max > maxResultsHardCap {
		max = maxResultsHardCap
	}

	if err := t.waitTurn(ctx); err != nil {
		return tools.Errf("search canceled: %s", err), nil
	}

	results, err := t.search(ctx, query, max)
	if err != nil {
		return tools.Errf("search failed: %s", err), nil
	}

	return tools.Ok(formatResults(results)), nil
}

// waitTurn enforces a small jittered gap between requests so we dont hammer
// duckduckgo with back-to-back calls (e.g. the model retrying rapidly).
// its context-aware so a canceled run doesn't block on the sleep.
func (t *Tool) waitTurn(ctx context.Context) error {
	t.mu.Lock()
	gap := minRequestGap + time.Duration(rand.Int63n(int64(jitterGap)))
	wait := gap - time.Since(t.lastCall)
	t.lastCall = time.Now()
	t.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// small rotation so we dont send the exact same fingerprint on every call.
var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
}

func (t *Tool) search(ctx context.Context, query string, max int) ([]result, error) {
	reqURL := t.baseURL + "?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgents[rand.Intn(len(userAgents))])
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return parseResults(body, max)
}

// parseResults walks duckduckgo lite's html looking for the same two
// markers every "lite" result row uses: an <a class="result-link"> for the
// title+href, followed by a <td class="result-snippet"> for the blurb.
func parseResults(body []byte, max int) ([]result, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing html: %w", err)
	}

	var results []result
	var current *result

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= max {
			return
		}
		if n.Type == html.ElementNode {
			if n.Data == "a" && hasClass(n, "result-link") {
				if current != nil && current.Link != "" {
					results = append(results, *current)
				}
				if len(results) >= max {
					current = nil
					return
				}
				current = &result{Title: textContent(n)}
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						current.Link = cleanRedirect(attr.Val)
					}
				}
			}
			if n.Data == "td" && hasClass(n, "result-snippet") && current != nil {
				current.Snippet = truncate(textContent(n), maxSnippetChars)
			}
		}
		for c := n.FirstChild; c != nil && len(results) < max; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if current != nil && current.Link != "" && len(results) < max {
		results = append(results, *current)
	}

	return results, nil
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// cleanRedirect unwraps duckduckgo lite's own redirect links
// (//duckduckgo.com/l/?uddg=<url>&...) so we hand back the real destination
// instead of a link that bounces back through duckduckgo.
func cleanRedirect(raw string) string {
	if !strings.Contains(raw, "uddg=") {
		return raw
	}
	_, after, ok := strings.Cut(raw, "uddg=")
	if !ok {
		return raw
	}
	if amp := strings.Index(after, "&"); amp != -1 {
		after = after[:amp]
	}
	if decoded, err := url.QueryUnescape(after); err == nil {
		return decoded
	}
	return raw
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func formatResults(results []result) string {
	if len(results) == 0 {
		return "No results found. Try a different query."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d result(s):\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, r.Link)
		if r.Snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Snippet)
		}
		sb.WriteString("\n")
	}

	out := sb.String()
	if len(out) > maxOutputChars {
		out = out[:maxOutputChars] + "\n…(truncated - narrow your query for more focused results)"
	}
	return out
}
