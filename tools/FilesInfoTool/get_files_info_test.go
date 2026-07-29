package fileinfo_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"GoWork/tools"
	fileinfo "GoWork/tools/FilesInfoTool"
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
	tool := fileinfo.New()
	args, err := tools.InitDispatchArgs(root, nil)
	if err != nil {
		t.Fatalf("failed to init dispatch args: %v", err)
	}
	return testTool{AgentTool: tool, args: args}
}

func runTool(t *testing.T, tool testTool, path string) (string, bool) {
	t.Helper()
	input, err := json.Marshal(fileinfo.Input{Path: path})
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error (not a ToolResult failure): %v", err)
	}
	return result.Content, result.IsError
}

func TestGetFilesInfo(t *testing.T) {
	t.Run("lists files and directories with type and size", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.txt"), "hello world")
		mustMkdirAll(t, filepath.Join(root, "sub"))

		tool := newTool(t, root)
		content, isErr := runTool(t, tool, ".")

		if isErr {
			t.Fatalf("unexpected error result: %q", content)
		}
		if !strings.Contains(content, "a.txt: file, 11 bytes") {
			t.Errorf("expected a.txt entry with correct size, got: %q", content)
		}
		if !strings.Contains(content, "sub/: dir") {
			t.Errorf("expected sub/ entry marked as dir, got: %q", content)
		}
	})

	t.Run("does not recurse into subdirectories", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "sub"))
		writeFile(t, filepath.Join(root, "sub", "nested.txt"), "content")

		tool := newTool(t, root)
		content, isErr := runTool(t, tool, ".")

		if isErr {
			t.Fatalf("unexpected error result: %q", content)
		}
		if strings.Contains(content, "nested.txt") {
			t.Errorf("expected nested.txt to be excluded since it's not in the immediate directory, got: %q", content)
		}
	})

	t.Run("respects gitignore", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "keep.txt"), "keep me")
		writeFile(t, filepath.Join(root, "ignored.log"), "noise")
		writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")

		tool := newTool(t, root)
		content, isErr := runTool(t, tool, ".")

		if isErr {
			t.Fatalf("unexpected error result: %q", content)
		}
		if !strings.Contains(content, "keep.txt") {
			t.Errorf("expected keep.txt to be listed, got: %q", content)
		}
		if strings.Contains(content, "ignored.log") {
			t.Errorf("expected ignored.log to be excluded, got: %q", content)
		}
	})

	t.Run("rejects relative path traversal outside project root", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "../")
		if !isErr {
			t.Error("expected an error result for a path traversal attempt")
		}
	})

	t.Run("rejects absolute path outside project root", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "/etc")
		if !isErr {
			t.Error("expected an error result for an absolute path")
		}
	})

	t.Run("errors on nonexistent directory", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "does-not-exist")
		if !isErr {
			t.Error("expected an error result for nonexistent directory")
		}
	})

	t.Run("reports when directory is empty", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		content, isErr := runTool(t, tool, ".")
		if isErr {
			t.Fatalf("unexpected error result: %q", content)
		}
		if !strings.Contains(content, "empty") {
			t.Errorf("expected empty-directory message, got: %q", content)
		}
	})

	t.Run("rejects listing through a symlink escaping the root", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		writeFile(t, filepath.Join(outside, "secret.txt"), "should not be visible")

		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlinks not supported on this system: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "escape")
		if !isErr {
			t.Error("expected an error when listing through a symlink pointing outside root")
		}
	})

	t.Run("returns a Go error for malformed input JSON", func(t *testing.T) {
		tool := newTool(t, t.TempDir())
		_, err := tool.Run(context.Background(), json.RawMessage(`{not valid json`))
		if err == nil {
			t.Error("expected a Go error for malformed input JSON")
		}
	})

	t.Run("reports correct name and kind", func(t *testing.T) {
		tool := newTool(t, t.TempDir())
		if tool.Name() != "get_files_info" {
			t.Errorf("Name() = %q, want %q", tool.Name(), "get_files_info")
		}
	})

}

// --- test helpers ---

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
