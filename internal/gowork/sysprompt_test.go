package gowork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTreeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestBuildProjectTreeFallback forces the no-ripgrep path and checks that the
// Go walker produces the tree while honoring hidden/ignore rules.
func TestBuildProjectTreeFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeTreeFile(t, root, ".gitignore", "ignored.go\n")
	writeTreeFile(t, root, ".agentignore", "secret.go\n")
	writeTreeFile(t, root, ".env", "SECRET=1\n")
	writeTreeFile(t, root, "visible.go", "package main\n")
	writeTreeFile(t, root, "pkg/nested.go", "package pkg\n")
	writeTreeFile(t, root, "ignored.go", "package main\n")
	writeTreeFile(t, root, "secret.go", "package main\n")

	t.Setenv("PATH", t.TempDir()) // hide rg from exec.LookPath

	tree := buildProjectTree(root)
	for _, want := range []string{"visible.go", "pkg/nested.go"} {
		if !strings.Contains(tree, want) {
			t.Errorf("expected %q in fallback project tree, got:\n%s", want, tree)
		}
	}
	for _, stale := range []string{".env", "ignored.go", "secret.go", ".git"} {
		if strings.Contains(tree, stale) {
			t.Errorf("fallback project tree leaked %q:\n%s", stale, tree)
		}
	}
}

func TestBuildProjectTreeUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	tree := buildProjectTree(missing)
	if !strings.Contains(tree, "project tree unavailable") {
		t.Errorf("expected a clear unavailable message, got: %q", tree)
	}
}

func TestBuildProjectTreeEmptyRoot(t *testing.T) {
	if tree := buildProjectTree(""); tree != "" {
		t.Errorf("buildProjectTree(\"\") = %q, want empty string", tree)
	}
}
