//DESC: code for using anthropic models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// toAnthropicMessages turns our Message slice into anthropic's messages shape.
// Anthropic has no "system" role (system prompt is a separate top-level field),
// no "tool" results, and no assistant tool_calls - a call is an assistant
// "tool_use" content block and its result is a "tool_result" block in a user
// message. We lower both back to that shape.
func toAnthropicMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			continue
		case "assistant":
			// Echo tool calls as tool_use blocks (text must come first).
			if len(msg.ToolCalls) == 0 {
				out = append(out, map[string]interface{}{"role": "assistant", "content": msg.Content})
				continue
			}
			blocks := make([]map[string]interface{}, 0, len(msg.ToolCalls)+1)
			if msg.Content != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.Tool_call_id,
					"name":  tc.Tool_name,
					"input": tc.Input,
				})
			}
			out = append(out, map[string]interface{}{"role": "assistant", "content": blocks})
		case "tool":
			// Generic "tool" result lowers into a user-role tool_result block
			// tied to the call by tool_use_id.
			out = append(out, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": msg.ToolCallID,
						"content":     []map[string]string{{"type": "text", "text": msg.Content}},
					},
				},
			})
		default:
			out = append(out, map[string]interface{}{"role": msg.Role, "content": msg.Content})
		}
	}
	return out
}

// toAnthropicTools maps ToolDefs into anthropic's flat {name, description,
// input_schema} tools array (no "type"/"function" wrapper).
func toAnthropicTools() []map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(tools_def))
	for _, td := range tools_def {
		tools = append(tools, map[string]interface{}{
			"name":         td.Name,
			"description":  td.Description,
			"input_schema": toolSchemaForProto(td.InputSchema),
		})
	}
	return tools
}

// implementations of the Provider interface

func (a *anthropicProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":      a.model,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages":   toAnthropicMessages(messages),
	}
	if system_prompt != "" && !omitSystem(ctx) {
		reqBody["system"] = system_prompt
	}
	if len(tools_def) > 0 && !omitSystem(ctx) {
		reqBody["tools"] = toAnthropicTools()
	}

	url := anthropicBaseURL + "/messages"
	body, err := a.doRequest(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}

	var parsed struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
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

	var sb strings.Builder
	toolCalls := make([]ToolCall, 0, len(parsed.Content))
	for _, block := range parsed.Content {
		switch block.Type {
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				Tool_call_id: block.ID,
				Tool_name:    block.Name,
				// anthropic sends args as an already-parsed JSON object.
				Input: block.Input,
			})
		default: // "text"
			sb.WriteString(block.Text)
		}
	}

	return GenerateResult{
		Content:    sb.String(),
		ToolCalls:  toolCalls,
		StopReason: parsed.StopReason,
		Usage: Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	}, nil
}

func (a *anthropicProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"model":      a.model,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages":   toAnthropicMessages(messages),
		"stream":     true,
	}
	if system_prompt != "" && !omitSystem(ctx) {
		reqBody["system"] = system_prompt
	}
	if len(tools_def) > 0 && !omitSystem(ctx) {
		reqBody["tools"] = toAnthropicTools()
	}

	resp, err := streamRequest(ctx, http.MethodPost, anthropicBaseURL+"/messages", map[string]string{
		"x-api-key":         a.api_key,
		"anthropic-version": anthropicVersion,
	}, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}
	defer resp.Body.Close()

	var content strings.Builder
	usage := Usage{}
	finish := ""
	// Block state per index: text blocks append to content; tool_use blocks
	// accumulate their closing JSON in args.
	type anBlock struct {
		isTool bool
		id     string
		name   string
		args   strings.Builder
	}
	blocks := map[int]*anBlock{}

	err = scanStreamLines(ctx, resp, func(line string) error {
		payload, ok := sseData(line)
		if !ok {
			return nil
		}
		var ev struct {
			Type string `json:"type"`
			Index *int  `json:"index"`
			ContentBlock *struct {
				Type string          `json:"type"`
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Text string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message *struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil
		}
		if ev.Message != nil && ev.Message.Usage != nil {
			usage = Usage{
				PromptTokens:     ev.Message.Usage.InputTokens,
				CompletionTokens: ev.Message.Usage.OutputTokens,
				TotalTokens:      ev.Message.Usage.InputTokens + ev.Message.Usage.OutputTokens,
			}
		}
		switch ev.Type {
		case "content_block_start":
			idx := 0
			if ev.Index != nil {
				idx = *ev.Index
			}
			b := &anBlock{}
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				b.isTool = true
				b.id = ev.ContentBlock.ID
				b.name = ev.ContentBlock.Name
			}
			blocks[idx] = b
		case "content_block_delta":
			idx := 0
			if ev.Index != nil {
				idx = *ev.Index
			}
			b := blocks[idx]
			if b == nil {
				b = &anBlock{}
				blocks[idx] = b
			}
			if ev.Delta == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				b.isTool = false
				content.WriteString(ev.Delta.Text)
				onDelta(ev.Delta.Text)
			case "input_json_delta":
				b.isTool = true
				b.args.WriteString(ev.Delta.PartialJSON)
			}
		}
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			finish = ev.Delta.StopReason
		}
		return nil
	})
	if err != nil {
		return GenerateResult{}, err
	}

	var toolCalls []ToolCall
	for _, b := range blocks {
		if b == nil || !b.isTool {
			continue
		}
		toolCalls = append(toolCalls, ToolCall{
			Tool_call_id: b.id,
			Tool_name:    b.name,
			Input:        rawArgs(b.args.String()),
		})
	}

	return GenerateResult{
		Content:    content.String(),
		ToolCalls:  toolCalls,
		StopReason: finish,
		Usage:      usage,
	}, nil
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
		SupportsTools:   true,
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
