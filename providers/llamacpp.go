//DESC: code for using models served locally by llama.cpp's server (llama-server)

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// llama.cpp's server runs locally and doesn't need an api key, just a
// running `llama-server` - which defaults to localhost:8080. Respect
// LLAMACPP_HOST if it's set, same pattern as OLLAMA_HOST in ollama.go.
func llamaCppBaseURL() string {
	if host := os.Getenv("LLAMACPP_HOST"); host != "" {
		return strings.TrimSuffix(host, "/")
	}
	return "http://localhost:8080"
}

type llamaCppProvider struct {
	model   string
	baseURL string
}

func newLlamaCpp(model string) *llamaCppProvider {
	return &llamaCppProvider{model: model, baseURL: llamaCppBaseURL()}
}

func NewLlamaCpp(model string) Provider {
	return newLlamaCpp(model)
}

func (l *llamaCppProvider) doRequest(ctx context.Context, method, endpoint string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, endpoint, nil, reqBody)
}

// toLlamaCppMessages converts our provider-agnostic Message slice into the
// OpenAI-style shape llama.cpp's server expects - same shape as ollama, so
// no role translation is needed here either.
func toLlamaCppMessages(messages []Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return out
}

// implementations of the Provider interface

func (l *llamaCppProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    l.model,
		"messages": toLlamaCppMessages(messages),
		"stream":   false,
	}

	body, err := l.doRequest(ctx, http.MethodPost, l.baseURL+"/v1/chat/completions", reqBody)
	if err != nil {
		return GenerateResult{}, err
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
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
		return GenerateResult{}, fmt.Errorf("empty response from llama.cpp")
	}

	return GenerateResult{
		Content: parsed.Choices[0].Message.Content,
		Usage: Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
	}, nil
}

func (l *llamaCppProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	templateBody := map[string]interface{}{
		"messages": toLlamaCppMessages(messages),
	}
	templateResp, err := l.doRequest(ctx, http.MethodPost, l.baseURL+"/apply-template", templateBody)
	if err != nil {
		return 0, err
	}

	var templateParsed struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(templateResp, &templateParsed); err != nil {
		return 0, fmt.Errorf("failed to parse apply-template response: %w", err)
	}

	tokenizeBody := map[string]interface{}{
		"content":     templateParsed.Prompt,
		"add_special": true,
	}
	tokenizeResp, err := l.doRequest(ctx, http.MethodPost, l.baseURL+"/tokenize", tokenizeBody)
	if err != nil {
		return 0, err
	}

	var tokenizeParsed struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.Unmarshal(tokenizeResp, &tokenizeParsed); err != nil {
		return 0, fmt.Errorf("failed to parse tokenize response: %w", err)
	}

	return len(tokenizeParsed.Tokens), nil
}

func (l *llamaCppProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	propsURL := l.baseURL + "/props"
	if model != "" {
		propsURL += "?model=" + url.QueryEscape(model)
	}

	body, err := l.doRequest(ctx, http.MethodGet, propsURL, nil)
	if err != nil {
		return ModelInfo{}, err
	}

	var parsed struct {
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	ctxLen := parsed.DefaultGenerationSettings.NCtx

	return ModelInfo{
		ID:            model,
		ContextWindow: ctxLen,
		MaxOutputTokens: ctxLen,
		InputPrice:  0,
		OutputPrice: 0,
	}, nil
}

func (l *llamaCppProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	body, err := l.doRequest(ctx, http.MethodGet, l.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		info, err := l.Info(ctx, m.ID)
		if err != nil {
			return nil, fmt.Errorf("info for %s: %w", m.ID, err)
		}
		models = append(models, info)
	}

	return models, nil
}
