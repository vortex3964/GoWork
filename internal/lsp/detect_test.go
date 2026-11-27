package lsp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFakeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return path
}

func TestDetectUsesProcessPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use a POSIX shell script")
	}
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "gopls", "#!/bin/sh\necho 'gopls version 0.16.2'\n")
	t.Setenv("PATH", dir)

	found := Detect(context.Background())
	if len(found) != 1 || found[0].Name != "gopls" {
		t.Fatalf("Detect = %+v, want only gopls from the temp PATH", found)
	}
}

func TestDetectInPathFindsKnownServers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use a POSIX shell script")
	}
	dir := t.TempDir()
	gopls := writeFakeExecutable(t, dir, "gopls", "#!/bin/sh\necho 'gopls version 0.16.2'\n")
	writeFakeExecutable(t, dir, "rust-analyzer", "#!/bin/sh\necho 'rust-analyzer 2024-01-01'\n")
	// Unknown binaries must not appear in the result.
	writeFakeExecutable(t, dir, "not-a-language-server", "#!/bin/sh\necho nope\n")

	found := DetectInPath(context.Background(), dir)
	if len(found) != 2 {
		t.Fatalf("found %d servers, want 2: %+v", len(found), found)
	}

	byName := make(map[string]ServerInfo, len(found))
	for _, server := range found {
		byName[server.Name] = server
	}

	got, ok := byName["gopls"]
	if !ok {
		t.Fatalf("gopls not detected: %+v", found)
	}
	if got.Path != gopls {
		t.Errorf("gopls path = %q, want %q", got.Path, gopls)
	}
	if got.Version != "gopls version 0.16.2" {
		t.Errorf("gopls version = %q", got.Version)
	}
	if len(got.LanguageIDs) != 1 || got.LanguageIDs[0] != "go" {
		t.Errorf("gopls language IDs = %v, want [go]", got.LanguageIDs)
	}

	rust, ok := byName["rust-analyzer"]
	if !ok {
		t.Fatalf("rust-analyzer not detected: %+v", found)
	}
	if len(rust.LanguageIDs) != 1 || rust.LanguageIDs[0] != "rust" {
		t.Errorf("rust-analyzer language IDs = %v, want [rust]", rust.LanguageIDs)
	}
	if rust.Version != "rust-analyzer 2024-01-01" {
		t.Errorf("rust-analyzer version = %q", rust.Version)
	}
}

func TestDetectInPathSearchesEveryPathEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use a POSIX shell script")
	}
	first := t.TempDir()
	second := t.TempDir()
	writeFakeExecutable(t, first, "clangd", "#!/bin/sh\necho 'clangd version 18'\n")
	writeFakeExecutable(t, second, "pyright", "#!/bin/sh\necho 'pyright 1.1.350'\n")

	pathEnv := first + string(os.PathListSeparator) + second
	found := DetectInPath(context.Background(), pathEnv)
	names := make(map[string]bool, len(found))
	for _, server := range found {
		names[server.Name] = true
	}
	if !names["clangd"] || !names["pyright"] {
		t.Fatalf("detected %v, want clangd and pyright", names)
	}
}

func TestDetectInPathSkipsNonExecutableAndMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use a POSIX shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gopls"), []byte("not executable"), 0o644); err != nil {
		t.Fatalf("write non-executable: %v", err)
	}
	if found := DetectInPath(context.Background(), dir); len(found) != 0 {
		t.Fatalf("detected non-executable files: %+v", found)
	}
	if found := DetectInPath(context.Background(), t.TempDir()); len(found) != 0 {
		t.Fatalf("detected servers in an empty dir: %+v", found)
	}
}

func TestProbeVersionIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use a POSIX shell script")
	}
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "gopls", "#!/bin/sh\nsleep 5\necho 'too late'\n")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	found := DetectInPath(ctx, dir)
	if len(found) != 1 {
		t.Fatalf("found %d servers, want 1", len(found))
	}
	if found[0].Version != "" {
		t.Errorf("version = %q, want empty after cancellation", found[0].Version)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("detection took %v, want a bounded probe", elapsed)
	}
}

func TestDetectLanguageIDsAreCopied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use a POSIX shell script")
	}
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "clangd", "#!/bin/sh\necho 'clangd version 18'\n")
	found := DetectInPath(context.Background(), dir)
	if len(found) != 1 {
		t.Fatalf("found %d servers, want 1", len(found))
	}
	langs := found[0].LanguageIDs
	if len(langs) < 2 {
		t.Fatalf("clangd should list c and cpp, got %v", langs)
	}
	original := strings.Join(knownLanguageIDs("clangd"), ",")
	langs[0] = "mutated"
	if strings.Join(knownLanguageIDs("clangd"), ",") != original {
		t.Fatal("known server language IDs were mutated through a detection result")
	}
}

func knownLanguageIDs(name string) []string {
	for _, known := range knownServers {
		if known.binary == name {
			return known.languageIDs
		}
	}
	return nil
}
