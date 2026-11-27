//DESC: code for using OpenCode Go subscription models. OpenCode Go exposes an
// OpenAI-compatible chat completions endpoint plus its own model listing with
// context-window and tool-capability metadata.

package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	opencodeGoDefaultBaseURL = "https://opencode.ai/inference/go"
	opencodeGoUserAgent      = "gowork/1.0"
)

// opencodeGoBaseURL returns the gateway base URL, honoring OPENCODE_BASE_URL
// so tests (and self-hosted gateways) can point at a different host.
func opencodeGoBaseURL() string {
	if base := strings.TrimSpace(os.Getenv("OPENCODE_BASE_URL")); base != "" {
		return strings.TrimSuffix(base, "/")
	}
	return opencodeGoDefaultBaseURL
}

type opencodeGoProvider struct {
	api_key string
	model   string
	baseURL string
	// sessionID is the stable conversation id sent on every request. It is
	// generated once per provider instance; callers can override it per
	// conversation with WithOpenCodeSession.
	sessionID string
}

func newOpenCodeGo(model string, api_key string) *opencodeGoProvider {
	return &opencodeGoProvider{
		api_key:   api_key,
		model:     model,
		baseURL:   opencodeGoBaseURL(),
		sessionID: newOpenCodeSessionID(),
	}
}

// newOpenCodeSessionID generates a random, stable-per-provider conversation
// id in the form OpenCode expects for x-opencode-session.
func newOpenCodeSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("gowork-%d", time.Now().UnixNano())
	}
	return "gowork-" + hex.EncodeToString(b[:])
}

// opencodeSessionKey carries a caller-supplied conversation id.
type opencodeSessionKey struct{}

// WithOpenCodeSession returns a context carrying a stable OpenCode session
// id. Coding-agent requests should reuse one id for the whole conversation.
// An empty id is ignored, in which case the provider's own id is used.
func WithOpenCodeSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, opencodeSessionKey{}, id)
}

// sessionIDFor returns the conversation id for one request: the context id
// when set, otherwise the provider instance's stable id.
func (p *opencodeGoProvider) sessionIDFor(ctx context.Context) string {
	if id, ok := ctx.Value(opencodeSessionKey{}).(string); ok {
		if id = strings.TrimSpace(id); id != "" {
			return id
		}
	}
	return p.sessionID
}

// headers builds the headers every OpenCode Go request must carry. The
// Content-Type is set by the request helpers.
func (p *opencodeGoProvider) headers(ctx context.Context) map[string]string {
	return map[string]string{
		"Authorization":      "Bearer " + p.api_key,
		"User-Agent":         opencodeGoUserAgent,
		"x-opencode-session": p.sessionIDFor(ctx),
	}
}

func (p *opencodeGoProvider) chatCompletionsURL() string {
	return p.baseURL + "/openai/v1/chat/completions"
}

func (p *opencodeGoProvider) modelsURL() string {
	return p.baseURL + "/v1/models"
}

// opencodeGoHTTPError maps an OpenCode Go HTTP status onto our ErrorKinds.
// The response body is preserved verbatim (wrapped as Err) so entitlement
// messages from 401/403 responses reach the user.
func opencodeGoHTTPError(url string, status int, body []byte) *ProviderError {
	kind := ErrUnknown
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = ErrAuthFailed
	case status == http.StatusTooManyRequests:
		kind = ErrRateLimited
	case status >= 500:
		kind = ErrServerOverloaded
	}

	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(status)
	}
	return newProviderError(kind, fmt.Sprintf("%s returned %d", url, status), errors.New(detail))
}

// doRequest is shared by Generate, Info and ListModels so every JSON request
// carries the required OpenCode headers and error mapping.
func (p *opencodeGoProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
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
	for k, v := range p.headers(ctx) {
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
		return nil, opencodeGoHTTPError(url, resp.StatusCode, body)
	}
	return body, nil
}

// doStreamRequest is the streaming counterpart of doRequest. It uses the
// no-timeout client and leaves the response body open for the stream parser.
func (p *opencodeGoProvider) doStreamRequest(ctx context.Context, url string, reqBody interface{}) (*http.Response, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, newProviderError(ErrInvalidRequest, "failed to marshal request body", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, newProviderError(ErrInvalidRequest, "failed to build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range p.headers(ctx) {
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
		return nil, opencodeGoHTTPError(url, resp.StatusCode, body)
	}
	return resp, nil
}

// implementations of the Provider interface

func (p *opencodeGoProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    p.model,
		"messages": openAICompatMessages(ctx, messages),
	}
	// Only add the key when there is something to send: compaction requests
	// (WithoutSystem) omit tools, and assigning the key unconditionally used
	// to put "tools": null on the wire.
	if tools := openAICompatTools(ctx); len(tools) > 0 {
		reqBody["tools"] = tools
	}

	body, err := p.doRequest(ctx, http.MethodPost, p.chatCompletionsURL(), reqBody)
	if err != nil {
		return GenerateResult{}, err
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return GenerateResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return GenerateResult{}, fmt.Errorf("empty response from opencode go")
	}

	msg := parsed.Choices[0].Message
	toolCalls := make([]ToolCall, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			Tool_call_id: tc.ID,
			Tool_name:    tc.Function.Name,
			Input:        rawArgs(tc.Function.Arguments),
		})
	}

	return GenerateResult{
		Content:    msg.Content,
		ToolCalls:  toolCalls,
		StopReason: parsed.Choices[0].FinishReason,
		Usage: Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
	}, nil
}

func (p *opencodeGoProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	var tools []map[string]interface{}
	if hasTools() {
		tools = openAICompatTools(ctx)
	}

	reqBody := map[string]interface{}{
		"model":    p.model,
		"messages": openAICompatMessages(ctx, messages),
		"stream":   true,
		"stream_options": map[string]interface{}{
			"include_usage": true,
		},
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}

	resp, err := p.doStreamRequest(ctx, p.chatCompletionsURL(), reqBody)
	if err != nil {
		return GenerateResult{}, err
	}
	defer resp.Body.Close()

	return parseOpenAICompatStream(ctx, resp, onDelta)
}

// opencodeGoModel mirrors one entry in OpenCode Go's /v1/models response.
type opencodeGoModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// Capabilities reports per-capability support; tools.supported is the
	// authoritative "can call tools" flag.
	Capabilities struct {
		Tools struct {
			Supported bool `json:"supported"`
		} `json:"tools"`
	} `json:"capabilities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

func (m opencodeGoModel) toModelInfo() ModelInfo {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = m.ID
	}
	return ModelInfo{
		ID:              m.ID,
		DisplayName:     name,
		ContextWindow:   m.Limit.Context,
		MaxOutputTokens: m.Limit.Output,
		SupportsTools:   m.Capabilities.Tools.Supported,
	}
}

func (p *opencodeGoProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	body, err := p.doRequest(ctx, http.MethodGet, p.modelsURL(), nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []opencodeGoModel `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		// The gateway may list non-chat entries (embeddings, etc.); only
		// chat models are usable by the chat completions endpoint.
		if !strings.EqualFold(strings.TrimSpace(m.Type), "chat") {
			continue
		}
		models = append(models, m.toModelInfo())
	}
	return models, nil
}

// Info looks the model up in the live model list so context window and tool
// support come from the gateway's authoritative metadata.
func (p *opencodeGoProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	models, err := p.ListModels(ctx)
	if err != nil {
		return ModelInfo{}, err
	}
	for _, m := range models {
		if m.ID == model {
			return m, nil
		}
	}
	// Unknown (or newly-added) model: keep callers working with a bare entry.
	return ModelInfo{
		ID:            model,
		DisplayName:   model,
		SupportsTools: ModelSupportsTools("opencode-go", model),
	}, nil
}
