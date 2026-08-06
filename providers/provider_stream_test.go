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
	"time"
)

// TestOpenAICompatStream verifies the shared OpenAI-compatible streaming
// parser: it forwards text deltas as they arrive and assembles tool-call
// argument fragments plus usage into the final result.
func TestOpenAICompatStream(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.Stream {
			t.Errorf("expected stream:true in request body")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"a.go\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_use\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":7,\"total_tokens\":17}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	// Drive the shared OpenAI-compatible helper directly.
	var got strings.Builder
	var mu sync.Mutex
	res, err := streamOpenAICompat(context.Background(), srv.URL, "test",
		map[string]string{"Authorization": "Bearer k"},
		toOpenAIMessages(context.Background(), []Message{{Role: "user", Content: "hi"}}),
		openAICompatTools(context.Background()), true, func(d string) {
			mu.Lock()
			got.WriteString(d)
			mu.Unlock()
		})
	if err != nil {
		t.Fatalf("streamOpenAICompat: %v", err)
	}
	mu.Lock()
	streamed := got.String()
	mu.Unlock()

	if streamed != "Hello world" {
		t.Errorf("streamed text = %q, want %q", streamed, "Hello world")
	}
	if res.Content != "Hello world" {
		t.Errorf("final content = %q, want %q", res.Content, "Hello world")
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Tool_name != "read_file" {
		t.Errorf("tool name = %q, want read_file", tc.Tool_name)
	}
	// The arguments are accumulated across deltas: {"path":"a.go"}
	var args map[string]string
	if err := json.Unmarshal(tc.Input, &args); err != nil {
		t.Fatalf("args %s not valid JSON: %v", tc.Input, err)
	}
	if args["path"] != "a.go" {
		t.Errorf("args path = %q, want a.go", args["path"])
	}
	if res.StopReason != "tool_use" {
		t.Errorf("stop reason = %q, want tool_use", res.StopReason)
	}
	if res.Usage.TotalTokens != 17 {
		t.Errorf("usage total = %d, want 17", res.Usage.TotalTokens)
	}
}

// TestStreamContextCancel verifies that canceling the context mid-stream
// surfaces as a canceled error - this is what the ctrl+i interrupt relies on
// to stop a running generation.
func TestStreamContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for {
			// The client cancels its request, which cancels r.Context();
			// exit so the handler (and Server.Close) can wind down.
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			fl.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer srv.Close()

	_, err := streamOpenAICompat(ctx, srv.URL, "test",
		map[string]string{"Authorization": "Bearer k"},
		toOpenAIMessages(context.Background(), []Message{{Role: "user", Content: "hi"}}),
		nil, false, func(string) {})
	if err == nil {
		t.Fatal("expected a cancel error, got nil")
	}
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Kind != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
}

// TestOllamaStream verifies the NDJSON streaming parser handles content
// deltas followed by a final chunk carrying tool calls and usage.
func TestOllamaStream(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"Reading"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":" files"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"id":"","function":{"name":"grep_file","arguments":{"pattern":"x","path":"."}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":3}`)
	}))
	defer srv.Close()

	o := &ollamaProvider{model: "test", baseURL: srv.URL, toolsEnabled: true}
	var got strings.Builder
	res, err := o.GenerateStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(d string) {
		got.WriteString(d)
	})
	if err != nil {
		t.Fatalf("ollama stream: %v", err)
	}
	if got.String() != "Reading files" {
		t.Errorf("streamed = %q, want %q", got.String(), "Reading files")
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Tool_name != "grep_file" {
		t.Fatalf("expected grep_file tool call, got %+v", res.ToolCalls)
	}
	if res.ToolCalls[0].Tool_call_id == "" {
		t.Error("synthesized tool_call_id should not be empty")
	}
	if res.Usage.TotalTokens != 8 {
		t.Errorf("usage total = %d, want 8", res.Usage.TotalTokens)
	}
	if res.StopReason != "stop" {
		t.Errorf("stop reason = %q, want stop", res.StopReason)
	}
}
