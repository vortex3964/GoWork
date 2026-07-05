package createfiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	createfile "GoWork/tools/CreateFileTool"
)

func newTool(t *testing.T, root string) *createfile.Tool {
	t.Helper()
	tool, err := createfile.New(root)
	if err != nil {
		t.Fatalf("failed to construct tool: %v", err)
	}
	return tool
}

func run(t *testing.T, tool *createfile.Tool, path, content string) (string, bool) {
	t.Helper()
	input, err := json.Marshal(createfile.Input{Path: path, Content: content})
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected Go error (not a ToolResult failure): %v", err)
	}
	return result.Content, result.IsError
}

func TestCreateFile(t *testing.T) {
	t.Run("creates file with content inside root", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := run(t, tool, "hello.txt", "hello world")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		data, err := os.ReadFile(filepath.Join(root, "hello.txt"))
		if err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
		if string(data) != "hello world" {
			t.Errorf("file content = %q, want %q", string(data), "hello world")
		}
	})

	t.Run("creates missing nested directories", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := run(t, tool, "nested/deep/dir/file.txt", "content")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		path := filepath.Join(root, "nested", "deep", "dir", "file.txt")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected nested file to exist at %q: %v", path, err)
		}
	})

	t.Run("path ending in slash creates directory only", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		content, isErr := run(t, tool, "just/a/dir/", "")
		if isErr {
			t.Fatalf("unexpected error result: %q", content)
		}

		info, err := os.Stat(filepath.Join(root, "just", "a", "dir"))
		if err != nil {
			t.Fatalf("expected directory to exist: %v", err)
		}
		if !info.IsDir() {
			t.Error("expected a directory, got a file")
		}
	})

	t.Run("empty content creates empty file", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := run(t, tool, "empty.txt", "")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		info, err := os.Stat(filepath.Join(root, "empty.txt"))
		if err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
		if info.Size() != 0 {
			t.Errorf("expected 0-byte file, got size %d", info.Size())
		}
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		run(t, tool, "overwrite.txt", "first")
		_, isErr := run(t, tool, "overwrite.txt", "second")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		data, _ := os.ReadFile(filepath.Join(root, "overwrite.txt"))
		if string(data) != "second" {
			t.Errorf("file content = %q, want %q", string(data), "second")
		}
	})

	t.Run("rejects empty path", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := run(t, tool, "", "content")
		if !isErr {
			t.Error("expected an error result for an empty path")
		}
	})

	t.Run("rejects relative path traversal outside project root", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := run(t, tool, "../outside/evil.txt", "malicious")
		if !isErr {
			t.Error("expected an error result for a path traversal attempt")
		}

		outsidePath := filepath.Join(filepath.Dir(root), "outside", "evil.txt")
		if _, err := os.Stat(outsidePath); err == nil {
			t.Error("file should not have been created outside project root")
		}
	})

	t.Run("rejects absolute path", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := run(t, tool, "/etc/evil.txt", "malicious")
		if !isErr {
			t.Error("expected an error result for an absolute path")
		}

		if _, err := os.Stat("/etc/evil.txt"); err == nil {
			t.Fatal("SAFETY FAILURE: file was created outside the sandbox at /etc/evil.txt")
		}
	})

	t.Run("rejects creation through a symlink escaping the root", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()

		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlinks not supported on this system: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := run(t, tool, "escape/evil.txt", "malicious")
		if !isErr {
			t.Error("expected an error when creating a file through a symlink pointing outside root")
		}

		if _, err := os.Stat(filepath.Join(outside, "evil.txt")); err == nil {
			t.Fatal("SAFETY FAILURE: file was created outside the sandbox via a symlink")
		}
	})

	t.Run("returns a Go error for malformed input JSON", func(t *testing.T) {
		tool := newTool(t, t.TempDir())
		_, err := tool.Run(context.Background(), json.RawMessage(`{not valid json`))
		if err == nil {
			t.Error("expected a Go error for malformed input JSON")
		}
	})

}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
