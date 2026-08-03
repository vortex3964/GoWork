// DESC: this file contains the provider interface wich every
// other ai supported will inherit and also contains common functions
// every supported ai will use

//TODO:make context building to send for the api faster instead of having to rebuild
//it every time for every new prompt inside the generate function witch currently is
//only in the gemini file

//TODO: add tool calls to the llm

//TODO: properly parce the llms response right now its scuffed

// IMPORTANT: we should handle retries or handle high server demands etch
package providers

//TODO:maybe we should limit the output tokens of non local models
// so that we dont burn all of our api money

//supported:
//gemini
//groq

//local:
//ollama
//llama.cpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// structure to know whitch tool the ai needs
// responses will be returned to main main will forward
// it to the registry , main is going to be the midleware
// for the tools and the ai to comunicate
type ToolCall struct {
	Tool_call_id string // asigned by the ai model
	Tool_name    string
	Input        json.RawMessage
}

// Message carries structured parts now, not just plain text,
// because "assistant called a tool" and "here's the tool's result"
// both have to round-trip through context on the next turn.
type Message struct {
	Role       string // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []ToolCall // set when assistant message IS a tool call
	ToolCallID string     // set on the "tool" role message that answers a call
}

type ModelInfo struct {
	ID              string
	DisplayName     string
	ContextWindow   int
	MaxOutputTokens int
	// SupportsTools is true when the model is known to be trained for tool
	// calling. Cloud providers expose no API flag, so it's filled from known
	// model families; local servers degrade gracefully when given tools.
	SupportsTools bool
}

// ModelSupportsTools is the hook for asking "can this provider+model call
// tools?" and leans toward "yes" for modern models. Local providers depend on
// the loaded model, so their allowlist is heuristic; an unknown local model
// falls back to true.
func ModelSupportsTools(providerName, model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "google", "gemini", "anthropic", "claude":
		// Claude 3+ / Gemini 1.5+ all support tools.
		return true
	case "groq":
		// Most hosted models support tools; compound models don't.
		if strings.Contains(m, "compound") {
			return false
		}
		return true
	case "openai", "gpt":
		return true
	case "ollama":
		return ollamaModelTools(m)
	case "llamacpp", "lmstudio":
		// Capability belongs to the loaded GGUF, assume yes.
		return true
	default:
		return true
	}
}

// ollamaModelTools is an allowlist of tool-capable Ollama model families, so
// we don't offer tools to models that can't act on them (and would 400).
func ollamaModelTools(m string) bool {
	// Code-completion / embedding variants don't do tool calling. Note: we
	// deliberately do NOT ban "-coder" here -- instruct coder models (e.g.
	// qwen2.5-coder) DO support tools and are matched by the family cases
	// below. Only genuinely non-tool-capable variants are filtered out.
	if strings.Contains(m, "embed") ||
		strings.Contains(m, "minilm") || strings.HasSuffix(strings.TrimSpace(m), "-base") {
		return false
	}
	switch {
	case strings.Contains(m, "qwen3"), strings.Contains(m, "qwen2"),
		strings.Contains(m, "qwen1.5"), strings.Contains(m, "deepseek"):
		return true
	case strings.HasPrefix(m, "llama3.1"), strings.HasPrefix(m, "llama3.2"),
		strings.HasPrefix(m, "llama3.3"), strings.HasPrefix(m, "llama4"):
		return true
	case strings.Contains(m, "gemma"), strings.Contains(m, "functiongemma"),
		strings.Contains(m, "groq-tool-use"), strings.Contains(m, "command-r"),
		strings.Contains(m, "phi3"), strings.Contains(m, "hermes"),
		strings.Contains(m, "mistral"), strings.Contains(m, "mixtral"):
		return true
	case strings.Contains(m, "instruct"):
		return true
	default:
		return false
	}
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type GenerateResult struct {
	Content    string
	ToolCalls  []ToolCall
	StopReason string // "end_turn" | "tool_use" | ...
	Usage      Usage
}

// definition for the tools the ai
// is able to call
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// this will be used by every provider in the generate method
var tools_def []ToolDef

func InitToolsDef(t []ToolDef) {
	tools_def = t
}

// system_prompt holds the rendered system prompt. It's a first-class
// instruction channel with higher authority than the conversation, so it's
// kept out of the message history and injected by each provider into its own
// native system slot instead (top-level "system" for anthropic,
// "systemInstruction" for gemini, a leading role:system message for the
// openai-compatible providers).
var system_prompt string

func InitSystemPrompt(s string) {
	system_prompt = s
}

// systemMessage returns the system prompt as a Message for providers whose
// API expresses it as a leading role:system message. Nil when unset.
func systemMessage() *Message {
	if system_prompt == "" {
		return nil
	}
	return &Message{Role: "system", Content: system_prompt}
}

// hasToolCapability reports whether a server-reported capabilities list
// (ollama/lm studio) includes tool calling. Accepts the common spellings.
func hasToolCapability(caps []string) bool {
	for _, c := range caps {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "tools", "tool_use", "tool":
			return true
		}
	}
	return false
}

// rawArgs normalizes tool-call arguments (a JSON string or raw object) into a
// json.RawMessage for the dispatcher. Empty or invalid JSON degrades to {}.
func rawArgs(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return json.RawMessage("{}")
}

// openAICompatMessages lowers the shared Message slice into OpenAI's chat
// completions shape, shared by openai/groq/llama.cpp/lm studio:
//   - assistant calls echo their tool_calls so the model can reference its ids;
//   - results go out under role "tool", tied to the call by tool_call_id;
//   - everything else passes through as role/content.
func openAICompatMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages)+1)
	if sm := systemMessage(); sm != nil {
		out = append(out, map[string]interface{}{"role": "system", "content": sm.Content})
	}
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			m := map[string]interface{}{"role": "assistant", "content": msg.Content}
			if len(msg.ToolCalls) > 0 {
				calls := make([]map[string]interface{}, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					calls = append(calls, map[string]interface{}{
						"id":   tc.Tool_call_id,
						"type": "function",
						"function": map[string]interface{}{
							"name":      tc.Tool_name,
							"arguments": string(tc.Input),
						},
					})
				}
				m["tool_calls"] = calls
			}
			out = append(out, m)
		case "tool":
			out = append(out, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      msg.Content,
			})
		default:
			out = append(out, map[string]interface{}{"role": msg.Role, "content": msg.Content})
		}
	}
	return out
}

// openAICompatTools wraps each ToolDef into OpenAI's tools array shape.
func openAICompatTools() []map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(tools_def))
	for _, td := range tools_def {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        td.Name,
				"description": td.Description,
				"parameters":  td.InputSchema,
			},
		})
	}
	return tools
}

// NOTE: since ai is stateless and we send all the context everytime then
// just pass it by reference in the Generate function
type Provider interface {
	Generate(ctx context.Context, messages []Message) (GenerateResult, error)
	// GenerateStream is the streaming variant of Generate. It calls onDelta
	// with each chunk of assistant text as it arrives and returns the fully
	// assembled result (content, tool calls, usage) once the stream ends.
	GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error)
	EstimateTokens(ctx context.Context, messages []Message) (int, error)
	Info(ctx context.Context, model string) (ModelInfo, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// TODO: the errors we will have to acount for
type ErrorKind int

const (
	ErrUnknown ErrorKind = iota
	ErrRateLimited
	ErrContextExceeded
	ErrAuthFailed
	ErrInvalidRequest
	ErrServerOverloaded
	ErrTimeout
	ErrCanceled
)

type ProviderError struct {
	Kind    ErrorKind
	Message string
	Err     error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

func newProviderError(kind ErrorKind, msg string, err error) *ProviderError {
	return &ProviderError{Kind: kind, Message: msg, Err: err}
}

func classifyHTTPStatus(status int) ErrorKind {
	switch {
	case status == 401 || status == 403:
		return ErrAuthFailed
	case status == 429:
		return ErrRateLimited
	case status == 400 || status == 422:
		return ErrInvalidRequest
	case status == 408:
		return ErrTimeout
	case status >= 500:
		return ErrServerOverloaded
	default:
		return ErrUnknown
	}
}

// httpClient is used for every non-streaming provider request. It carries an
// overall timeout so a dead host (e.g. a local server that isn't running, or a
// cloud endpoint that hangs) can't block the app forever. Streaming uses
// httpStreamClient instead, which has no hard timeout and relies on the
// request's context for cancellation.
var httpClient = &http.Client{Timeout: 2 * time.Minute}

// StreamFunc is invoked by a provider with each chunk of assistant text as it
// arrives during a streaming call.
type StreamFunc func(delta string)

// httpStreamClient is used for streaming requests. It deliberately has no
// overall timeout - a streamed response can legitimately run for a long time
// - so cancellation comes entirely from the request context (which the caller
// wires up).
var httpStreamClient = &http.Client{}

// streamRequest performs an HTTP request for a streaming call and returns the
// response with its body still open so the caller can read the stream. It
// uses the no-timeout client and honors ctx for cancellation.
func streamRequest(ctx context.Context, method, url string, headers map[string]string, reqBody interface{}) (*http.Response, error) {
	var reader io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return nil, newProviderError(ErrInvalidRequest, "failed to marshal request body", err)
		}
		reader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, newProviderError(ErrInvalidRequest, "failed to build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpStreamClient.Do(req)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, newProviderError(ErrCanceled, "request canceled", err)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, newProviderError(ErrTimeout, "request timed out", err)
		default:
			return nil, newProviderError(ErrUnknown, "request failed", err)
		}
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, newProviderError(classifyHTTPStatus(resp.StatusCode),
			fmt.Sprintf("%s returned %d", url, resp.StatusCode), fmt.Errorf("%s", body))
	}

	return resp, nil
}

// scanStreamLines scans a streaming response body line by line, calling fn
// for every non-empty, trimmed line. It stops (and returns) on context
// cancellation so an interrupt aborts the stream cleanly.
func scanStreamLines(ctx context.Context, resp *http.Response, fn func(line string) error) error {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return newProviderError(ErrCanceled, "request canceled", ctx.Err())
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return newProviderError(ErrUnknown, "reading stream failed", err)
	}
	return nil
}

// sseData strips the "data:" prefix off an SSE line, or returns ok=false if
// the line isn't a data payload. "[DONE]" sentinels yield ok=false too.
func sseData(line string) (payload string, ok bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if p == "" || p == "[DONE]" {
		return "", false
	}
	return p, true
}

// streamOpenAICompat handles the shared OpenAI-compatible streaming shape used
// by openai/groq/llama.cpp/lm studio: SSE chunks whose choices[].delta holds
// incremental text and (optionally) tool-call fragments. requestUsage, when
// true, asks the server to echo token usage in the final chunk.
func streamOpenAICompat(ctx context.Context, url, model string, headers map[string]string, messages []map[string]interface{}, tools []map[string]interface{}, requestUsage bool, onDelta StreamFunc) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}
	if requestUsage {
		reqBody["stream_options"] = map[string]interface{}{"include_usage": true}
	}

	resp, err := streamRequest(ctx, http.MethodPost, url, headers, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}
	defer resp.Body.Close()

	var content strings.Builder
	var usage Usage
	finish := ""
	type tcAcc struct {
		id   string
		name string
		args strings.Builder
	}
	var toolCalls []*tcAcc

	err = scanStreamLines(ctx, resp, func(line string) error {
		payload, ok := sseData(line)
		if !ok {
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return nil
		}
		if chunk.Usage != nil {
			usage = Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		c := chunk.Choices[0]
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		if c.Delta.Content != "" {
			content.WriteString(c.Delta.Content)
			onDelta(c.Delta.Content)
		}
		for _, tc := range c.Delta.ToolCalls {
			for len(toolCalls) <= tc.Index {
				toolCalls = append(toolCalls, &tcAcc{})
			}
			acc := toolCalls[tc.Index]
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.args.WriteString(tc.Function.Arguments)
			}
		}
		return nil
	})
	if err != nil {
		return GenerateResult{}, err
	}

	calls := make([]ToolCall, 0, len(toolCalls))
	for _, acc := range toolCalls {
		if acc.name == "" {
			continue
		}
		calls = append(calls, ToolCall{
			Tool_call_id: acc.id,
			Tool_name:    acc.name,
			Input:        rawArgs(acc.args.String()),
		})
	}

	return GenerateResult{
		Content:    content.String(),
		ToolCalls:  calls,
		StopReason: finish,
		Usage:      usage,
	}, nil
}

func doJSONRequest(ctx context.Context, method, url string, headers map[string]string, reqBody interface{}) ([]byte, error) {
	var reader io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return nil, newProviderError(ErrInvalidRequest, "failed to marshal request body", err)
		}
		reader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, newProviderError(ErrInvalidRequest, "failed to build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, newProviderError(ErrCanceled, "request canceled", err)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, newProviderError(ErrTimeout, "request timed out", err)
		default:
			return nil, newProviderError(ErrUnknown, "request failed", err)
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newProviderError(ErrUnknown, "failed to read response body", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, newProviderError(classifyHTTPStatus(resp.StatusCode),
			fmt.Sprintf("%s returned %d", url, resp.StatusCode), fmt.Errorf("%s", body))
	}

	return body, nil
}

type retryingProvider struct {
	inner      Provider
	maxRetries int
}

func WithRetries(p Provider, maxRetries int) Provider {
	return &retryingProvider{inner: p, maxRetries: maxRetries}
}

func (r *retryingProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	var lastErr error
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		result, err := r.inner.Generate(ctx, messages)
		if err == nil {
			return result, nil
		}
		lastErr = err

		var perr *ProviderError
		if !errors.As(err, &perr) || !retryable(perr.Kind) {
			return GenerateResult{}, err
		}
		select {
		case <-ctx.Done():
			return GenerateResult{}, ctx.Err()
		case <-time.After(backoff(attempt)):
		}
	}
	return GenerateResult{}, lastErr
}

func (r *retryingProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	return r.inner.EstimateTokens(ctx, messages)
}

// GenerateStream forwards to the inner provider without retrying. Retrying a
// stream that partially delivered would re-emit the already-streamed tokens,
// so streams are single-shot: any error bubbles straight back to the caller.
func (r *retryingProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	return r.inner.GenerateStream(ctx, messages, onDelta)
}

func (r *retryingProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	return r.inner.Info(ctx, model)
}

func (r *retryingProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return r.inner.ListModels(ctx)
}

func retryable(k ErrorKind) bool {
	switch k {
	case ErrRateLimited, ErrServerOverloaded, ErrTimeout:
		return true
	default:
		return false
	}
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<attempt) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// Selects an ai provider by name and returns a ready-to-use Provider.
// Cloud providers require a non-empty api_key; local ones ignore it.
func Select_provider(providerName string, model string, api_key string) (Provider, error) {
	name := strings.ToLower(strings.TrimSpace(providerName))
	switch name {
	case "google", "gemini":
		if api_key == "" {
			return nil, fmt.Errorf("empty api key")
		}
		return WithRetries(newGemini(model, api_key), 3), nil
	case "anthropic", "claude":
		if api_key == "" {
			return nil, fmt.Errorf("empty api key")
		}
		return WithRetries(newAnthropic(model, api_key), 3), nil
	case "groq":
		if api_key == "" {
			return nil, fmt.Errorf("empty api key")
		}
		return WithRetries(newGroq(model, api_key), 3), nil
	case "openai", "gpt":
		if api_key == "" {
			return nil, fmt.Errorf("empty api key")
		}
		return WithRetries(newOpenAI(model, api_key), 3), nil
	case "ollama":
		return newOllama(model), nil
	case "llamacpp":
		return newLlamaCpp(model), nil
	case "lmstudio":
		return newLMStudio(model), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
}

// IsLocalProvider reports whether the named provider talks to a local server
// and therefore needs no API key.
func IsLocalProvider(providerName string) bool {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "ollama", "llamacpp", "lmstudio":
		return true
	default:
		return false
	}
}

// NewForListing builds a provider instance solely for ListModels calls.
// For cloud providers api_key must be set; for local ones it may be empty.
func NewForListing(providerName string, api_key string) (Provider, error) {
	return Select_provider(providerName, "", api_key)
}

func ExportContext(context []Message) error {
	var sb strings.Builder
	for _, msg := range context {
		sb.WriteString(fmt.Sprintf("[%s]\n%s\n\n", strings.ToUpper(msg.Role), msg.Content))
	}

	return os.WriteFile("context.txt", []byte(sb.String()), 0644)
}
