package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// captureTransport records JSON request bodies and returns a minimal response
// that every provider's parser accepts. Streaming requests get an SSE (or
// Ollama NDJSON) body instead of a chat completion.
type captureTransport struct {
	mu       sync.Mutex
	requests []map[string]interface{}
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]interface{}
	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		if len(data) > 0 {
			_ = json.Unmarshal(data, &body)
		}
	}

	c.mu.Lock()
	c.requests = append(c.requests, body)
	c.mu.Unlock()

	respBody := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"message":{"content":"ok"},"done":true}`
	if stream, _ := body["stream"].(bool); stream {
		if strings.HasSuffix(req.URL.Path, "/api/chat") {
			respBody = `{"message":{"content":"ok"},"done":true,"done_reason":"stop","prompt_eval_count":1,"eval_count":1}` + "\n"
		} else {
			respBody = "data: [DONE]\n\n"
		}
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(respBody)),
		Request:    req,
	}, nil
}

func (c *captureTransport) reset() {
	c.mu.Lock()
	c.requests = nil
	c.mu.Unlock()
}

func (c *captureTransport) last(t *testing.T) map[string]interface{} {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("no request captured")
	}
	return c.requests[len(c.requests)-1]
}

// roundTripFunc adapts a function into an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// useHTTPClients routes both JSON and streaming provider requests through
// transport for the duration of the test.
func useHTTPClients(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	oldHTTP, oldStream := httpClient, httpStreamClient
	httpClient = &http.Client{Transport: transport}
	httpStreamClient = &http.Client{Transport: transport}
	t.Cleanup(func() {
		httpClient = oldHTTP
		httpStreamClient = oldStream
	})
}

// qaProviderCases returns every OpenAI-compatible provider affected by the
// compaction request shape, pointed at a URL the fake transport ignores.
func qaProviderCases() []struct {
	name string
	p    Provider
} {
	return []struct {
		name string
		p    Provider
	}{
		{"openai", &openaiProvider{api_key: "k", model: "m"}},
		{"groq", &groqProvider{api_key: "k", model: "m"}},
		{"ollama", &ollamaProvider{model: "m", baseURL: "http://fake", toolsEnabled: true}},
		{"llamacpp", &llamaCppProvider{model: "m", baseURL: "http://fake", toolsEnabled: true}},
		{"lmstudio", &lmStudioProvider{model: "m", baseURL: "http://fake", toolsEnabled: true}},
		{"opencode-go", &opencodeGoProvider{api_key: "k", model: "m", baseURL: "http://fake", sessionID: "s"}},
	}
}

// TestWithoutSystemOmitsToolsKey guards against sending "tools": null on the
// internal compaction request: the key must be absent, not present and nil.
func TestWithoutSystemOmitsToolsKey(t *testing.T) {
	InitSystemPrompt("SYSTEM PROMPT")
	InitToolsDef([]ToolDef{{
		Name:        "read_file",
		Description: "reads a file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})
	t.Cleanup(func() {
		InitSystemPrompt("")
		InitToolsDef(nil)
	})

	tr := &captureTransport{}
	useHTTPClients(t, tr)
	msgs := []Message{{Role: "user", Content: "hi"}}

	modes := []struct {
		name string
		call func(Provider, context.Context) error
	}{
		{"generate", func(p Provider, ctx context.Context) error {
			_, err := p.Generate(ctx, msgs)
			return err
		}},
		{"stream", func(p Provider, ctx context.Context) error {
			_, err := p.GenerateStream(ctx, msgs, func(string) {})
			return err
		}},
	}

	for _, mode := range modes {
		for _, tc := range qaProviderCases() {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				tr.reset()
				if err := mode.call(tc.p, WithoutSystem(context.Background())); err != nil {
					t.Fatalf("compaction request: %v", err)
				}
				if tools, ok := tr.last(t)["tools"]; ok {
					t.Fatalf("WithoutSystem request sent a tools key: %v", tools)
				}

				tr.reset()
				if err := mode.call(tc.p, context.Background()); err != nil {
					t.Fatalf("normal request: %v", err)
				}
				if _, ok := tr.last(t)["tools"]; !ok {
					t.Fatal("normal request omitted the tools key")
				}
			})
		}
	}
}

// TestAnthropicStreamUsesFinalUsage makes sure the final cumulative
// message_delta.usage wins over the 1-token estimate in message_start.
func TestAnthropicStreamUsesFinalUsage(t *testing.T) {
	streamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	useHTTPClients(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(streamBody)),
			Request:    req,
		}, nil
	}))

	a := &anthropicProvider{api_key: "k", model: "m"}
	var got strings.Builder
	res, err := a.GenerateStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(delta string) {
		got.WriteString(delta)
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if got.String() != "Hello" || res.Content != "Hello" {
		t.Errorf("content = %q / streamed %q, want Hello", res.Content, got.String())
	}
	if res.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", res.StopReason)
	}
	if res.Usage.PromptTokens != 10 || res.Usage.CompletionTokens != 20 || res.Usage.TotalTokens != 30 {
		t.Errorf("Usage = %+v, want 10/20/30", res.Usage)
	}
}
