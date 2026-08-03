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
	// whether the loaded model supports tools; only then send a tools array.
	toolsEnabled bool
}

func newLlamaCpp(model string) *llamaCppProvider {
	l := &llamaCppProvider{model: model, baseURL: llamaCppBaseURL()}
	// Info reads /props chat_template_caps.tools first (the supported way to
	// tell), falling back to assume-yes when the field is absent.
	if info, err := l.Info(context.Background(), model); err == nil {
		l.toolsEnabled = info.SupportsTools
	} else {
		l.toolsEnabled = ModelSupportsTools("llamacpp", model)
	}
	return l
}

func NewLlamaCpp(model string) Provider {
	return newLlamaCpp(model)
}

func (l *llamaCppProvider) doRequest(ctx context.Context, method, endpoint string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, endpoint, nil, reqBody)
}

// toLlamaCppMessages is llama.cpp's alias for the shared OpenAI-compatible
// lowering.
func toLlamaCppMessages(messages []Message) []map[string]interface{} {
	return openAICompatMessages(messages)
}

// implementations of the Provider interface

func (l *llamaCppProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    l.model,
		"messages": toLlamaCppMessages(messages),
		"stream":   false,
	}
	if l.toolsEnabled && len(tools_def) > 0 {
		reqBody["tools"] = openAICompatTools()
	}

	body, err := l.doRequest(ctx, http.MethodPost, l.baseURL+"/v1/chat/completions", reqBody)
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
		return GenerateResult{}, fmt.Errorf("empty response from llama.cpp")
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

func (l *llamaCppProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	var tools []map[string]interface{}
	if l.toolsEnabled && len(tools_def) > 0 {
		tools = openAICompatTools()
	}
	return streamOpenAICompat(ctx, l.baseURL+"/v1/chat/completions", l.model,
		nil, toLlamaCppMessages(messages), tools, false, onDelta)
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
		// chat_template_caps declares what the loaded model's template can
		// do (e.g. "tools":true, "parallel_tool_calls":true) - the supported
		// way to detect tool support instead of guessing by model name.
		ChatTemplateCaps struct {
			Tools *bool `json:"tools"`
		} `json:"chat_template_caps"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	ctxLen := parsed.DefaultGenerationSettings.NCtx

	supportsTools := true
	if parsed.ChatTemplateCaps.Tools != nil {
		supportsTools = *parsed.ChatTemplateCaps.Tools
	}

	return ModelInfo{
		ID:              model,
		DisplayName:     model,
		ContextWindow:   ctxLen,
		MaxOutputTokens: ctxLen,
		SupportsTools:   supportsTools,
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
		// Names only - Info-per-model made listing slow/fragile for the
		// picker. Context window is filled later when a model is selected.
		models = append(models, ModelInfo{ID: m.ID, DisplayName: m.ID})
	}

	return models, nil
}
