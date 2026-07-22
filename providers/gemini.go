//DESC: code for using gemini models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type geminiProvider struct {
	api_key string
	model   string
}

func newGemini(model string, api_key string) *geminiProvider {
	return &geminiProvider{api_key: api_key, model: model}
}

// doRequest is shared by every endpoint below (generate, countTokens, get
// model, list models) so they all build/send/read requests the same way.
func (g *geminiProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, url, map[string]string{"x-goog-api-key": g.api_key}, reqBody)
}

// toContents turns our provider-agnostic Message slice into gemini's
// "contents" shape, flipping "assistant" -> "model" since that's what
// gemini calls it.
func toContents(messages []Message) []map[string]interface{} {
	contents := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]interface{}{
			"role": role,
			"parts": []map[string]string{
				{"text": msg.Content},
			},
		})
	}
	return contents
}

// implementations of the Provider interface

func (g *geminiProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"contents": toContents(messages),
	}

	url := geminiBaseURL + "/models/" + g.model + ":generateContent"
	body, err := g.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return GenerateResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return GenerateResult{}, fmt.Errorf("empty response from gemini")
	}

	return GenerateResult{
		Content: parsed.Candidates[0].Content.Parts[0].Text,
		Usage: Usage{
			PromptTokens:     parsed.UsageMetadata.PromptTokenCount,
			CompletionTokens: parsed.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      parsed.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

func (g *geminiProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	reqBody := map[string]interface{}{
		"contents": toContents(messages),
	}

	url := geminiBaseURL + "/models/" + g.model + ":countTokens"
	body, err := g.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return 0, err
	}

	var parsed struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.TotalTokens, nil
}

// geminiModel mirrors the model object gemini's API returns. Both Info
// (single model) and ListModels (all models) parse into this and convert
// it into our own ModelInfo.
type geminiModel struct {
	Name             string `json:"name"` // comes back as "models/gemini-2.5-flash"
	DisplayName      string `json:"displayName"`
	Description      string `json:"description"`
	InputTokenLimit  int    `json:"inputTokenLimit"`
	OutputTokenLimit int    `json:"outputTokenLimit"`
}

func (gm geminiModel) toModelInfo() ModelInfo {
	return ModelInfo{
		ContextWindow:   gm.InputTokenLimit,
		MaxOutputTokens: gm.OutputTokenLimit,
	}
}

func (g *geminiProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	url := geminiBaseURL + "/models/" + model
	body, err := g.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelInfo{}, err
	}

	var parsed geminiModel
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.toModelInfo(), nil
}

func (g *geminiProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := geminiBaseURL + "/models"
	body, err := g.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Models []geminiModel `json:"models"`
		//TODO: gemini paginates this past a certain model count via
		//nextPageToken - not handled yet, so very large lists get cut off.
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		models = append(models, m.toModelInfo())
	}

	return models, nil
}
