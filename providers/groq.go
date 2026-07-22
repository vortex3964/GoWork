//DESC: code for using groq models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const groqBaseURL = "https://api.groq.com/openai/v1"

type groqProvider struct {
	api_key string
	model   string
}

func newGroq(model string, api_key string) *groqProvider {
	return &groqProvider{api_key: api_key, model: model}
}

// doRequest is shared by every endpoint below (generate, get model, list
// models) so they all build/send/read requests the same way.
func (g *groqProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, url, map[string]string{"Authorization": "Bearer " + g.api_key}, reqBody)
}

// toGroqMessages turns our provider-agnostic Message slice into groq's
// OpenAI-compatible messages shape, which is a straight passthrough since
// groq already uses "system"/"user"/"assistant" role names.
func toGroqMessages(messages []Message) []map[string]string {
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

func (g *groqProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    g.model,
		"messages": toGroqMessages(messages),
	}

	url := groqBaseURL + "/chat/completions"
	body, err := g.doRequest(ctx, http.MethodPost, url, reqBody)
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
		return GenerateResult{}, fmt.Errorf("empty response from groq")
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

//TODO: find a better function for estimating tokens

// groq doesn't expose a dedicated token-counting endpoint like gemini's
// countTokens, so this falls back to a rough estimate until something
// better is wired up.
func (g *groqProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Content)
	}
	return chars / 4, nil
}

// groqModel mirrors the model object groq's API returns. Both Info
// (single model) and ListModels (all models) parse into this and convert
// it into our own ModelInfo.
type groqModel struct {
	ID                 string `json:"id"`
	ContextWindow      int    `json:"context_window"`
	MaxCompletionTokens int   `json:"max_completion_tokens"`
}

func (gm groqModel) toModelInfo() ModelInfo {
	return ModelInfo{
		ContextWindow:   gm.ContextWindow,
		MaxOutputTokens: gm.MaxCompletionTokens,
	}
}

func (g *groqProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	url := groqBaseURL + "/models/" + model
	body, err := g.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelInfo{}, err
	}

	var parsed groqModel
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.toModelInfo(), nil
}

func (g *groqProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := groqBaseURL + "/models"
	body, err := g.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []groqModel `json:"data"`
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
