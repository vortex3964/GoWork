package deletefiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	
	"GoWork/tools"
	deletefiletool "GoWork/tools/DeleteFileTool"
)

type testTool struct {
	tools.AgentTool
	args tools.DispatchArgs
}

func (tt testTool) Run(ctx context.Context, input json.RawMessage) (tools.ToolResult, error) {
	return tt.AgentTool.Run(ctx, tt.args, input)
}

func newTool(t *testing.T, root string) testTool {
	t.Helper()
	tool := deletefiletool.New()
	args, err := tools.InitDispatchArgs(root)
	if err != nil {
		t.Fatalf("failed to init dispatch args: %v", err)
	}
	return testTool{AgentTool: tool, args: args}
}

func runTool(t *testing.T, tool testTool, path string) (string, bool) {
	t.Helper()
	input, err := json.Marshal(struct{ Path string `json:"path"` }{Path: path})
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error (not a ToolResult failure): %v", err)
	}
	return result.Content, result.IsError
}
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file %q: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("failed to create test dir %q: %v", path, err)
	}
}

func TestDeleteFile(t *testing.T) {
	t.Run("removes now-empty immediate parent directory", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/hi.go")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(filepath.Join(root, "hello")); !os.IsNotExist(err) {
			t.Error("expected empty 'hello' directory to be removed after deleting its only file")
		}
	})

	t.Run("does not cascade beyond the immediate parent", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello", "world"))
		writeFile(t, filepath.Join(root, "hello", "world", "hi.go"), "package world")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/world/hi.go")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(filepath.Join(root, "hello", "world")); !os.IsNotExist(err) {
			t.Error("expected 'hello/world' to be removed since it became empty")
		}
		if _, err := os.Stat(filepath.Join(root, "hello")); err != nil {
			t.Error("expected 'hello' to survive — cleanup should not cascade past one level")
		}
	})

	t.Run("keeps non-empty parent directory", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")
		writeFile(t, filepath.Join(root, "hello", "sibling.txt"), "keep me")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/hi.go")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(filepath.Join(root, "hello")); err != nil {
			t.Error("expected 'hello' to survive since it still contains sibling.txt")
		}
	})

	t.Run("does not remove project root itself even if empty", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "onlyfile.txt"), "content")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "onlyfile.txt")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(root); err != nil {
			t.Error("project root itself should never be deleted, even if it becomes empty")
		}
	})

	t.Run("errors on deleting nonexistent file", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "does-not-exist.txt")
		if !isErr {
			t.Error("expected an error result for a nonexistent file")
		}
	})

	t.Run("rejects empty path", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "")
		if !isErr {
			t.Error("expected an error result for an empty path")
		}
	})

	t.Run("rejects relative path traversal outside project root", func(t *testing.T) {
		root := t.TempDir()
		outsideDir := t.TempDir()
		writeFile(t, filepath.Join(outsideDir, "victim.txt"), "content")

		rel, err := filepath.Rel(root, outsideDir)
		if err != nil {
			t.Fatalf("failed to compute relative path: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, filepath.Join(rel, "victim.txt"))
		if !isErr {
			t.Error("expected an error result for a path traversal attempt")
		}
		if _, err := os.Stat(filepath.Join(outsideDir, "victim.txt")); err != nil {
			t.Fatal("SAFETY FAILURE: file outside the sandbox was deleted")
		}
	})

	t.Run("rejects deletion through a symlink escaping the root", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		writeFile(t, filepath.Join(outside, "victim.txt"), "content")

		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlinks not supported on this system: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "escape/victim.txt")
		if !isErr {
			t.Error("expected an error when deleting through a symlink pointing outside root")
		}
		if _, err := os.Stat(filepath.Join(outside, "victim.txt")); err != nil {
			t.Fatal("SAFETY FAILURE: file outside the sandbox was deleted via a symlink")
		}
	})

	t.Run("returns a Go error for malformed input JSON", func(t *testing.T) {
		tool := newTool(t, t.TempDir())
		_, err := tool.Run(context.Background(), json.RawMessage(`{not valid json`))
		if err == nil {
			t.Error("expected a Go error for malformed input JSON")
		}
	})

	t.Run("reports correct name", func(t *testing.T) {
		tool := newTool(t, t.TempDir())
		if tool.Name() != "delete_file" {
			t.Errorf("Name() = %q, want %q", tool.Name(), "delete_file")
		}
	})
}
