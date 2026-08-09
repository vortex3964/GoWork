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
	"strings"
	"sync"
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
	// ThoughtSignature is gemini-only. Thinking models return a signature on
	// every functionCall part, and that signature MUST be echoed back on the
	// same functionCall when it's replayed in a later turn, or the API 400s.
	ThoughtSignature string
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
	case strings.Contains(m, "qwen"), strings.Contains(m, "deepseek"):
		return true
	case strings.HasPrefix(m, "llama4"), strings.HasPrefix(m, "llama3"), strings.HasPrefix(m, "llama"),
		strings.Contains(m, "codestral"), strings.Contains(m, "devstral"):
		return true
	case strings.Contains(m, "gemma"), strings.Contains(m, "functiongemma"),
		strings.Contains(m, "groq-tool-use"), strings.Contains(m, "command-r"),
		strings.Contains(m, "phi"), strings.Contains(m, "hermes"),
		strings.Contains(m, "mistral"), strings.Contains(m, "mixtral"),
		strings.Contains(m, "glm"), strings.Contains(m, "yi"),
		strings.Contains(m, "internlm"), strings.Contains(m, "solar"),
		strings.Contains(m, "aya"), strings.Contains(m, "granite"):
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

// this will be used by every provider in the generate method. The slice is
// read on streaming goroutines, so every access goes through the lock: the
// skills tab swaps one entry (the skill tool) at runtime via UpdateToolDef.
var toolsMu sync.RWMutex
var tools_def []ToolDef

func InitToolsDef(t []ToolDef) {
	toolsMu.Lock()
	defer toolsMu.Unlock()
	tools_def = t
}

// UpdateToolDef replaces a single tool's registration (name/description/
// schema). The skills tab uses it to keep the skill tool's available-skills
// list in sync with the session's loaded skills. Reports whether the tool
// was found.
func UpdateToolDef(name string, td ToolDef) bool {
	toolsMu.Lock()
	defer toolsMu.Unlock()
	for i := range tools_def {
		if tools_def[i].Name == name {
			tools_def[i] = td
			return true
		}
	}
	return false
}

// toolDefs returns a snapshot of the registered tools for one request.
func toolDefs() []ToolDef {
	toolsMu.RLock()
	defer toolsMu.RUnlock()
	out := make([]ToolDef, len(tools_def))
	copy(out, tools_def)
	return out
}

// hasTools reports whether any tools are registered.
func hasTools() bool {
	toolsMu.RLock()
	defer toolsMu.RUnlock()
	return len(tools_def) > 0
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
// API expresses it as a leading role:system message. Nil when unset or when
// the request opts out (context compaction).
func systemMessage(ctx context.Context) *Message {
	if system_prompt == "" || omitSystem(ctx) {
		return nil
	}
	return &Message{Role: "system", Content: system_prompt}
}

// noSystemKey marks a request as an internal call (context compaction) where
// the fixed per-request overhead - the system prompt and the tool schemas -
// is useless and would only eat tokens, so providers skip both.
type noSystemKey struct{}

// WithoutSystem returns a context that makes providers omit the system prompt
// and tool schemas from the request. Used for the compaction call, which only
// needs the conversation history and the compaction prompt.
func WithoutSystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, noSystemKey{}, true)
}

// omitSystem reports whether ctx was produced by WithoutSystem.
func omitSystem(ctx context.Context) bool {
	v, _ := ctx.Value(noSystemKey{}).(bool)
	return v
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

// toolSchemaForProto returns a copy of an OpenAI-style tool schema with
// JSON-Schema keywords the provider's schema format doesn't support -
// notably additionalProperties, which openai's JSON-Schema subset accepts but
// gemini (protobuf Schema) and anthropic's input_schema reject with a 400.
// The sanitizer is recursive so nested object schemas get cleaned too.
func toolSchemaForProto(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, _ := json.Marshal(dropAdditionalProperties(v))
	return b
}

func dropAdditionalProperties(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "additionalProperties" {
				continue
			}
			out[k] = dropAdditionalProperties(val)
		}
		return out
	case []any:
		arr := make([]any, len(t))
		for i, x := range t {
			arr[i] = dropAdditionalProperties(x)
		}
		return arr
	default:
		return v
	}
}

// openAICompatMessages lowers the shared Message slice into OpenAI's chat
// completions shape, shared by openai/groq/llama.cpp/lm studio:
//   - assistant calls echo their tool_calls so the model can reference its ids;
//   - results go out under role "tool", tied to the call by tool_call_id;
//   - everything else passes through as role/content.
func openAICompatMessages(ctx context.Context, messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages)+1)
	if sm := systemMessage(ctx); sm != nil {
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
func openAICompatTools(ctx context.Context) []map[string]interface{} {
	if omitSystem(ctx) {
		return nil
	}
	defs := toolDefs()
	tools := make([]map[string]interface{}, 0, len(defs))
	for _, td := range defs {
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
	Info(ctx context.Context, model string) (ModelInfo, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// enrichListedModels fills in the real per-model info (context window, max
// output, authoritative tool support) for a local server's name-only model
// listing by calling Info for each model. Local pickers used to skip this to
// avoid the two failure modes it caused: an unbounded hang (no per-call
// timeout) and one slow/failing model aborting the whole list. Both are
// handled here - each Info call gets its own deadline, calls run concurrently
// in a small worker pool, and a failure just leaves the bare entry in place.
func enrichListedModels(ctx context.Context, p Provider, bare []ModelInfo) []ModelInfo {
	if len(bare) == 0 {
		return bare
	}
	const (
		perCallTimeout = 8 * time.Second
		workers        = 4
	)
	out := make([]ModelInfo, len(bare))

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, b := range bare {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cctx, cancel := context.WithTimeout(ctx, perCallTimeout)
			defer cancel()
			info, err := p.Info(cctx, id)
			if err != nil {
				out[idx] = bare[idx] // fail-soft: keep the bare entry
				return
			}
			info.ID = id
			info.DisplayName = id
			out[idx] = info
		}(i, b.ID)
	}
	wg.Wait()
	return out
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
		// A mid-stream cancel can show up as a transport read error; map it
		// back to ErrCanceled so callers see the interrupt, not a generic
		// failure.
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return newProviderError(ErrCanceled, "request canceled", ctx.Err())
		}
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
