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
	// whether the loaded model supports tools; only then send a tools array.
	toolsEnabled bool
}

func newLMStudio(model string) *lmStudioProvider {
	l := &lmStudioProvider{model: model, baseURL: lmStudioBaseURL()}
	// Info reads the server's per-model capabilities first, only falling back
	// to assume-yes when they're absent - use that, not the name list, to gate.
	if info, err := l.Info(context.Background(), model); err == nil {
		l.toolsEnabled = info.SupportsTools
	} else {
		l.toolsEnabled = ModelSupportsTools("lmstudio", model)
	}
	return l
}

func NewLMStudio(model string) Provider {
	return newLMStudio(model)
}

func (l *lmStudioProvider) doRequest(ctx context.Context, method, endpoint string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, endpoint, nil, reqBody)
}

func toLMStudioMessages(messages []Message) []map[string]interface{} {
	return openAICompatMessages(messages)
}

func (l *lmStudioProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    l.model,
		"messages": toLMStudioMessages(messages),
		"stream":   false,
	}
	if l.toolsEnabled && len(tools_def) > 0 {
		reqBody["tools"] = openAICompatTools()
	}

	body, err := l.doRequest(ctx, http.MethodPost, l.baseURL+"/chat/completions", reqBody)
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
		return GenerateResult{}, fmt.Errorf("empty response from lm studio")
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

func (l *lmStudioProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	var tools []map[string]interface{}
	if l.toolsEnabled && len(tools_def) > 0 {
		tools = openAICompatTools()
	}
	return streamOpenAICompat(ctx, l.baseURL+"/chat/completions", l.model,
		nil, toLMStudioMessages(messages), tools, false, onDelta)
}

func (l *lmStudioProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokensForModel(msg, l.model)
	}
	return total, nil
}

type lmStudioModel struct {
	ID string `json:"id"`
	// Capabilities (lm studio >= 0.3.16) reports e.g. "tool_use"; absent on
	// older servers, where we fall back to assuming yes.
	Capabilities []string `json:"capabilities"`
}

func (m lmStudioModel) toModelInfo() ModelInfo {
	supportsTools := true
	if len(m.Capabilities) > 0 {
		supportsTools = hasToolCapability(m.Capabilities)
	}
	return ModelInfo{
		ID:            m.ID,
		DisplayName:   m.ID,
		SupportsTools: supportsTools,
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
