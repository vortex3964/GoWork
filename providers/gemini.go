//DESC: code for using gemini models

package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// toContents turns our Message slice into gemini's "contents" shape,
// flipping "assistant" -> "model". Tool calls become functionCall parts on a
// model turn; results become functionResponse parts on a user turn (gemini
// keys results by tool name, so the result message's ToolCallID holds it).
func toContents(messages []Message) []map[string]interface{} {
	contents := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			role := "model"
			if len(msg.ToolCalls) == 0 {
				contents = append(contents, map[string]interface{}{
					"role":  role,
					"parts": []map[string]interface{}{{"text": msg.Content}},
				})
				continue
			}
			parts := make([]map[string]interface{}, 0, len(msg.ToolCalls)+1)
			if msg.Content != "" {
				parts = append(parts, map[string]interface{}{"text": msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				parts = append(parts, map[string]interface{}{
					"functionCall": map[string]interface{}{
						"name": tc.Tool_name,
						"args": tc.Input,
					},
				})
			}
			contents = append(contents, map[string]interface{}{"role": role, "parts": parts})
		case "tool":
			// functionResponse lives in a user turn, keyed by tool name.
			contents = append(contents, map[string]interface{}{
				"role": "user",
				"parts": []map[string]interface{}{
					{
						"functionResponse": map[string]interface{}{
							"name": msg.ToolCallID,
							"response": map[string]interface{}{
								"output": msg.Content,
							},
						},
					},
				},
			})
		default:
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
	}
	return contents
}

// toGeminiTools wraps each ToolDef into gemini's {functionDeclarations:[...]}
// shape, each declaration's parameters being a JSON schema.
func toGeminiTools() []map[string]interface{} {
	decls := make([]map[string]interface{}, 0, len(tools_def))
	for _, td := range tools_def {
		decls = append(decls, map[string]interface{}{
			"name":        td.Name,
			"description": td.Description,
			"parameters":  td.InputSchema,
		})
	}
	return []map[string]interface{}{{"functionDeclarations": decls}}
}

// implementations of the Provider interface

func (g *geminiProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"contents": toContents(messages),
	}
	if system_prompt != "" {
		reqBody["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{{"text": system_prompt}},
		}
	}
	if len(tools_def) > 0 {
		reqBody["tools"] = toGeminiTools()
		reqBody["toolConfig"] = map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{"mode": "AUTO"},
		}
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
					Text     string `json:"text"`
					FuncCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
						ID   string          `json:"id"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
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

	var sb strings.Builder
	toolCalls := make([]ToolCall, 0, len(parsed.Candidates[0].Content.Parts))
	for _, part := range parsed.Candidates[0].Content.Parts {
		if part.FuncCall != nil {
			name := part.FuncCall.Name
			// gemini has no stable call id, so fall back to the tool name -
			// that's also what the functionResponse round-trip keys on.
			id := name
			if part.FuncCall.ID != "" {
				id = part.FuncCall.ID
			}
			toolCalls = append(toolCalls, ToolCall{
				Tool_call_id: id,
				Tool_name:    name,
				Input:        part.FuncCall.Args,
			})
			continue
		}
		sb.WriteString(part.Text)
	}

	return GenerateResult{
		Content:    sb.String(),
		ToolCalls:  toolCalls,
		StopReason: parsed.Candidates[0].FinishReason,
		Usage: Usage{
			PromptTokens:     parsed.UsageMetadata.PromptTokenCount,
			CompletionTokens: parsed.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      parsed.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

func (g *geminiProvider) GenerateStream(ctx context.Context, messages []Message, onDelta StreamFunc) (GenerateResult, error) {
	reqBody := map[string]interface{}{
		"contents": toContents(messages),
	}
	if system_prompt != "" {
		reqBody["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]string{{"text": system_prompt}},
		}
	}
	if len(tools_def) > 0 {
		reqBody["tools"] = toGeminiTools()
		reqBody["toolConfig"] = map[string]interface{}{
			"functionCallingConfig": map[string]interface{}{"mode": "AUTO"},
		}
	}

	url := geminiBaseURL + "/models/" + g.model + ":streamGenerateContent?alt=sse"
	resp, err := streamRequest(ctx, http.MethodPost, url, map[string]string{"x-goog-api-key": g.api_key}, reqBody)
	if err != nil {
		return GenerateResult{}, err
	}
	defer resp.Body.Close()

	var content strings.Builder
	var toolCalls []ToolCall
	usage := Usage{}
	finish := ""

	err = scanStreamLines(ctx, resp, func(line string) error {
		payload, ok := sseData(line)
		if !ok {
			return nil
		}
		var parsed struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text     string `json:"text"`
						FuncCall *struct {
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
							ID   string          `json:"id"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata *struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			return nil
		}
		if parsed.UsageMetadata != nil {
			usage = Usage{
				PromptTokens:     parsed.UsageMetadata.PromptTokenCount,
				CompletionTokens: parsed.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      parsed.UsageMetadata.TotalTokenCount,
			}
		}
		if len(parsed.Candidates) == 0 {
			return nil
		}
		c := parsed.Candidates[0]
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		for _, part := range c.Content.Parts {
			if part.FuncCall != nil {
				id := part.FuncCall.Name
				if part.FuncCall.ID != "" {
					id = part.FuncCall.ID
				}
				toolCalls = append(toolCalls, ToolCall{
					Tool_call_id: id,
					Tool_name:    part.FuncCall.Name,
					Input:        part.FuncCall.Args,
				})
				continue
			}
			if part.Text != "" {
				content.WriteString(part.Text)
				onDelta(part.Text)
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
	id := strings.TrimPrefix(gm.Name, "models/")
	return ModelInfo{
		ID:              id,
		DisplayName:     gm.DisplayName,
		ContextWindow:   gm.InputTokenLimit,
		MaxOutputTokens: gm.OutputTokenLimit,
		SupportsTools:   true,
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
