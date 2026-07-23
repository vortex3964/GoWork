//DESC: code for using anthropic models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const anthropicBaseURL = "https://api.anthropic.com/v1"

const anthropicVersion = "2023-06-01"

// anthropic requires this on every request and doesn't default it
// server-side the way other providers do
const anthropicDefaultMaxTokens = 4096

type anthropicProvider struct {
	api_key string
	model   string
}

func newAnthropic(model string, api_key string) *anthropicProvider {
	return &anthropicProvider{api_key: api_key, model: model}
}

// doRequest is shared by every endpoint below (generate, count tokens,
// get model, list models) so they all build/send/read requests the same
// way. anthropic uses x-api-key instead of a bearer token, and also needs
// the anthropic-version header on every call.
func (a *anthropicProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, url, map[string]string{
		"x-api-key":         a.api_key,
		"anthropic-version": anthropicVersion,
	}, reqBody)
}

// toAnthropicMessages turns our provider-agnostic Message slice into
// anthropic's messages shape. Unlike gemini/groq, anthropic has no
// "system" role in the messages array - system prompts go in a separate
// top-level field - so system messages get filtered out here and would
// need to be threaded through separately if/when we start using them.
func toAnthropicMessages(messages []Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		out = append(out, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}
	return out
}

// implementations of the Provider interface

func (a *anthropicProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":      a.model,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages":   toAnthropicMessages(messages),
	}

	url := anthropicBaseURL + "/messages"
	body, err := a.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}

	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return GenerateResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(parsed.Content) == 0 {
		return GenerateResult{}, fmt.Errorf("empty response from anthropic")
	}

	return GenerateResult{
		Content: parsed.Content[0].Text,
		Usage: Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	}, nil
}

func (a *anthropicProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	reqBody := map[string]interface{}{
		"model":    a.model,
		"messages": toAnthropicMessages(messages),
	}

	url := anthropicBaseURL + "/messages/count_tokens"
	body, err := a.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return 0, err
	}

	var parsed struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.InputTokens, nil
}

// anthropicModel mirrors the model object anthropic's API returns. Both
// Info (single model) and ListModels (all models) parse into this and
// convert it into our own ModelInfo. NOTE: max_input_tokens isn't
// consistently documented across anthropic's model endpoints, so
// ContextWindow may come back 0 depending on the model/account.
type anthropicModel struct {
	ID             string `json:"id"`
	MaxInputTokens int    `json:"max_input_tokens"`
	MaxTokens      int    `json:"max_tokens"`
}

func (am anthropicModel) toModelInfo() ModelInfo {
	return ModelInfo{
		ID:              am.ID,
		DisplayName:     am.ID,
		ContextWindow:   am.MaxInputTokens,
		MaxOutputTokens: am.MaxTokens,
	}
}

func (a *anthropicProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	url := anthropicBaseURL + "/models/" + model
	body, err := a.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelInfo{}, err
	}

	var parsed anthropicModel
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.toModelInfo(), nil
}

func (a *anthropicProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := anthropicBaseURL + "/models"
	body, err := a.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []anthropicModel `json:"data"`
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
