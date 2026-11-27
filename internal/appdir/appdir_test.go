package appdir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// isolateConfig points the platform config directory at a temporary tree so
// tests never touch the real user configuration.
func isolateConfig(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", base)
		t.Setenv("LOCALAPPDATA", base)
	case "darwin":
		t.Setenv("HOME", base)
	default:
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
		t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "cache"))
	}
	return base
}

func TestConfigDirOverrideCreatesPrivateDirectory(t *testing.T) {
	isolateConfig(t)
	override := filepath.Join(t.TempDir(), "nested", "config")
	t.Setenv(EnvConfigDir, override)

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if got != override {
		t.Fatalf("ConfigDir = %q, want %q", got, override)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("ConfigDir did not create a directory: %q", got)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("config dir mode = %04o, want 0700", perm)
		}
	}
}

func TestConfigDirFallsBackToPlatformRoot(t *testing.T) {
	isolateConfig(t)
	t.Setenv(EnvConfigDir, "")

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	want := filepath.Join(base, AppName)
	if got != want {
		t.Fatalf("ConfigDir = %q, want %q", got, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("ConfigDir did not create %q: info=%v err=%v", got, info, err)
	}
}

func TestSkillsMCPAndLSPPaths(t *testing.T) {
	isolateConfig(t)
	configRoot := filepath.Join(t.TempDir(), "cfg")
	t.Setenv(EnvConfigDir, configRoot)

	skills, err := SkillsDir()
	if err != nil {
		t.Fatalf("SkillsDir: %v", err)
	}
	if want := filepath.Join(configRoot, SkillsDirName); skills != want {
		t.Fatalf("SkillsDir = %q, want %q", skills, want)
	}
	if info, err := os.Stat(skills); err != nil || !info.IsDir() {
		t.Fatalf("SkillsDir did not create %q: info=%v err=%v", skills, info, err)
	}

	mcp, err := MCPConfigPath()
	if err != nil {
		t.Fatalf("MCPConfigPath: %v", err)
	}
	if want := filepath.Join(configRoot, MCPConfigFile); mcp != want {
		t.Fatalf("MCPConfigPath = %q, want %q", mcp, want)
	}
	if _, err := os.Stat(mcp); !os.IsNotExist(err) {
		t.Fatalf("MCPConfigPath created the config file: %v", err)
	}

	lsp, err := LSPConfigPath()
	if err != nil {
		t.Fatalf("LSPConfigPath: %v", err)
	}
	if want := filepath.Join(configRoot, LSPConfigFile); lsp != want {
		t.Fatalf("LSPConfigPath = %q, want %q", lsp, want)
	}
	if _, err := os.Stat(lsp); !os.IsNotExist(err) {
		t.Fatalf("LSPConfigPath created the config file: %v", err)
	}
}

func TestCacheDirIsCreatedAndSeparateFromConfig(t *testing.T) {
	isolateConfig(t)
	t.Setenv(EnvConfigDir, filepath.Join(t.TempDir(), "config"))

	cache, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if filepath.Base(cache) != AppName {
		t.Fatalf("CacheDir base = %q, want %q", filepath.Base(cache), AppName)
	}
	info, err := os.Stat(cache)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("CacheDir did not create a directory: %q", cache)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("cache dir mode = %04o, want 0700", perm)
		}
	}
	config, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if cache == config {
		t.Fatalf("cache and config directories are identical: %q", cache)
	}
}

func TestMigrateLegacySkillsCopiesOnlyMissingEntries(t *testing.T) {
	isolateConfig(t)
	configRoot := filepath.Join(t.TempDir(), "config")
	t.Setenv(EnvConfigDir, configRoot)

	exeDir := t.TempDir()
	legacy := filepath.Join(exeDir, ".GoWork", SkillsDirName)
	writeTestFile(t, filepath.Join(legacy, "demo", "SKILL.md"), "legacy body", 0o644)
	writeTestFile(t, filepath.Join(legacy, "demo", "asset.txt"), "asset body", 0o644)
	writeTestFile(t, filepath.Join(legacy, "other", "SKILL.md"), "other body", 0o644)

	// The destination already has one of the skills and must keep it.
	dest, err := SkillsDir()
	if err != nil {
		t.Fatalf("SkillsDir: %v", err)
	}
	writeTestFile(t, filepath.Join(dest, "demo", "SKILL.md"), "user version", 0o600)

	result, err := MigrateLegacySkills(exeDir)
	if err != nil {
		t.Fatalf("MigrateLegacySkills: %v", err)
	}
	if !result.Migrated() {
		t.Fatal("migration reported no copied entries")
	}
	if got := readTestFile(t, filepath.Join(dest, "demo", "SKILL.md")); got != "user version" {
		t.Fatalf("existing skill was overwritten: %q", got)
	}
	if got := readTestFile(t, filepath.Join(dest, "demo", "asset.txt")); got != "asset body" {
		t.Fatalf("missing asset = %q, want %q", got, "asset body")
	}
	if got := readTestFile(t, filepath.Join(dest, "other", "SKILL.md")); got != "other body" {
		t.Fatalf("new skill = %q, want %q", got, "other body")
	}

	wantSkipped := filepath.Join("demo", "SKILL.md")
	if len(result.Skipped) != 1 || result.Skipped[0] != wantSkipped {
		t.Fatalf("Skipped = %v, want [%s]", result.Skipped, wantSkipped)
	}
	joinedCopied := strings.Join(result.Copied, ",")
	if !strings.Contains(joinedCopied, filepath.Join("demo", "asset.txt")) ||
		!strings.Contains(joinedCopied, filepath.Join("other", "SKILL.md")) {
		t.Fatalf("Copied = %v, missing expected entries", result.Copied)
	}
	if !sorted(result.Copied) || !sorted(result.Skipped) {
		t.Fatalf("migration results are not deterministic: copied=%v skipped=%v", result.Copied, result.Skipped)
	}
}

func TestMigrateLegacySkillsMissingDirectoryIsNoOp(t *testing.T) {
	isolateConfig(t)
	t.Setenv(EnvConfigDir, filepath.Join(t.TempDir(), "config"))

	result, err := MigrateLegacySkills(t.TempDir())
	if err != nil {
		t.Fatalf("MigrateLegacySkills: %v", err)
	}
	if result.Migrated() || len(result.Skipped) != 0 {
		t.Fatalf("unexpected migration result: %+v", result)
	}

	result, err = MigrateLegacySkills("")
	if err != nil || result.Migrated() {
		t.Fatalf("empty exe dir: result=%+v err=%v", result, err)
	}
}

func TestMigrateLegacySkillsSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires extra privileges on Windows")
	}
	isolateConfig(t)
	t.Setenv(EnvConfigDir, filepath.Join(t.TempDir(), "config"))

	exeDir := t.TempDir()
	legacy := filepath.Join(exeDir, ".GoWork", SkillsDirName)
	writeTestFile(t, filepath.Join(legacy, "real", "SKILL.md"), "real", 0o600)
	target := filepath.Join(t.TempDir(), "outside.txt")
	writeTestFile(t, target, "outside", 0o600)
	if err := os.Symlink(target, filepath.Join(legacy, "real", "link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}

	result, err := MigrateLegacySkills(exeDir)
	if err != nil {
		t.Fatalf("MigrateLegacySkills: %v", err)
	}
	dest, err := SkillsDir()
	if err != nil {
		t.Fatalf("SkillsDir: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "real", "link.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink was copied: %v", err)
	}
	found := false
	for _, skipped := range result.Skipped {
		if skipped == filepath.Join("real", "link.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Skipped = %v, want symlink entry", result.Skipped)
	}
}

func TestMigrateLegacySkillsConcurrentRunsDoNotOverwrite(t *testing.T) {
	isolateConfig(t)
	t.Setenv(EnvConfigDir, filepath.Join(t.TempDir(), "config"))

	exeDir := t.TempDir()
	legacy := filepath.Join(exeDir, ".GoWork", SkillsDirName)
	for i := 0; i < 5; i++ {
		writeTestFile(t, filepath.Join(legacy, "demo", "SKILL.md"), "legacy", 0o600)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := MigrateLegacySkills(exeDir)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}

	dest, err := SkillsDir()
	if err != nil {
		t.Fatalf("SkillsDir: %v", err)
	}
	if got := readTestFile(t, filepath.Join(dest, "demo", "SKILL.md")); got != "legacy" {
		t.Fatalf("migrated content = %q, want legacy", got)
	}
}

func TestMigrateLegacySkillsPreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not model Unix execute bits")
	}
	isolateConfig(t)
	t.Setenv(EnvConfigDir, filepath.Join(t.TempDir(), "config"))

	exeDir := t.TempDir()
	legacy := filepath.Join(exeDir, ".GoWork", SkillsDirName)
	writeTestFile(t, filepath.Join(legacy, "demo", "script.sh"), "#!/bin/sh\n", 0o755)

	if _, err := MigrateLegacySkills(exeDir); err != nil {
		t.Fatalf("MigrateLegacySkills: %v", err)
	}
	dest, err := SkillsDir()
	if err != nil {
		t.Fatalf("SkillsDir: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, "demo", "script.sh"))
	if err != nil {
		t.Fatalf("stat migrated script: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("migrated script mode = %04o, want owner execute", info.Mode().Perm())
	}
}

func sorted(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] > values[i] {
			return false
		}
	}
	return true
}
