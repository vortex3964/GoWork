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
	o := &ollamaProvider{model: model, baseURL: ollamaBaseURL()}
	// Ask the server for real capabilities; Info already prefers the
	// /api/show "capabilities" array and only falls back to the name
	// allowlist when the server doesn't report one. Don't gate tools on the
	// static list alone.
	if info, err := o.Info(context.Background(), model); err == nil {
		o.toolsEnabled = info.SupportsTools
	} else {
		o.toolsEnabled = ModelSupportsTools("ollama", model)
	}
	return o
}

// doRequest is shared by every endpoint below (chat, show, tags) so they
// all build/send/read requests the same way.
func (o *ollamaProvider) doRequest(ctx context.Context, method, url string, reqBody interface{}) ([]byte, error) {
	return doJSONRequest(ctx, method, url, nil, reqBody)
}

// toOllamaMessages turns our Message slice into ollama's chat shape.
// Assistant tool calls echo back as tool_calls; results go out as "tool".
func toOllamaMessages(ctx context.Context, messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages)+1)
	if sm := systemMessage(ctx); sm != nil {
		out = append(out, map[string]interface{}{"role": "system", "content": sm.Content})
	}
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
			// Newer ollama servers require tool messages to name the call
			// they answer (tool_call_id), matching the id echoed back in the
			// assistant's tool_calls above. Older versions ignore unknown
			// fields, so sending it unconditionally is safe.
			out = append(out, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      msg.Content,
			})
		default:
			out = append(out, map[string]interface{}{"role": msg.Role, "content": msg.Content})
		}
	}
	return out
}

// toOllamaTools wraps each ToolDef into ollama's openai-style tools array.
func toOllamaTools(ctx context.Context) []map[string]interface{} {
	if omitSystem(ctx) {
		return nil
	}
	defs := toolDefs()
	tools := make([]map[string]interface{}, 0, len(defs))
	for _, td := range defs {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        td.Name,
				"description": td.Description,
				"parameters":  toolSchemaForProto(td.InputSchema),
			},
		})
	}
	return tools
}

// implementations of the Provider interface

func (o *ollamaProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    o.model,
		"messages": toOllamaMessages(ctx, messages),
		"stream":   false,
	}
	if o.toolsEnabled && hasTools() {
		reqBody["tools"] = toOllamaTools(ctx)
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
				ID       string `json:"id"`
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
		id := tc.ID
		// Some ollama versions (and many small models) don't return an id on
		// tool_calls. The tool-result round-trip keys on ToolCallID, so
		// synthesize one rather than echoing an empty id back on the next
		// turn (which some servers reject or fail to match).
		if id == "" {
			id = nextTextCallID()
		}
		toolCalls = append(toolCalls, ToolCall{
			Tool_call_id: id,
			Tool_name:    tc.Function.Name,
			Input:        tc.Function.Arguments,
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

func (o *ollamaProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":    o.model,
		"messages": toOllamaMessages(ctx, messages),
		"stream":   true,
	}
	if o.toolsEnabled && hasTools() {
		reqBody["tools"] = toOllamaTools(ctx)
	}

	resp, err := streamRequest(ctx, http.MethodPost, o.baseURL+"/api/chat", nil, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}
	defer resp.Body.Close()

	var content strings.Builder
	usage := Usage{}
	finish := ""
	var toolCalls []ToolCall

	err = scanStreamLines(ctx, resp, func(line string) error {
		var chunk struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			Done            bool   `json:"done"`
			DoneReason      string `json:"done_reason"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return nil
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
			onDelta(chunk.Message.Content)
		}
		if chunk.Done {
			finish = chunk.DoneReason
			usage = Usage{
				PromptTokens:     chunk.PromptEvalCount,
				CompletionTokens: chunk.EvalCount,
				TotalTokens:      chunk.PromptEvalCount + chunk.EvalCount,
			}
			for _, tc := range chunk.Message.ToolCalls {
				id := tc.ID
				if id == "" {
					id = nextTextCallID()
				}
				toolCalls = append(toolCalls, ToolCall{
					Tool_call_id: id,
					Tool_name:    tc.Function.Name,
					Input:        tc.Function.Arguments,
				})
			}
		}
		return nil
	})
	if err != nil {
		return GenerateResult{}, err
	}

	return GenerateResult{
		Content:    content.String(),
		ToolCalls:  toolCalls,
		StopReason: finish,
		Usage:      usage,
	}, nil
}

// ollamaShowResponse mirrors the bits of /api/show we care about.
type ollamaShowResponse struct {
	Details struct {
		ParameterSize     string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
		Family            string `json:"family"`
	} `json:"details"`
	ModelInfo map[string]interface{} `json:"model_info"`
	// Capabilities is ollama's authoritative tool-calling signal (present
	// after the model has been loaded once).
	Capabilities []string `json:"capabilities"`
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

	supportsTools := hasToolCapability(parsed.Capabilities)
	// Older ollama servers (or freshly-pulled, not-yet-loaded models) may not
	// report capabilities yet; fall back to the family allowlist then, so we
	// don't wrongly claim a model can't use tools.
	if !supportsTools && len(parsed.Capabilities) == 0 {
		supportsTools = ModelSupportsTools("ollama", model)
	}

	return ModelInfo{
		ID:            model,
		DisplayName:   model,
		ContextWindow: ctxLen,
		SupportsTools: supportsTools,
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
			// Capabilities is authoritative when present; absent on older
			// servers, where we fall back to the family allowlist.
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	models := make([]ModelInfo, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		// Names only - calling /api/show per model made the picker hang
		// (and used to abort the whole list on a single failure).
		supportsTools := hasToolCapability(m.Capabilities)
		if !supportsTools && len(m.Capabilities) == 0 {
			supportsTools = ModelSupportsTools("ollama", m.Name)
		}
		models = append(models, ModelInfo{ID: m.Name, DisplayName: m.Name, SupportsTools: supportsTools})
	}

	// Fill in context window (and authoritative tool support) per model,
	// concurrently and fail-soft, so the picker shows the same info the
	// cloud providers show instead of bare names.
	return enrichListedModels(ctx, o, models), nil
}
