//DESC: code for using openai models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const openaiBaseURL = "https://api.openai.com/v1"

type openaiProvider struct {
	api_key string
	model   string
}

func newOpenAI(model string, api_key string) *openaiProvider {
	return &openaiProvider{api_key: api_key, model: model}
}

// doRequest is shared by every endpoint below (generate, get model, list
// models) so they all build/send/read requests the same way.
func (o *openaiProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, url, map[string]string{"Authorization": "Bearer " + o.api_key}, reqBody)
}

// toOpenAIMessages is the openai alias for the shared OpenAI-compatible
// lowering.
func toOpenAIMessages(messages []Message) []map[string]interface{} {
	return openAICompatMessages(messages)
}

// implementations of the Provider interface

func (o *openaiProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    o.model,
		"messages": toOpenAIMessages(messages),
	}
	if len(tools_def) > 0 {
		reqBody["tools"] = openAICompatTools()
	}

	url := openaiBaseURL + "/chat/completions"
	body, err := o.doRequest(ctx, http.MethodPost, url, reqBody)
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
		return GenerateResult{}, fmt.Errorf("empty response from openai")
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

func (o *openaiProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	var tools []map[string]interface{}
	if len(tools_def) > 0 {
		tools = openAICompatTools()
	}
	return streamOpenAICompat(ctx, openaiBaseURL+"/chat/completions", o.model,
		map[string]string{"Authorization": "Bearer " + o.api_key},
		toOpenAIMessages(messages), tools, true, onDelta)
}

// openai doesn't expose a free token-counting endpoint, so this counts tokens
// locally with the model's own BPE vocabulary (openai-compatible tiktoken).
func (o *openaiProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokensForModel(msg, o.model)
	}
	return total, nil
}

// openaiModel mirrors the model object openai's API returns. NOTE: unlike
// gemini/groq, openai's /v1/models response doesn't include context
// window or max output token limits at all - those aren't published
// through the API anywhere, so ContextWindow/MaxOutputTokens below will
// always come back 0 for now.
type openaiModel struct {
	ID string `json:"id"`
}

func (om openaiModel) toModelInfo() ModelInfo {
	return ModelInfo{
		ID:            om.ID,
		DisplayName:   om.ID,
		SupportsTools: true,
	}
}

func (o *openaiProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	url := openaiBaseURL + "/models/" + model
	body, err := o.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelInfo{}, err
	}

	var parsed openaiModel
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.toModelInfo(), nil
}

func (o *openaiProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := openaiBaseURL + "/models"
	body, err := o.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []openaiModel `json:"data"`
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
