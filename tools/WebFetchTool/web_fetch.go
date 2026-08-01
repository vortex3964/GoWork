// DESC: web_fetch tool - fetches a url and returns its readable text.

// WARN: this is the tool most likely to blow up context if left
// unchecked (a single page can be hundreds of kb), so it has several layers:
//   - downloads are hard-capped at maxDownloadBytes regardless of what the
//     server claims Content-Length is
//   - pages under inlineThreshold chars are returned as-is, no model call
//   - pages over that either get summarized against the caller's "prompt"
//     (capped at maxSummaryInputChars of input) using the configured
//     Provider, or - if theres no provider, no prompt, or the summarization
//     call itself fails - get saved to disk and returned as a short preview
//     plus a file path, so the full page never gets dumped into context
//   - redirects are capped and refuse to leave http(s) or land on a
//     loopback/private/link-local address (basic ssrf protection - a
//     malicious page could otherwise redirect us at localhost or a cloud
//     metadata endpoint)
package webfetchtool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"

	"GoWork/providers"
	"GoWork/tools"
)

const (
	fetchTimeout          = 20 * time.Second
	maxRedirects          = 5
	maxDownloadBytes      = 5 << 20 // 5MB hard cap on the raw response body
	inlineThreshold        = 4000   // chars: below this we just return the text as-is
	maxSummaryInputChars   = 20000  // cap on how much extracted text we hand to the provider
	persistedPreviewChars  = 500
	fetchCacheDir          = ".agent-cache/fetch"
)

type Input struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt,omitempty"`
}

type Tool struct {
	client               *http.Client
	blockPrivateNetworks bool
}

func New() tools.AgentTool {
	c := &http.Client{Timeout: fetchTimeout}
	c.CheckRedirect = makeRedirectGuard(true)
	return &Tool{client: c, blockPrivateNetworks: true}
}

func NewWithClient(client *http.Client, blockPrivateNetworks bool) tools.AgentTool {
	if client.CheckRedirect == nil {
		client.CheckRedirect = makeRedirectGuard(blockPrivateNetworks)
	}
	return &Tool{client: client, blockPrivateNetworks: blockPrivateNetworks}
}

func (t *Tool) Name() string { return "web_fetch" }

func (t *Tool) Description() string {
	return fmt.Sprintf(`Fetches a url and returns its readable text content.

Guardrails: downloads are capped at %d bytes and converted to plain text first. Short pages (under ~%d characters) come back as-is. Longer pages are either summarized against your "prompt" (if you gave one and a model is configured) or saved to a file in the project and returned as a short preview plus the file path - use a read tool on that path if you need the rest. This tool does not execute javascript, so content thats rendered client-side wont appear.`, maxDownloadBytes, inlineThreshold)
}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The url to fetch. Must be http or https.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "What you're looking for on this page. Used to focus the summary when the page is too long to return in full.",
			},
		},
		"required": []string{"url"},
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindWebSearch }

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("web_fetch: invalid input: %w", err)
	}

	trimmed := strings.TrimSpace(input.URL)
	if trimmed == "" {
		return tools.Errf("url is required"), nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return tools.Errf("invalid url %q", trimmed), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return tools.Errf("only http and https urls are supported, got scheme %q", parsed.Scheme), nil
	}
	if t.blockPrivateNetworks && isPrivateHost(parsed.Hostname()) {
		return tools.Errf("refusing to fetch a private/internal address"), nil
	}

	rawBody, contentType, err := t.download(ctx, parsed.String())
	if err != nil {
		return tools.Errf("fetch failed: %s", err), nil
	}

	text := extractText(rawBody, contentType)
	if strings.TrimSpace(text) == "" {
		return tools.Errf("fetched %s but found no readable text (the page may be javascript-rendered or non-text content)", trimmed), nil
	}

	if len(text) <= inlineThreshold {
		return tools.Ok(fmt.Sprintf("fetched %s (%d chars):\n\n%s", trimmed, len(text), text)), nil
	}

	// content is long: prefer a focused summary from the model, and fall
	// back to persisting-to-disk-plus-preview if that isn't possible or fails.
	// the provider is resolved here (not at construction) so a mid-session or
	// even mid-call provider switch is picked up instead of using a stale one.
	var provider providers.Provider
	if args.Provider != nil {
		provider = args.Provider()
	}
	if input.Prompt != "" && provider != nil {
		summary, err := t.summarize(ctx, provider, trimmed, input.Prompt, text)
		if err == nil {
			return tools.Ok(summary), nil
		}
		// summarization failed (rate limited, provider down, etc) - dont
		// fail the whole call, just fall through to the persisted-file path
	}

	return t.persist(args, trimmed, text)
}

func (t *Tool) download(ctx context.Context, target string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", "GoWork-agent/1.0 (+web_fetch tool)")
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return nil, "", fmt.Errorf("reading body: %w", err)
	}

	return body, resp.Header.Get("Content-Type"), nil
}

func extractText(body []byte, contentType string) string {
	if !strings.Contains(contentType, "html") {
		return strings.TrimSpace(string(body))
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return strings.TrimSpace(string(body))
	}

	blockTags := map[string]bool{
		"p": true, "div": true, "li": true, "br": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "tr": true,
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[n.Data] {
			sb.WriteString("\n")
		}
	}
	walk(doc)

	return collapseBlankLines(sb.String())
}

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := false
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// summarize asks the configured provider to extract whats relevant to
// prompt from text, capping how much raw page content we hand over so a
// single fetch cant balloon into a giant model call.
func (t *Tool) summarize(ctx context.Context, p providers.Provider, sourceURL, prompt, text string) (string, error) {
	input := text
	if len(input) > maxSummaryInputChars {
		input = input[:maxSummaryInputChars]
	}

	msg := providers.Message{
		Role: "user",
		Content: fmt.Sprintf(
			"You are extracting information from a fetched web page. Answer the request below using only the page content provided. Be concise - a few short paragraphs at most.\n\nRequest: %s\n\nPage (%s):\n%s",
			prompt, sourceURL, input,
		),
	}

	result, err := p.Generate(ctx, []providers.Message{msg})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("summary of %s (focused on: %s):\n\n%s", sourceURL, prompt, strings.TrimSpace(result.Content)), nil
}

// persist saves the full extracted text under the project root (so it stays
// inside the same sandbox every other write goes through) and hands back a
// short preview plus the path, instead of dumping the whole page into context.
func (t *Tool) persist(args tools.DispatchArgs, sourceURL, text string) (tools.ToolResult, error) {
	preview := text
	if len(preview) > persistedPreviewChars {
		preview = preview[:persistedPreviewChars] + "…"
	}

	if args.Root == nil {
		// no project root to write into (e.g. called outside a dispatcher) -
		// fall back to a hard truncation so we still return something small.
		return tools.Ok(fmt.Sprintf("fetched %s (%d chars, truncated - no project root available to save the full page):\n\n%s", sourceURL, len(text), preview)), nil
	}

	if err := tools.MkdirAllInRoot(args.Root, fetchCacheDir); err != nil {
		return tools.Errf("fetched %s but failed to save it to disk: %s", sourceURL, err), nil
	}

	name := fmt.Sprintf("%s/%d.md", fetchCacheDir, time.Now().UnixNano())
	f, err := args.Root.Create(name)
	if err != nil {
		return tools.Errf("fetched %s but failed to save it to disk: %s", sourceURL, err), nil
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		return tools.Errf("fetched %s but failed to save it to disk: %s", sourceURL, err), nil
	}

	fullPath := filepath.Join(args.RootPath, name)
	return tools.Ok(fmt.Sprintf(
		"fetched %s (%d chars - too long to inline, saved to %s).\nPreview:\n\n%s\n\nUse a read tool on %s for the full content.",
		sourceURL, len(text), fullPath, preview, fullPath,
	)), nil
}

// isPrivateHost reports whether host resolves to a loopback, private, or
// link-local address. used both before the initial request and on every
// redirect hop so a page cant bounce us into internal infrastructure.
func isPrivateHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// cant resolve - let the http client fail naturally on the actual
		// request rather than misclassifying a dns hiccup as a security block
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func makeRedirectGuard(blockPrivate bool) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing to follow redirect to non-http(s) scheme %q", req.URL.Scheme)
		}
		if blockPrivate && isPrivateHost(req.URL.Hostname()) {
			return fmt.Errorf("refusing to follow redirect into a private/internal address")
		}
		return nil
	}
}
