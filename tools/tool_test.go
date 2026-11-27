package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeIgnoreFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// TestLoadIgnoresCachesCompiledMatchers proves repeated calls for an unchanged
// root reuse the same compiled matcher slice instead of recompiling.
func TestLoadIgnoresCachesCompiledMatchers(t *testing.T) {
	root := t.TempDir()
	writeIgnoreFile(t, root, ".gitignore", "*.log\n")
	writeIgnoreFile(t, root, ".agentignore", "*.tmp\n")

	first := LoadIgnores(root)
	second := LoadIgnores(root)

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 matchers on each call, got %d and %d", len(first), len(second))
	}
	if &first[0] != &second[0] {
		t.Fatal("expected repeated LoadIgnores calls to reuse the cached matcher slice")
	}
	if !IsIgnored(second, "debug.log") || !IsIgnored(second, "scratch.tmp") {
		t.Fatalf("cached matchers do not honor the ignore patterns")
	}
}

// TestLoadIgnoresRefreshesWhenFilesChange proves the cache invalidates per file
// based on mtime/size, for both .gitignore and .agentignore.
func TestLoadIgnoresRefreshesWhenFilesChange(t *testing.T) {
	root := t.TempDir()
	gitignorePath := writeIgnoreFile(t, root, ".gitignore", "*.log\n")
	writeIgnoreFile(t, root, ".agentignore", "*.tmp\n")

	before := LoadIgnores(root)
	if !IsIgnored(before, "debug.log") || !IsIgnored(before, "scratch.tmp") {
		t.Fatalf("initial matchers do not reflect both ignore files")
	}

	// Same size as before, so only the mtime can invalidate the entry.
	writeIgnoreFile(t, root, ".gitignore", "*.bak\n")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(gitignorePath, future, future); err != nil {
		t.Fatalf("updating .gitignore mtime: %v", err)
	}

	afterGitignore := LoadIgnores(root)
	if &before[0] == &afterGitignore[0] {
		t.Fatal("expected .gitignore change to recompile the matchers")
	}
	if IsIgnored(afterGitignore, "debug.log") {
		t.Fatalf(".gitignore cache was not refreshed: old *.log pattern still active")
	}
	if !IsIgnored(afterGitignore, "backup.bak") {
		t.Fatalf(".gitignore cache was not refreshed: new *.bak pattern missing")
	}
	if !IsIgnored(afterGitignore, "scratch.tmp") {
		t.Fatalf(".agentignore pattern was lost after .gitignore refresh")
	}

	// Different size, so this also covers size-based invalidation.
	writeIgnoreFile(t, root, ".agentignore", "*.cache\n")
	afterAgentignore := LoadIgnores(root)
	if &afterGitignore[0] == &afterAgentignore[0] {
		t.Fatal("expected .agentignore change to recompile the matchers")
	}
	if IsIgnored(afterAgentignore, "scratch.tmp") {
		t.Fatalf(".agentignore cache was not refreshed: old *.tmp pattern still active")
	}
	if !IsIgnored(afterAgentignore, "page.cache") {
		t.Fatalf(".agentignore cache was not refreshed: new *.cache pattern missing")
	}
	if !IsIgnored(afterAgentignore, "backup.bak") {
		t.Fatalf(".gitignore pattern was lost after .agentignore refresh")
	}
}

// TestLoadIgnoresHandlesMissingFiles proves a missing ignore file is not
// cached as a permanent "no ignore rules" state.
func TestLoadIgnoresHandlesMissingFiles(t *testing.T) {
	root := t.TempDir()

	if ignores := LoadIgnores(root); len(ignores) != 0 {
		t.Fatalf("expected no matchers for a root without ignore files, got %d", len(ignores))
	}

	path := writeIgnoreFile(t, root, ".gitignore", "*.log\n")
	ignores := LoadIgnores(root)
	if len(ignores) != 1 || !IsIgnored(ignores, "debug.log") {
		t.Fatalf("expected .gitignore to be picked up after it appears")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing .gitignore: %v", err)
	}
	if ignores := LoadIgnores(root); len(ignores) != 0 {
		t.Fatalf("expected matchers to be dropped after .gitignore is removed, got %d", len(ignores))
	}
}
