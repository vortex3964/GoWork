package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mapAddr(m map[string]interface{}) string {
	return fmt.Sprintf("%p", m)
}

func rawSchemaString(t *testing.T, v interface{}) string {
	t.Helper()
	raw, ok := v.(json.RawMessage)
	if !ok {
		t.Fatalf("schema has type %T, want json.RawMessage", v)
	}
	return string(raw)
}

func TestToolRepresentationsCachedPerVersion(t *testing.T) {
	preserveToolState(t)
	InitToolsDef([]ToolDef{{
		Name:        "hello",
		Description: "hello",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"hello":{"type":"string"}}}`),
	}})

	ctx := context.Background()
	open1 := openAICompatTools(ctx)
	open2 := openAICompatTools(ctx)
	if len(open1) != 1 || len(open2) != 1 {
		t.Fatalf("openai tools lengths = %d/%d, want 1/1", len(open1), len(open2))
	}
	if mapAddr(open1[0]) != mapAddr(open2[0]) {
		t.Error("openai tool map rebuilt on the second call")
	}

	gem1 := toGeminiTools()
	gem2 := toGeminiTools()
	if len(gem1) != 1 || len(gem2) != 1 || mapAddr(gem1[0]) != mapAddr(gem2[0]) {
		t.Error("gemini tools rebuilt on the second call")
	}

	ant1 := toAnthropicTools()
	ant2 := toAnthropicTools()
	if len(ant1) != 1 || len(ant2) != 1 || mapAddr(ant1[0]) != mapAddr(ant2[0]) {
		t.Error("anthropic tools rebuilt on the second call")
	}

	oll1 := toOllamaTools(ctx)
	oll2 := toOllamaTools(ctx)
	if len(oll1) != 1 || len(oll2) != 1 || mapAddr(oll1[0]) != mapAddr(oll2[0]) {
		t.Error("ollama tools rebuilt on the second call")
	}

	// The returned slice header itself must be independent, so a caller
	// appending to it cannot corrupt the cached bundle.
	open3 := openAICompatTools(ctx)
	open3 = append(open3, map[string]interface{}{"type": "injected"})
	fourth := openAICompatTools(ctx)
	if len(fourth) != 1 {
		t.Fatalf("cache was mutated through a returned slice: got %d tools, want 1", len(fourth))
	}

	toolRepresentationsMu.Lock()
	cachedVersion := uint64(0)
	if cachedToolRepresentations != nil {
		cachedVersion = cachedToolRepresentations.version
	}
	toolRepresentationsMu.Unlock()
	if cachedVersion != toolsVersion.Load() {
		t.Fatalf("cached bundle version = %d, want %d", cachedVersion, toolsVersion.Load())
	}
}

func TestToolRepresentationsInvalidatedOnUpdate(t *testing.T) {
	preserveToolState(t)
	InitToolsDef([]ToolDef{{
		Name:        "hello",
		Description: "hello",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})

	ctx := context.Background()
	before := openAICompatTools(ctx)
	beforeGemini := toGeminiTools()
	if len(before) != 1 || len(beforeGemini) != 1 {
		t.Fatalf("unexpected initial tools: openai=%d gemini=%d", len(before), len(beforeGemini))
	}

	// This is the skills-tab path: one tool's description/schema changes
	// while the version is bumped so the next request sees current skills.
	if !UpdateToolDef("hello", ToolDef{
		Name:        "hello",
		Description: "hello hello",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}) {
		t.Fatal("UpdateToolDef did not find the tool")
	}

	after := openAICompatTools(ctx)
	if len(after) != 1 {
		t.Fatalf("openai tools after update = %d, want 1", len(after))
	}
	if mapAddr(before[0]) == mapAddr(after[0]) {
		t.Error("cache was not invalidated by UpdateToolDef")
	}
	fn := after[0]["function"].(map[string]interface{})
	if fn["description"] != "hello hello" {
		t.Errorf("description = %v, want updated value", fn["description"])
	}

	afterGemini := toGeminiTools()
	decls := afterGemini[0]["functionDeclarations"].([]map[string]interface{})
	if decls[0]["description"] != "hello hello" {
		t.Error("gemini declarations were not invalidated by UpdateToolDef")
	}
	if strings.Contains(rawSchemaString(t, decls[0]["parameters"]), "additionalProperties") {
		t.Error("updated gemini schema was not sanitized")
	}

	// A full registry replacement must invalidate every provider view too.
	InitToolsDef([]ToolDef{{
		Name:        "other",
		Description: "other",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})
	replaced := openAICompatTools(ctx)
	if len(replaced) != 1 {
		t.Fatalf("openai tools after InitToolsDef = %d, want 1", len(replaced))
	}
	fn = replaced[0]["function"].(map[string]interface{})
	if fn["name"] != "other" {
		t.Errorf("tool name after InitToolsDef = %v, want other", fn["name"])
	}
	replacedGemini := toGeminiTools()
	decls = replacedGemini[0]["functionDeclarations"].([]map[string]interface{})
	if decls[0]["name"] != "other" {
		t.Errorf("gemini tool name after InitToolsDef = %v, want other", decls[0]["name"])
	}
}

func TestToolSchemasSanitizedOnceForAllProtoProviders(t *testing.T) {
	preserveToolState(t)
	schema := `{"type":"object","additionalProperties":false,"properties":{"nested":{"type":"object","additionalProperties":true}}}`
	InitToolsDef([]ToolDef{{
		Name:        "hello",
		Description: "hello",
		InputSchema: json.RawMessage(schema),
	}})

	ctx := context.Background()

	// OpenAI accepts additionalProperties, so its representation keeps the
	// original schema untouched.
	open := openAICompatTools(ctx)
	openParams := open[0]["function"].(map[string]interface{})["parameters"]
	if !strings.Contains(rawSchemaString(t, openParams), "additionalProperties") {
		t.Error("openai schema should keep additionalProperties")
	}

	gem := toGeminiTools()
	decls := gem[0]["functionDeclarations"].([]map[string]interface{})
	if got := rawSchemaString(t, decls[0]["parameters"]); strings.Contains(got, "additionalProperties") {
		t.Errorf("gemini schema not sanitized: %s", got)
	}

	ant := toAnthropicTools()
	if got := rawSchemaString(t, ant[0]["input_schema"]); strings.Contains(got, "additionalProperties") {
		t.Errorf("anthropic schema not sanitized: %s", got)
	}

	oll := toOllamaTools(ctx)
	ollFn := oll[0]["function"].(map[string]interface{})
	if got := rawSchemaString(t, ollFn["parameters"]); strings.Contains(got, "additionalProperties") {
		t.Errorf("ollama schema not sanitized: %s", got)
	}
}

func TestToolRepresentationsConcurrentAccess(t *testing.T) {
	preserveToolState(t)
	InitToolsDef([]ToolDef{{
		Name:        "hello",
		Description: "hello",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < 20; j++ {
				openAICompatTools(ctx)
				toGeminiTools()
				toAnthropicTools()
				toOllamaTools(ctx)
				UpdateToolDef("hello", ToolDef{
					Name:        "hello",
					Description: fmt.Sprintf("hello %d %d", worker, j),
					InputSchema: json.RawMessage(`{"type":"object"}`),
				})
			}
		}(i)
	}
	wg.Wait()
}
