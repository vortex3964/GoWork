package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// testOpenCodeGoServer starts an httptest server and points the provider at it
// through OPENCODE_BASE_URL, which is the documented test override.
func testOpenCodeGoServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("OPENCODE_BASE_URL", srv.URL)
	return srv
}

func TestOpenCodeGoListModels(t *testing.T) {
	var gotPath string
	testOpenCodeGoServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"object": "list",
			"data": [
				{
					"id": "deepseek-v4.1-flash",
					"name": "DeepSeek V4.1 Flash",
					"type": "chat",
					"capabilities": {"tools": {"supported": true}},
					"limit": {"context": 1000000, "output": 384000}
				},
				{
					"id": "text-embedding-3",
					"name": "Embedding",
					"type": "embedding",
					"capabilities": {"tools": {"supported": false}},
					"limit": {"context": 8192, "output": 0}
				},
				{
					"id": "chat-no-tools",
					"name": "",
					"type": "chat",
					"capabilities": {"tools": {"supported": false}},
					"limit": {"context": 128000, "output": 8192}
				}
			]
		}`)
	})

	p := newOpenCodeGo("deepseek-v4.1-flash", "sk-test")
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if gotPath != "/v1/models" {
		t.Errorf("models path = %q, want /v1/models", gotPath)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 chat models: %+v", len(models), models)
	}

	first := models[0]
	if first.ID != "deepseek-v4.1-flash" {
		t.Errorf("ID = %q, want deepseek-v4.1-flash", first.ID)
	}
	if first.DisplayName != "DeepSeek V4.1 Flash" {
		t.Errorf("DisplayName = %q, want DeepSeek V4.1 Flash", first.DisplayName)
	}
	if first.ContextWindow != 1000000 {
		t.Errorf("ContextWindow = %d, want 1000000", first.ContextWindow)
	}
	if first.MaxOutputTokens != 384000 {
		t.Errorf("MaxOutputTokens = %d, want 384000", first.MaxOutputTokens)
	}
	if !first.SupportsTools {
		t.Error("SupportsTools = false, want true")
	}

	second := models[1]
	if second.ID != "chat-no-tools" {
		t.Errorf("second ID = %q, want chat-no-tools", second.ID)
	}
	if second.DisplayName != "chat-no-tools" {
		t.Errorf("DisplayName fallback = %q, want chat-no-tools", second.DisplayName)
	}
	if second.SupportsTools {
		t.Error("second SupportsTools = true, want false")
	}

	info, err := p.Info(context.Background(), "deepseek-v4.1-flash")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.ContextWindow != first.ContextWindow || !info.SupportsTools {
		t.Errorf("Info = %+v, want fields from ListModels entry", info)
	}
}

func TestOpenCodeGoHeadersAndSession(t *testing.T) {
	var mu sync.Mutex
	var requests []*http.Request
	var bodies []map[string]interface{}

	testOpenCodeGoServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("chat path = %q, want /openai/v1/chat/completions", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, r.Clone(context.Background()))
		bodies = append(bodies, body)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	})

	p := newOpenCodeGo("test-model", "sk-secret")
	for i := 0; i < 2; i++ {
		if _, err := p.Generate(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
			t.Fatalf("Generate %d: %v", i, err)
		}
	}

	mu.Lock()
	if len(requests) != 2 {
		mu.Unlock()
		t.Fatalf("got %d requests, want 2", len(requests))
	}
	requestsCopy := append([]*http.Request(nil), requests...)
	bodiesCopy := append([]map[string]interface{}(nil), bodies...)
	mu.Unlock()

	var sessions []string
	for i, r := range requestsCopy {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-secret" {
			t.Errorf("request %d Authorization = %q, want Bearer sk-secret", i, got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("request %d Content-Type = %q, want application/json", i, got)
		}
		if got := r.Header.Get("User-Agent"); got != "gowork/1.0" {
			t.Errorf("request %d User-Agent = %q, want gowork/1.0", i, got)
		}
		session := r.Header.Get("x-opencode-session")
		if session == "" {
			t.Errorf("request %d x-opencode-session is empty", i)
		}
		sessions = append(sessions, session)

		if got, _ := bodiesCopy[i]["model"].(string); got != "test-model" {
			t.Errorf("request %d model = %q, want test-model", i, got)
		}
	}
	if sessions[0] != sessions[1] {
		t.Errorf("session id changed between requests: %q vs %q", sessions[0], sessions[1])
	}

	// A caller-supplied conversation id must override the provider default.
	override := context.Background()
	override = WithOpenCodeSession(override, "conversation-42")
	if _, err := p.Generate(override, []Message{{Role: "user", Content: "again"}}); err != nil {
		t.Fatalf("Generate with session override: %v", err)
	}
	mu.Lock()
	overrideSession := requests[2].Header.Get("x-opencode-session")
	mu.Unlock()
	if overrideSession != "conversation-42" {
		t.Errorf("overridden x-opencode-session = %q, want conversation-42", overrideSession)
	}
}

func TestOpenCodeGoGenerateToolCall(t *testing.T) {
	InitToolsDef([]ToolDef{{
		Name:        "read_file",
		Description: "read a file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}})
	defer InitToolsDef(nil)

	var gotTools []interface{}
	testOpenCodeGoServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.Tools) != 1 || body.Tools[0].Function.Name != "read_file" {
			t.Errorf("tools sent = %+v, want read_file", body.Tools)
		}
		gotTools = make([]interface{}, len(body.Tools))

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"content": "I'll read it.",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {"name": "read_file", "arguments": "{\"path\":\"main.go\"}"}
					}]
				},
				"finish_reason": "tool_use"
			}],
			"usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
		}`)
	})

	p := newOpenCodeGo("deepseek-v4.1-flash", "sk-test")
	res, err := p.Generate(context.Background(), []Message{{Role: "user", Content: "read main.go"}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(gotTools) != 1 {
		t.Errorf("server saw %d tools, want 1", len(gotTools))
	}
	if res.Content != "I'll read it." {
		t.Errorf("Content = %q, want %q", res.Content, "I'll read it.")
	}
	if res.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", res.StopReason)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Tool_call_id != "call_1" || tc.Tool_name != "read_file" {
		t.Errorf("tool call = %+v, want call_1/read_file", tc)
	}
	var args map[string]string
	if err := json.Unmarshal(tc.Input, &args); err != nil {
		t.Fatalf("tool args %s are not valid JSON: %v", tc.Input, err)
	}
	if args["path"] != "main.go" {
		t.Errorf("tool args path = %q, want main.go", args["path"])
	}
	if res.Usage.PromptTokens != 12 || res.Usage.CompletionTokens != 8 || res.Usage.TotalTokens != 20 {
		t.Errorf("Usage = %+v, want 12/8/20", res.Usage)
	}
}

func TestOpenCodeGoGenerateStream(t *testing.T) {
	InitToolsDef([]ToolDef{{
		Name:        "read_file",
		Description: "read a file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})
	defer InitToolsDef(nil)

	var streamSession string
	testOpenCodeGoServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("stream path = %q, want /openai/v1/chat/completions", r.URL.Path)
		}
		streamSession = r.Header.Get("x-opencode-session")

		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !body.Stream {
			t.Error("expected stream:true in request body")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"a.go\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_use\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4,\"total_tokens\":13}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	})

	p := newOpenCodeGo("deepseek-v4.1-flash", "sk-test")
	var streamed strings.Builder
	res, err := p.GenerateStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(delta string) {
		streamed.WriteString(delta)
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	if streamSession == "" {
		t.Error("streaming request is missing x-opencode-session")
	}
	if streamed.String() != "Hello world" {
		t.Errorf("streamed text = %q, want %q", streamed.String(), "Hello world")
	}
	if res.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", res.Content, "Hello world")
	}
	if res.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", res.StopReason)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Tool_name != "read_file" {
		t.Fatalf("tool calls = %+v, want read_file", res.ToolCalls)
	}
	if res.ToolCalls[0].Tool_call_id != "call_1" {
		t.Errorf("tool call id = %q, want call_1", res.ToolCalls[0].Tool_call_id)
	}
	var args map[string]string
	if err := json.Unmarshal(res.ToolCalls[0].Input, &args); err != nil {
		t.Fatalf("tool args %s are not valid JSON: %v", res.ToolCalls[0].Input, err)
	}
	if args["path"] != "a.go" {
		t.Errorf("tool args path = %q, want a.go", args["path"])
	}
	if res.Usage.TotalTokens != 13 {
		t.Errorf("usage total = %d, want 13", res.Usage.TotalTokens)
	}
}

func TestOpenCodeGoErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantKind   ErrorKind
		wantDetail string
	}{
		{
			name:       "unauthorized",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"invalid API key"}}`,
			wantKind:   ErrAuthFailed,
			wantDetail: "invalid API key",
		},
		{
			name:       "forbidden",
			status:     http.StatusForbidden,
			body:       `{"error":{"message":"no Go subscription for this account"}}`,
			wantKind:   ErrAuthFailed,
			wantDetail: "no Go subscription for this account",
		},
		{
			name:       "rate limited",
			status:     http.StatusTooManyRequests,
			body:       `{"error":{"message":"too many requests"}}`,
			wantKind:   ErrRateLimited,
			wantDetail: "too many requests",
		},
		{
			name:       "server overloaded",
			status:     http.StatusBadGateway,
			body:       `{"error":{"message":"upstream unavailable"}}`,
			wantKind:   ErrServerOverloaded,
			wantDetail: "upstream unavailable",
		},
		{
			name:       "other status",
			status:     http.StatusBadRequest,
			body:       `{"error":{"message":"bad request body"}}`,
			wantKind:   ErrUnknown,
			wantDetail: "bad request body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testOpenCodeGoServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})

			p := newOpenCodeGo("test-model", "sk-bad")
			_, err := p.ListModels(context.Background())
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			var perr *ProviderError
			if !errors.As(err, &perr) {
				t.Fatalf("error type = %T, want *ProviderError", err)
			}
			if perr.Kind != tc.wantKind {
				t.Errorf("error kind = %v, want %v", perr.Kind, tc.wantKind)
			}
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Errorf("error %q does not contain response body %q", err.Error(), tc.wantDetail)
			}
		})
	}
}

func TestOpenCodeGoRegistration(t *testing.T) {
	for _, name := range []string{"opencode-go", "opencodego", "opencode_go"} {
		if _, err := Select_provider(name, "model", ""); err == nil {
			t.Errorf("Select_provider(%q) with empty key succeeded, want error", name)
		}
		p, err := Select_provider(name, "model", "sk-test")
		if err != nil {
			t.Fatalf("Select_provider(%q): %v", name, err)
		}
		if _, ok := p.(*retryingProvider); !ok {
			t.Errorf("Select_provider(%q) = %T, want retrying provider", name, p)
		}
		if IsLocalProvider(name) {
			t.Errorf("IsLocalProvider(%q) = true, want false", name)
		}
		if !ModelSupportsTools(name, "deepseek-v4.1-flash") {
			t.Errorf("ModelSupportsTools(%q) = false, want true", name)
		}
	}
}
