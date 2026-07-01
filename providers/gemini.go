//DESC: code for using gemini models

package providers

import (
    "bytes"
    "encoding/json"
    "io"
    "net/http"
	"fmt"
)

type geminiProvider struct {
	api_key string
	model  string
}

func newGemini(model string , api_key string) *geminiProvider {
	return &geminiProvider{api_key: api_key , model:model}
}

// implementations of the interface

func (g *geminiProvider) Generate(userPrompt string ,context []Message) (string, error) {
	
	contents := make([]map[string]interface{},0,len(context)+1)

	for _, msg := range context {
		role := msg.Role
		if role == "assistant" {
			role = "model" // gemini uses "model" not "assistant"
		}
		contents = append(contents, map[string]interface{}{
			"role": role,
			"parts": []map[string]string{
				{"text": msg.Content},
			},
		})
	}

	// append the new user prompt at the end
	contents = append(contents, map[string]interface{}{
		"role": "user",
		"parts": []map[string]string{
			{"text": userPrompt},
		},
	})

	reqBody := map[string]interface{}{
		"contents": contents,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal error: %w", err)
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models/" + g.model + ":generateContent"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.api_key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini error (%d): %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from gemini")
	}

	return parsed.Candidates[0].Content.Parts[0].Text, nil
}
