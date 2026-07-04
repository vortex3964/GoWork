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

func TestDeleteFile_CleansUpEmptyParentOnly(t *testing.T) {
	t.Run("removes now-empty immediate parent directory", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")
 
		result := tools.Delete_file("hello", "hi.go")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}
 
		if _, err := os.Stat(filepath.Join(root, "hello")); !os.IsNotExist(err) {
			t.Error("expected empty 'hello' directory to be removed after deleting its only file")
		}
	})
 
	t.Run("does not cascade beyond the immediate parent", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		mustMkdirAll(t, filepath.Join(root, "hello", "world"))
		writeFile(t, filepath.Join(root, "hello", "world", "hi.go"), "package world")
 
		result := tools.Delete_file("hello/world", "hi.go")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}
 
		// immediate parent ("world") should be gone, since it's now empty
		if _, err := os.Stat(filepath.Join(root, "hello", "world")); !os.IsNotExist(err) {
			t.Error("expected 'hello/world' to be removed since it became empty")
		}
		// "hello" is now empty too, but should NOT be removed — only one level up
		if _, err := os.Stat(filepath.Join(root, "hello")); err != nil {
			t.Error("expected 'hello' to survive — cleanup should not cascade past one level")
		}
	})
 
	t.Run("keeps non-empty parent directory", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		mustMkdirAll(t, filepath.Join(root, "hello"))
		writeFile(t, filepath.Join(root, "hello", "hi.go"), "package hello")
		writeFile(t, filepath.Join(root, "hello", "sibling.txt"), "keep me")
 
		result := tools.Delete_file("hello", "hi.go")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}
 
		if _, err := os.Stat(filepath.Join(root, "hello")); err != nil {
			t.Error("expected 'hello' to survive since it still contains sibling.txt")
		}
	})
 
	t.Run("does not remove ProjectRoot itself even if empty", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		writeFile(t, filepath.Join(root, "onlyfile.txt"), "content")
 
		result := tools.Delete_file(".", "onlyfile.txt")
		if strings.Contains(result, "error") {
			t.Fatalf("unexpected error: %q", result)
		}
 
		if _, err := os.Stat(root); err != nil {
			t.Error("ProjectRoot itself should never be deleted, even if it becomes empty")
		}
	})
}

func TestGetFilesInfo(t *testing.T) {
	t.Run("lists files and directories with type and size", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		writeFile(t, filepath.Join(root, "a.txt"), "hello world")
		mustMkdirAll(t, filepath.Join(root, "sub"))
 
		result := tools.Get_files_info(".")
 
		if !strings.Contains(result, "a.txt: file, 11 bytes") {
			t.Errorf("expected a.txt entry with correct size, got: %q", result)
		}
		if !strings.Contains(result, "sub/: dir") {
			t.Errorf("expected sub/ entry marked as dir, got: %q", result)
		}
	})
 
	t.Run("does not recurse into subdirectories", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		mustMkdirAll(t, filepath.Join(root, "sub"))
		writeFile(t, filepath.Join(root, "sub", "nested.txt"), "content")
 
		result := tools.Get_files_info(".")
		if strings.Contains(result, "nested.txt") {
			t.Errorf("expected nested.txt to be excluded since it's not in the immediate directory, got: %q", result)
		}
	})
 
	t.Run("respects gitignore", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		writeFile(t, filepath.Join(root, "keep.txt"), "keep me")
		writeFile(t, filepath.Join(root, "ignored.log"), "noise")
		writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
 
		result := tools.Get_files_info(".")
		if !strings.Contains(result, "keep.txt") {
			t.Errorf("expected keep.txt to be listed, got: %q", result)
		}
		if strings.Contains(result, "ignored.log") {
			t.Errorf("expected ignored.log to be excluded, got: %q", result)
		}
	})
 
	t.Run("rejects path outside project root", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		outside := filepath.Dir(root)
		result := tools.Get_files_info(outside)
		if !strings.Contains(result, "error") {
			t.Errorf("expected error for out-of-root path, got: %q", result)
		}
	})
 
	t.Run("errors on nonexistent directory", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		result := tools.Get_files_info("does-not-exist")
		if !strings.Contains(result, "error") {
			t.Errorf("expected error for nonexistent directory, got: %q", result)
		}
	})
 
	t.Run("reports when directory is empty", func(t *testing.T) {
		root := t.TempDir()
		withProjectRoot(t, root)
 
		result := tools.Get_files_info(".")
		if !strings.Contains(result, "empty") {
			t.Errorf("expected empty-directory message, got: %q", result)
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
