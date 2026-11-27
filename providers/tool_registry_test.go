package providers

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestAddAndRemoveToolDef(t *testing.T) {
	preserveToolState(t)
	InitToolsDef(nil)

	before := toolsVersion.Load()
	if !AddToolDef(ToolDef{Name: "mcp_files_read", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}) {
		t.Fatal("AddToolDef rejected a valid definition")
	}
	if toolsVersion.Load() <= before {
		t.Fatal("AddToolDef did not bump the registry version")
	}
	defs := ToolDefs()
	if len(defs) != 1 || defs[0].Name != "mcp_files_read" {
		t.Fatalf("ToolDefs = %+v", defs)
	}

	// Replacement updates in place instead of appending a duplicate.
	if !AddToolDef(ToolDef{Name: "mcp_files_read", Description: "updated", InputSchema: json.RawMessage(`{"type":"object"}`)}) {
		t.Fatal("AddToolDef rejected a replacement")
	}
	defs = ToolDefs()
	if len(defs) != 1 || defs[0].Description != "updated" {
		t.Fatalf("replacement ToolDefs = %+v", defs)
	}

	// The returned slice is independent from the registry.
	defs[0].Name = "mutated"
	if got := ToolDefs()[0].Name; got != "mcp_files_read" {
		t.Fatalf("ToolDefs returned an aliased slice: %q", got)
	}

	// A request-visible provider view picks up the new tool.
	ctx := context.Background()
	if got := len(openAICompatTools(ctx)); got != 1 {
		t.Fatalf("openAI tools = %d, want 1", got)
	}

	versionBeforeRemove := toolsVersion.Load()
	if !RemoveToolDef("mcp_files_read") {
		t.Fatal("RemoveToolDef did not find the tool")
	}
	if toolsVersion.Load() <= versionBeforeRemove {
		t.Fatal("RemoveToolDef did not bump the registry version")
	}
	if got := len(ToolDefs()); got != 0 {
		t.Fatalf("tools after remove = %d", got)
	}
	if got := len(openAICompatTools(ctx)); got != 0 {
		t.Fatalf("openAI tools after remove = %d", got)
	}
	if RemoveToolDef("missing") {
		t.Fatal("RemoveToolDef reported removing a missing tool")
	}
	if AddToolDef(ToolDef{Name: "  "}) {
		t.Fatal("AddToolDef accepted an empty name")
	}
}

func TestAddRemoveToolDefConcurrent(t *testing.T) {
	preserveToolState(t)
	InitToolsDef(nil)
	defer InitToolsDef(nil)

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := "tool_" + string(rune('a'+i))
			for j := 0; j < 100; j++ {
				AddToolDef(ToolDef{Name: name, Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)})
				_ = ToolDefs()
				RemoveToolDef(name)
			}
		}()
	}
	wg.Wait()
	if got := len(ToolDefs()); got != 0 {
		t.Fatalf("leftover tools after concurrent add/remove: %d", got)
	}
}
