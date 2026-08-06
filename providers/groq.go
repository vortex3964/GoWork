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

// toGroqMessages is groq's alias for the shared OpenAI-compatible lowering.
func toGroqMessages(ctx context.Context, messages []Message) []map[string]interface{} {
	return openAICompatMessages(ctx, messages)
}

// implementations of the Provider interface

func (g *groqProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    g.model,
		"messages": toGroqMessages(ctx, messages),
	}
	if len(tools_def) > 0 {
		reqBody["tools"] = openAICompatTools(ctx)
	}

	url := groqBaseURL + "/chat/completions"
	body, err := g.doRequest(ctx, http.MethodPost, url, reqBody)
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
		return GenerateResult{}, fmt.Errorf("empty response from groq")
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

func (g *groqProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	var tools []map[string]interface{}
	if len(tools_def) > 0 {
		tools = openAICompatTools(ctx)
	}
	return streamOpenAICompat(ctx, groqBaseURL+"/chat/completions", g.model,
		map[string]string{"Authorization": "Bearer " + g.api_key},
		toGroqMessages(ctx, messages), tools, true, onDelta)
}

//TODO: find a better function for estimating tokens

// groqModel mirrors the model object groq's API returns. Both Info
// (single model) and ListModels (all models) parse into this and convert
// it into our own ModelInfo.
type groqModel struct {
	ID                  string `json:"id"`
	ContextWindow       int    `json:"context_window"`
	MaxCompletionTokens int    `json:"max_completion_tokens"`
}

func (gm groqModel) toModelInfo() ModelInfo {
	return ModelInfo{
		ID:              gm.ID,
		DisplayName:     gm.ID,
		ContextWindow:   gm.ContextWindow,
		MaxOutputTokens: gm.MaxCompletionTokens,
		SupportsTools:   ModelSupportsTools("groq", gm.ID),
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
