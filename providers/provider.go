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
	"context"
	"fmt"
	"os"
	"strings"
)

//used to model the messages in the context window may change in the future
//to be better suited for messages for code
type Message struct{
	Role string
	Content string
}

type ModelInfo struct {
    ID string
    ContextWindow int
    MaxOutputTokens int
    InputPrice float64
    OutputPrice float64
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

//selects an ai provider (model) and returns it to the main loop
// curently it still only works for gemini
func Select_provider(model string , api_key string) (Provider , error){
	if api_key == ""{
		return nil , fmt.Errorf("Empty api key")
	}

	return newGemini(model,api_key) , nil
}

func ExportContext(context []Message) error {
	var sb strings.Builder
	for _, msg := range context {
		sb.WriteString(fmt.Sprintf("[%s]\n%s\n\n", strings.ToUpper(msg.Role), msg.Content))
	}

	return os.WriteFile("context.txt", []byte(sb.String()), 0644)
}
