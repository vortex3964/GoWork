package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"GoWork/tools"
)

// helper to set ProjectRoot for the duration of a test and restore it after
func withProjectRoot(t *testing.T, root string) {
	t.Helper()
	old := tools.ProjectRoot
	tools.ProjectRoot = root
	t.Cleanup(func() {
		tools.ProjectRoot = old
	})
}

func TestListDirectory(t *testing.T) {
	root := t.TempDir()
	withProjectRoot(t, root)

	writeFile(t, filepath.Join(root, "a.txt"), "hello")
	mustMkdirAll(t, filepath.Join(root, "sub"))
	writeFile(t, filepath.Join(root, "sub", "b.txt"), "world")

	t.Run("valid path inside root", func(t *testing.T) {
		result := tools.List_directory(root)
		if !strings.Contains(result, "a.txt") || !strings.Contains(result, "sub/") {
			t.Errorf("unexpected listing output: %q", result)
		}
	})

	t.Run("path outside root is rejected", func(t *testing.T) {
		outside := filepath.Dir(root)
		result := tools.List_directory(outside)
		if !strings.Contains(result, "error") {
			t.Errorf("expected error for out-of-root path, got: %q", result)
		}
	})

	t.Run("respects gitignore", func(t *testing.T) {
		writeFile(t, filepath.Join(root, "ignored.log"), "noise")
		writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")

		result := tools.List_directory(root)
		if strings.Contains(result, "ignored.log") {
			t.Error("expected ignored.log to be excluded from listing")
		}
	})
}

func TestCreateFile(t *testing.T) {
	t.Run("creates file with content inside root", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)

		result := tools.Create_file(".", "hello.txt", "hello world")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
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
		withProjectRoot(t, root)

		result := tools.Create_file("nested/deep/dir", "file.txt", "content")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}

		path := filepath.Join(root, "nested", "deep", "dir", "file.txt")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected nested file to exist at %q: %v", path, err)
		}
	})

	t.Run("empty content creates empty file", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)

		result := tools.Create_file(".", "empty.txt", "")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}

		info, err := os.Stat(filepath.Join(root, "empty.txt"))
		if err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
		if info.Size() != 0 {
			t.Errorf("expected 0-byte file, got size %d", info.Size())
		}
	})

	t.Run("rejects path outside project root", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)

		result := tools.Create_file("../outside", "evil.txt", "malicious")
		if !strings.Contains(result, "error") {
			t.Errorf("expected error for path traversal attempt, got: %q", result)
		}

		// double check the file was NOT created anywhere
		outsidePath := filepath.Join(filepath.Dir(root), "outside", "evil.txt")
		if _, err := os.Stat(outsidePath); err == nil {
			t.Error("file should not have been created outside project root")
		}
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)

		tools.Create_file(".", "overwrite.txt", "first")
		result := tools.Create_file(".", "overwrite.txt", "second")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}

		data, _ := os.ReadFile(filepath.Join(root, "overwrite.txt"))
		if string(data) != "second" {
			t.Errorf("file content = %q, want %q", string(data), "second")
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
