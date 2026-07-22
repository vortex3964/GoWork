// DESC: this file contains the provider interface wich every
// other ai supported will inherit and also contains common functions
// every supported ai will use

//TODO:make context building to send for the api faster instead of having to rebuild
//it every time for every new prompt inside the generate function witch currently is
//only in the gemini file

//TODO: add tool calls to the llm

//TODO: properly parce the llms response right now its scuffed

// IMPORTANT: we should handle retries or handle high server demands etch
package providers

//TODO:maybe we should limit the output tokens of non local models
// so that we dont burn all of our api money

//supported: 
//gemini
//groq

//local:
//ollama
//llama.cpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

//used to model the messages in the context window may change in the future
//to be better suited for messages for code
type Message struct{
	Role string
	Content string
}

type ModelInfo struct {
    ContextWindow int
    MaxOutputTokens int
}

type Usage struct {
    PromptTokens int
    CompletionTokens int
    TotalTokens int
}

type GenerateResult struct {
    Content string
    Usage Usage
}

//NOTE: since ai is stateless and we send all the context everytime then
//just pass it by reference in the Generate function
type Provider interface {
	Generate(ctx context.Context, messages []Message) (GenerateResult, error)
	EstimateTokens(ctx context.Context, messages []Message) (int, error)
	Info(ctx context.Context, model string) (ModelInfo, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

//TODO: the errors we will have to acount for
type ErrorKind int
const (
    ErrUnknown ErrorKind = iota
    ErrRateLimited
    ErrContextExceeded
    ErrAuthFailed
    ErrInvalidRequest
    ErrServerOverloaded
    ErrTimeout
    ErrCanceled
)

type ProviderError struct {
	Kind    ErrorKind
	Message string
	Err     error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

func newProviderError(kind ErrorKind, msg string, err error) *ProviderError {
	return &ProviderError{Kind: kind, Message: msg, Err: err}
}

func classifyHTTPStatus(status int) ErrorKind {
	switch {
	case status == 401 || status == 403:
		return ErrAuthFailed
	case status == 429:
		return ErrRateLimited
	case status == 400 || status == 422:
		return ErrInvalidRequest
	case status == 408:
		return ErrTimeout
	case status >= 500:
		return ErrServerOverloaded
	default:
		return ErrUnknown
	}
}

func doJSONRequest(ctx context.Context, method, url string, headers map[string]string, reqBody interface{}) ([]byte, error) {
	var reader io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return nil, newProviderError(ErrInvalidRequest, "failed to marshal request body", err)
		}
		reader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, newProviderError(ErrInvalidRequest, "failed to build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, newProviderError(ErrCanceled, "request canceled", err)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, newProviderError(ErrTimeout, "request timed out", err)
		default:
			return nil, newProviderError(ErrUnknown, "request failed", err)
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newProviderError(ErrUnknown, "failed to read response body", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, newProviderError(classifyHTTPStatus(resp.StatusCode),
			fmt.Sprintf("%s returned %d", url, resp.StatusCode), fmt.Errorf("%s", body))
	}

	return body, nil
}

type retryingProvider struct {
	inner      Provider
	maxRetries int
}

func WithRetries(p Provider, maxRetries int) Provider {
	return &retryingProvider{inner: p, maxRetries: maxRetries}
}

func (r *retryingProvider) Generate(ctx context.Context, messages []Message) (GenerateResult, error) {
	var lastErr error
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		result, err := r.inner.Generate(ctx, messages)
		if err == nil {
			return result, nil
		}
		lastErr = err

		var perr *ProviderError
		if !errors.As(err, &perr) || !retryable(perr.Kind) {
			return GenerateResult{}, err
		}
		select {
		case <-ctx.Done():
			return GenerateResult{}, ctx.Err()
		case <-time.After(backoff(attempt)):
		}
	}
	return GenerateResult{}, lastErr
}

func (r *retryingProvider) EstimateTokens(ctx context.Context, messages []Message) (int, error) {
	return r.inner.EstimateTokens(ctx, messages)
}

func (r *retryingProvider) Info(ctx context.Context, model string) (ModelInfo, error) {
	return r.inner.Info(ctx, model)
}

func (r *retryingProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return r.inner.ListModels(ctx)
}

func retryable(k ErrorKind) bool {
	switch k {
	case ErrRateLimited, ErrServerOverloaded, ErrTimeout:
		return true
	default:
		return false
	}
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<attempt) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

//selects an ai provider (model) and returns it to the main loop
// curently it still only works for gemini
func Select_provider(model string , api_key string) (Provider , error){
	if api_key == ""{
		return nil , fmt.Errorf("Empty api key")
	}

	return WithRetries(newGemini(model, api_key), 3) , nil
}

func ExportContext(context []Message) error {
	var sb strings.Builder
	for _, msg := range context {
		sb.WriteString(fmt.Sprintf("[%s]\n%s\n\n", strings.ToUpper(msg.Role), msg.Content))
	}

	return os.WriteFile("context.txt", []byte(sb.String()), 0644)
}
