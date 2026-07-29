package movefiletool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	
	"GoWork/tools"
	movefiletool "GoWork/tools/MoveFileTool"
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
	tool := movefiletool.New()
	args, err := tools.InitDispatchArgs(root, nil)
	if err != nil {
		t.Fatalf("failed to init dispatch args: %v", err)
	}
	return testTool{AgentTool: tool, args: args}
}

func runTool(t *testing.T, tool testTool, source, dest string) (string, bool) {
	t.Helper()
	input, err := json.Marshal(struct {
		SourcePath      string `json:"source_path"`
		DestinationPath string `json:"destination_path"`
	}{SourcePath: source, DestinationPath: dest})
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

func TestMoveFile(t *testing.T) {
	t.Run("moves file into a new nested directory", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "src"))
		writeFile(t, filepath.Join(root, "src", "foo.txt"), "hello")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "src/foo.txt", "dest/nested/foo.txt")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		data, err := os.ReadFile(filepath.Join(root, "dest", "nested", "foo.txt"))
		if err != nil {
			t.Fatalf("expected moved file to exist: %v", err)
		}
		if string(data) != "hello" {
			t.Errorf("moved file content = %q, want %q", string(data), "hello")
		}
		if _, err := os.Stat(filepath.Join(root, "src", "foo.txt")); !os.IsNotExist(err) {
			t.Error("expected original file to no longer exist")
		}
	})

	t.Run("renames file within the same directory", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/hi.go", "hello/renamed.go")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(filepath.Join(root, "hello", "hi.go")); !os.IsNotExist(err) {
			t.Error("expected original filename to no longer exist")
		}
		data, err := os.ReadFile(filepath.Join(root, "hello", "renamed.go"))
		if err != nil {
			t.Fatalf("expected renamed file to exist: %v", err)
		}
		if string(data) != "package hello" {
			t.Errorf("renamed file content = %q, want %q", string(data), "package hello")
		}
		// directory still has renamed.go in it, so it must survive.
		if _, err := os.Stat(filepath.Join(root, "hello")); err != nil {
			t.Error("expected 'hello' directory to survive since it still has a file in it")
		}
	})

	t.Run("overwrites an existing destination file", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "new content")
		writeFile(t, filepath.Join(root, "dest.txt"), "old content")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "source.txt", "dest.txt")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		data, err := os.ReadFile(filepath.Join(root, "dest.txt"))
		if err != nil {
			t.Fatalf("expected destination file to exist: %v", err)
		}
		if string(data) != "new content" {
			t.Errorf("destination content = %q, want %q", string(data), "new content")
		}
	})

	t.Run("removes now-empty immediate parent directory of source", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/hi.go", "moved.go")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(filepath.Join(root, "hello")); !os.IsNotExist(err) {
			t.Error("expected empty 'hello' directory to be removed after moving its only file out")
		}
	})

	t.Run("does not cascade source cleanup beyond the immediate parent", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello", "world"))
		writeFile(t, filepath.Join(root, "hello", "world", "hi.go"), "package world")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/world/hi.go", "moved.go")
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

	t.Run("keeps non-empty parent directory of source", func(t *testing.T) {
		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")
		writeFile(t, filepath.Join(root, "hello", "sibling.txt"), "keep me")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "hello/hi.go", "moved.go")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(filepath.Join(root, "hello")); err != nil {
			t.Error("expected 'hello' to survive since it still contains sibling.txt")
		}
	})

	t.Run("does not remove project root itself even if source was at the root", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "onlyfile.txt"), "content")

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "onlyfile.txt", "moved/onlyfile.txt")
		if isErr {
			t.Fatalf("unexpected error result")
		}

		if _, err := os.Stat(root); err != nil {
			t.Error("project root itself should never be deleted")
		}
	})

	t.Run("errors on moving a nonexistent source file", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "does-not-exist.txt", "dest.txt")
		if !isErr {
			t.Error("expected an error result for a nonexistent source file")
		}
		if _, err := os.Stat(filepath.Join(root, "dest.txt")); err == nil {
			t.Error("destination file should not have been created")
		}
	})

	t.Run("rejects empty source_path", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "", "dest.txt")
		if !isErr {
			t.Error("expected an error result for an empty source_path")
		}
	})

	t.Run("rejects empty destination_path", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "content")
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "source.txt", "")
		if !isErr {
			t.Error("expected an error result for an empty destination_path")
		}
	})

	t.Run("rejects source_path of the project root", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, ".", "dest.txt")
		if !isErr {
			t.Error("expected an error result when source_path is the project root")
		}
	})

	t.Run("rejects destination_path of the project root", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "content")
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "source.txt", ".")
		if !isErr {
			t.Error("expected an error result when destination_path is the project root")
		}
	})

	t.Run("rejects identical source and destination paths", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "content")
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "source.txt", "./source.txt")
		if !isErr {
			t.Error("expected an error result when source and destination resolve to the same path")
		}
	})

	t.Run("rejects relative path traversal on source", func(t *testing.T) {
		root := t.TempDir()
		outsideDir := t.TempDir()
		writeFile(t, filepath.Join(outsideDir, "victim.txt"), "content")

		rel, err := filepath.Rel(root, outsideDir)
		if err != nil {
			t.Fatalf("failed to compute relative path: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, filepath.Join(rel, "victim.txt"), "dest.txt")
		if !isErr {
			t.Error("expected an error result for a source path traversal attempt")
		}
		if _, err := os.Stat(filepath.Join(outsideDir, "victim.txt")); err != nil {
			t.Fatal("SAFETY FAILURE: file outside the sandbox was moved/deleted")
		}
	})

	t.Run("rejects relative path traversal on destination", func(t *testing.T) {
		root := t.TempDir()
		outsideDir := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "content")

		rel, err := filepath.Rel(root, outsideDir)
		if err != nil {
			t.Fatalf("failed to compute relative path: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "source.txt", filepath.Join(rel, "evil.txt"))
		if !isErr {
			t.Error("expected an error result for a destination path traversal attempt")
		}
		if _, err := os.Stat(filepath.Join(outsideDir, "evil.txt")); err == nil {
			t.Error("file should not have been created outside project root")
		}
		if _, err := os.Stat(filepath.Join(root, "source.txt")); err != nil {
			t.Error("source file should survive since the destination write failed")
		}
	})

	t.Run("rejects absolute source path", func(t *testing.T) {
		root := t.TempDir()
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "/etc/passwd", "dest.txt")
		if !isErr {
			t.Error("expected an error result for an absolute source path")
		}
	})

	t.Run("rejects absolute destination path", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "content")
		tool := newTool(t, root)

		_, isErr := runTool(t, tool, "source.txt", "/etc/evil.txt")
		if !isErr {
			t.Error("expected an error result for an absolute destination path")
		}
		if _, err := os.Stat("/etc/evil.txt"); err == nil {
			t.Fatal("SAFETY FAILURE: file was created outside the sandbox at /etc/evil.txt")
		}
	})

	t.Run("rejects move through a source symlink escaping the root", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		writeFile(t, filepath.Join(outside, "victim.txt"), "content")

		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlinks not supported on this system: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "escape/victim.txt", "dest.txt")
		if !isErr {
			t.Error("expected an error when reading a source file through a symlink pointing outside root")
		}
		if _, err := os.Stat(filepath.Join(outside, "victim.txt")); err != nil {
			t.Fatal("SAFETY FAILURE: file outside the sandbox was moved via a symlink")
		}
	})

	t.Run("rejects move through a destination symlink escaping the root", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		writeFile(t, filepath.Join(root, "source.txt"), "content")

		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlinks not supported on this system: %v", err)
		}

		tool := newTool(t, root)
		_, isErr := runTool(t, tool, "source.txt", "escape/evil.txt")
		if !isErr {
			t.Error("expected an error when writing a destination file through a symlink pointing outside root")
		}
		if _, err := os.Stat(filepath.Join(outside, "evil.txt")); err == nil {
			t.Fatal("SAFETY FAILURE: file was created outside the sandbox via a symlink")
		}
		if _, err := os.Stat(filepath.Join(root, "source.txt")); err != nil {
			t.Error("source file should survive since the destination write failed")
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
		if tool.Name() != "move_file" {
			t.Errorf("Name() = %q, want %q", tool.Name(), "move_file")
		}
	})
}
