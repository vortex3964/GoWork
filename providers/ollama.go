//DESC: code for using locally-hosted ollama models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// ollama runs locally and doesn't need an api key, just a running server -
// which defaults to localhost:11434. Respect OLLAMA_HOST if it's set,
func ollamaBaseURL() string {
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		return strings.TrimSuffix(host, "/")
	}
	return "http://localhost:11434"
}

type ollamaProvider struct {
	model   string
	baseURL string
	// toolsEnabled is false for models that can't act on tools, so we don't
	// send a tools array the server would reject with a 400.
	toolsEnabled bool
}

func newOllama(model string) *ollamaProvider {
	return &ollamaProvider{model: model, baseURL: ollamaBaseURL(), toolsEnabled: ModelSupportsTools("ollama", model)}
}

// NewOllama is a temporary exported escape hatch so callers outside this
// package can get an ollama Provider directly, since Select_provider only
// wires up gemini for now. Drop this once provider selection covers ollama
// too and go through Select_provider instead.
func NewOllama(model string) Provider {
	return newOllama(model)
}

// doRequest is shared by every endpoint below (chat, show, tags) so they
// all build/send/read requests the same way.
func (o *ollamaProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, url, nil, reqBody)
}

// toOllamaMessages turns our Message slice into ollama's chat shape.
// Assistant tool calls echo back as tool_calls; results go out as "tool".
func toOllamaMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			m := map[string]interface{}{"role": "assistant", "content": msg.Content}
			if len(msg.ToolCalls) > 0 {
				calls := make([]map[string]interface{}, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					calls = append(calls, map[string]interface{}{
						"id":       tc.Tool_call_id,
						"function": map[string]interface{}{"name": tc.Tool_name, "arguments": tc.Input},
					})
				}
				m["tool_calls"] = calls
			}
			out = append(out, m)
		case "tool":
			out = append(out, map[string]interface{}{"role": "tool", "content": msg.Content})
		default:
			out = append(out, map[string]interface{}{"role": msg.Role, "content": msg.Content})
		}
	}
	return out
}

// toOllamaTools wraps each ToolDef into ollama's openai-style tools array.
func toOllamaTools() []map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(tools_def))
	for _, td := range tools_def {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        td.Name,
				"description": td.Description,
				"parameters":  td.InputSchema,
			},
		})
	}
	return tools
}

// implementations of the Provider interface

func (o *ollamaProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    o.model,
		"messages": toOllamaMessages(messages),
		"stream":   false,
	}
	if o.toolsEnabled && len(tools_def) > 0 {
		reqBody["tools"] = toOllamaTools()
	}

	url := o.baseURL + "/api/chat"
	body, err := o.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}

	var parsed struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		DoneReason      string `json:"done_reason"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return GenerateResult{}, fmt.Errorf("failed to parse response: %w", err)
	}

	toolCalls := make([]ToolCall, 0, len(parsed.Message.ToolCalls))
	for _, tc := range parsed.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			Tool_name: tc.Function.Name,
			Input:     tc.Function.Arguments,
		})
	}

	return GenerateResult{
		Content:    parsed.Message.Content,
		ToolCalls:  toolCalls,
		StopReason: parsed.DoneReason,
		Usage: Usage{
			PromptTokens:     parsed.PromptEvalCount,
			CompletionTokens: parsed.EvalCount,
			TotalTokens:      parsed.PromptEvalCount + parsed.EvalCount,
		},
	}, nil
}

func (o *ollamaProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	// ollama doesn't have a standalone "count tokens" endpoint. The closest
	// honest equivalent is asking it to evaluate the prompt and generate as
	// little as possible, then reading prompt_eval_count back off the
	// response - num_predict: 1 caps that generation to a single token.
	reqBody := map[string]interface{}{
		"model":    o.model,
		"messages": toOllamaMessages(messages),
		"stream":   false,
		"options": map[string]interface{}{
			"num_predict": 1,
		},
	}

	url := o.baseURL + "/api/chat"
	body, err := o.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return 0, err
	}

	var parsed struct {
		PromptEvalCount int `json:"prompt_eval_count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	return parsed.PromptEvalCount, nil
}

// ollamaShowResponse mirrors the bits of /api/show we care about.
type ollamaShowResponse struct {
	Details struct {
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
		Family            string `json:"family"`
	} `json:"details"`
	ModelInfo map[string]interface{} `json:"model_info"`
}

// contextLength digs the context window out of model_info. The key is
// architecture-specific (e.g. "llama.context_length" for smollm2,
// "qwen2.context_length" for qwen2.5), so it's built from
// general.architecture instead of being hardcoded per model family.
func (r ollamaShowResponse) contextLength() int {
	arch, _ := r.ModelInfo["general.architecture"].(string)
	if arch == "" {
		return 0
	}
	if v, ok := r.ModelInfo[arch+".context_length"].(float64); ok {
		return int(v)
	}
	return 0
}

func (o *ollamaProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	reqBody := map[string]interface{}{
		"model": model,
		"name":  model, // some ollama versions still expect "name" instead
	}

	url := o.baseURL + "/api/show"
	body, err := o.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return ModelInfo{}, err
	}

	var parsed ollamaShowResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ModelInfo{}, fmt.Errorf("failed to parse response: %w", err)
	}

	ctxLen := parsed.contextLength()

	return ModelInfo{
		ID:            model,
		DisplayName:   model,
		ContextWindow: ctxLen,
		SupportsTools: ModelSupportsTools("ollama", model),
		// ollama doesn't cap output tokens separately from the context
		// window - generation just keeps going until it fills whatever
		// context room is left, so we report the same number here.
		MaxOutputTokens: ctxLen,
	}, nil
}

func (o *ollamaProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := o.baseURL + "/api/tags"
	body, err := o.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		// Names only - calling /api/show per model made the picker hang
		// (and used to abort the whole list on a single failure). Context
		// window is filled later via Info when a model is selected.
		models = append(models, ModelInfo{ID: m.Name, DisplayName: m.Name, SupportsTools: ModelSupportsTools("ollama", m.Name)})
	}

	return models, nil
}
