package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

type registryTestTool struct {
	name string
	kind Kind
	runs atomic.Int64
}

func (t *registryTestTool) Name() string                 { return t.name }
func (t *registryTestTool) Description() string          { return "test tool" }
func (t *registryTestTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *registryTestTool) Kind() Kind                   { return t.kind }
func (t *registryTestTool) Run(context.Context, DispatchArgs, json.RawMessage) (ToolResult, error) {
	t.runs.Add(1)
	return Ok("ok"), nil
}

func TestDispatcherRegisterUnregisterTools(t *testing.T) {
	dispatcher, err := InitDispacher(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("InitDispacher: %v", err)
	}

	beta := &registryTestTool{name: "beta"}
	alpha := &registryTestTool{name: "alpha"}
	if err := dispatcher.Register(beta); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(alpha); err != nil {
		t.Fatal(err)
	}
	if got := dispatcher.Tools(); len(got) != 2 || got[0].Name() != "alpha" || got[1].Name() != "beta" {
		t.Fatalf("Tools() = %+v, want alpha, beta", got)
	}

	// Register replaces the existing tool with the same name.
	replacement := &registryTestTool{name: "beta"}
	if err := dispatcher.Register(replacement); err != nil {
		t.Fatal(err)
	}
	if got, ok := dispatcher.Lookup("beta"); !ok || got != replacement {
		t.Fatalf("Lookup(beta) = %v, %t", got, ok)
	}

	result := dispatcher.Dispach(context.Background(), ToolUse{Name: "alpha", Input: json.RawMessage(`{}`)})
	if result.IsError || result.Content != "ok" {
		t.Fatalf("Dispach result = %+v", result)
	}
	if !dispatcher.Unregister("alpha") {
		t.Fatal("Unregister(alpha) = false")
	}
	if _, ok := dispatcher.Lookup("alpha"); ok {
		t.Fatal("alpha still registered")
	}
	if dispatcher.Unregister("missing") {
		t.Fatal("Unregister(missing) = true")
	}
	if err := dispatcher.Register(nil); err == nil {
		t.Fatal("Register(nil) succeeded")
	}
	if err := dispatcher.Register(&registryTestTool{name: "  "}); err == nil {
		t.Fatal("Register accepted an empty name")
	}
}

func TestDispatcherConcurrentRegisterDispatch(t *testing.T) {
	dispatcher, err := InitDispacher(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("InitDispacher: %v", err)
	}
	for i := 0; i < 16; i++ {
		_ = dispatcher.Register(&registryTestTool{name: fmt.Sprintf("base_%d", i)})
	}

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				name := fmt.Sprintf("volatile_%d_%d", i, j)
				if err := dispatcher.Register(&registryTestTool{name: name}); err != nil {
					t.Errorf("Register: %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				dispatcher.Unregister(fmt.Sprintf("volatile_%d_%d", i, j))
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = dispatcher.Dispach(context.Background(), ToolUse{
					Name:  fmt.Sprintf("base_%d", j%16),
					Input: json.RawMessage(`{}`),
				})
				_ = dispatcher.Tools()
				_, _ = dispatcher.Lookup(fmt.Sprintf("volatile_%d_%d", i, j))
			}
		}()
	}
	wg.Wait()

	for _, tool := range dispatcher.Tools() {
		dispatcher.Unregister(tool.Name())
	}
	if got := len(dispatcher.Tools()); got != 0 {
		t.Fatalf("tools left after cleanup: %d", got)
	}
}
