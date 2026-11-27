package approot

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindFilePrefersTheAppRoot(t *testing.T) {
	appRoot := t.TempDir()
	projectRoot := t.TempDir()

	writeFile(t, filepath.Join(appRoot, "prompts", "system_prompt.md"), "installed")
	writeFile(t, filepath.Join(projectRoot, "prompts", "system_prompt.md"), "checkout")

	got, ok := FindFile("prompts/system_prompt.md", appRoot, projectRoot)
	if !ok {
		t.Fatal("FindFile did not find the asset")
	}
	want := filepath.Join(appRoot, "prompts", "system_prompt.md")
	if got != want {
		t.Fatalf("FindFile = %q, want %q", got, want)
	}
}

func TestFindFileFallsBackToLaterRoots(t *testing.T) {
	appRoot := t.TempDir()
	projectRoot := t.TempDir()

	writeFile(t, filepath.Join(projectRoot, "prompts", "system_prompt.md"), "checkout")

	got, ok := FindFile("prompts/system_prompt.md", appRoot, projectRoot)
	if !ok {
		t.Fatal("FindFile did not find the asset in the fallback root")
	}
	want := filepath.Join(projectRoot, "prompts", "system_prompt.md")
	if got != want {
		t.Fatalf("FindFile = %q, want %q", got, want)
	}
}

func TestFindFileSkipsDirectoriesAndMissingAssets(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prompts", "system_prompt.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if got, ok := FindFile("prompts/system_prompt.md", root); ok {
		t.Fatalf("FindFile returned a directory: %q", got)
	}
	if got, ok := FindFile("prompts/missing.md", root); ok {
		t.Fatalf("FindFile returned a missing asset: %q", got)
	}
}

func TestRootsIncludeExecutableDirectoryAndDropDuplicates(t *testing.T) {
	exeDir, err := ExecutableDir()
	if err != nil {
		t.Fatalf("ExecutableDir: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	roots := Roots(cwd)
	if len(roots) == 0 {
		t.Fatal("Roots returned no directories")
	}
	if roots[0] != exeDir {
		t.Errorf("Roots[0] = %q, want executable directory %q", roots[0], exeDir)
	}

	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		if seen[root] {
			t.Errorf("Roots returned duplicate %q", root)
		}
		seen[root] = true
	}
}
