//DESC: code for using models served locally by LM Studio (OpenAI-compatible API)

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// LM Studio runs locally and doesn't need an api key, just a running server.
// Defaults to localhost:1234/v1. Respect LMSTUDIO_HOST if set.
func lmStudioBaseURL() string {
	if host := os.Getenv("LMSTUDIO_HOST"); host != "" {
		return strings.TrimSuffix(host, "/")
	}
	return "http://localhost:1234/v1"
}

type lmStudioProvider struct {
	model   string
	baseURL string
}

func newLMStudio(model string) *lmStudioProvider {
	return &lmStudioProvider{model: model, baseURL: lmStudioBaseURL()}
}

func NewLMStudio(model string) Provider {
	return newLMStudio(model)
}

func (l *lmStudioProvider) doRequest(ctx context.Context, method, endpoint string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, endpoint, nil, reqBody)
}

func toLMStudioMessages(messages []Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return out
}

func (l *lmStudioProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    l.model,
		"messages": toLMStudioMessages(messages),
		"stream":   false,
	}

	body, err := l.doRequest(ctx, http.MethodPost, l.baseURL+"/chat/completions", reqBody)
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
		return GenerateResult{}, fmt.Errorf("empty response from lm studio")
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

func (l *lmStudioProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Content)
	}
	return chars / 4, nil
}

type lmStudioModel struct {
	ID string `json:"id"`
}

func (m lmStudioModel) toModelInfo() ModelInfo {
	return ModelInfo{
		ID:          m.ID,
		DisplayName: m.ID,
	}
}

func (l *lmStudioProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	// LM Studio's /models/{id} may not expose context limits; list and match.
	models, err := l.ListModels(ctx)
	if err != nil {
		return ModelInfo{}, err
	}
	for _, m := range models {
		if m.ID == model {
			return m, nil
		}
	}
	return ModelInfo{ID: model, DisplayName: model}, nil
}

func (l *lmStudioProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	body, err := l.doRequest(ctx, http.MethodGet, l.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []lmStudioModel `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		models = append(models, m.toModelInfo())
	}
	return models, nil
}
