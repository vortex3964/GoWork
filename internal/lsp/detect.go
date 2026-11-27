package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// versionTimeout bounds the optional `--version` probe so a broken server
	// binary can never stall detection.
	versionTimeout = 750 * time.Millisecond
	// versionWaitDelay bounds exec's wait for inherited stdout/stderr pipes
	// after the version command was killed.
	versionWaitDelay = 200 * time.Millisecond
	// maxVersionLine keeps a noisy server from filling the picker with one
	// enormous help dump.
	maxVersionLine = 200
)

// ServerInfo is a language server discovered on PATH.
type ServerInfo struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Version     string   `json:"version,omitempty"`
	LanguageIDs []string `json:"language_ids"`
}

// knownServer is a server family GoWork knows how to detect. Binaries are the
// command names to look for; language IDs are the LSP languageId values the
// server understands.
type knownServer struct {
	binary      string
	languageIDs []string
	versionArgs []string
}

var knownServers = []knownServer{
	{binary: "gopls", languageIDs: []string{"go"}, versionArgs: []string{"version"}},
	{binary: "pyright", languageIDs: []string{"python"}, versionArgs: []string{"--version"}},
	{binary: "basedpyright", languageIDs: []string{"python"}, versionArgs: []string{"--version"}},
	{binary: "pylsp", languageIDs: []string{"python"}, versionArgs: []string{"--version"}},
	{binary: "rust-analyzer", languageIDs: []string{"rust"}, versionArgs: []string{"--version"}},
	{binary: "typescript-language-server", languageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"}, versionArgs: []string{"--version"}},
	{binary: "tsserver", languageIDs: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"}, versionArgs: []string{"--version"}},
	{binary: "clangd", languageIDs: []string{"c", "cpp"}, versionArgs: []string{"--version"}},
	{binary: "jdtls", languageIDs: []string{"java"}, versionArgs: []string{"--version"}},
	{binary: "lua-language-server", languageIDs: []string{"lua"}, versionArgs: []string{"--version"}},
	{binary: "bash-language-server", languageIDs: []string{"shellscript"}, versionArgs: []string{"--version"}},
	{binary: "yaml-language-server", languageIDs: []string{"yaml"}, versionArgs: []string{"--version"}},
	{binary: "vscode-json-language-server", languageIDs: []string{"json", "jsonc"}, versionArgs: []string{"--version"}},
	{binary: "vscode-css-language-server", languageIDs: []string{"css", "scss", "less"}, versionArgs: []string{"--version"}},
	{binary: "vscode-html-language-server", languageIDs: []string{"html"}, versionArgs: []string{"--version"}},
	{binary: "docker-langserver", languageIDs: []string{"dockerfile"}, versionArgs: []string{"--version"}},
	{binary: "solargraph", languageIDs: []string{"ruby"}, versionArgs: []string{"--version"}},
	{binary: "csharp-ls", languageIDs: []string{"csharp"}, versionArgs: []string{"--version"}},
	{binary: "omnisharp", languageIDs: []string{"csharp"}, versionArgs: []string{"--version"}},
	{binary: "intelephense", languageIDs: []string{"php"}, versionArgs: []string{"--version"}},
	{binary: "kotlin-language-server", languageIDs: []string{"kotlin"}, versionArgs: []string{"--version"}},
	{binary: "sourcekit-lsp", languageIDs: []string{"swift"}, versionArgs: []string{"--version"}},
	{binary: "haskell-language-server-wrapper", languageIDs: []string{"haskell"}, versionArgs: []string{"--version"}},
	{binary: "ocamllsp", languageIDs: []string{"ocaml"}, versionArgs: []string{"--version"}},
	{binary: "elixir-ls", languageIDs: []string{"elixir"}, versionArgs: []string{"--version"}},
	{binary: "zls", languageIDs: []string{"zig"}, versionArgs: []string{"--version"}},
	{binary: "terraform-ls", languageIDs: []string{"terraform"}, versionArgs: []string{"version"}},
	{binary: "dart", languageIDs: []string{"dart"}, versionArgs: []string{"--version"}},
}

// Detect scans the process PATH for known language servers. Servers that are
// not installed are omitted. Version probing is best-effort and bounded.
func Detect(ctx context.Context) []ServerInfo {
	return DetectInPath(ctx, os.Getenv("PATH"))
}

// DetectInPath is Detect with an explicit PATH-style search list. It exists so
// callers and tests can control the lookup without mutating the environment.
func DetectInPath(ctx context.Context, pathEnv string) []ServerInfo {
	if ctx == nil {
		ctx = context.Background()
	}

	var found = make([]ServerInfo, 0, len(knownServers))
	for _, known := range knownServers {
		if err := ctx.Err(); err != nil {
			break
		}
		path, ok := findExecutable(pathEnv, known.binary)
		if !ok {
			continue
		}
		found = append(found, ServerInfo{
			Name:        known.binary,
			Path:        path,
			Version:     probeVersion(ctx, path, known.versionArgs),
			LanguageIDs: append([]string(nil), known.languageIDs...),
		})
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}

// findExecutable is exec.LookPath with an explicit search path. It checks the
// executable bit on Unix and PATHEXT-style suffixes on Windows.
func findExecutable(pathEnv, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		for _, candidate := range executableCandidates(filepath.Join(dir, name)) {
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
				continue
			}
			abs, err := filepath.Abs(candidate)
			if err != nil {
				abs = candidate
			}
			return abs, true
		}
	}
	return "", false
}

func executableCandidates(path string) []string {
	if runtime.GOOS != "windows" {
		return []string{path}
	}
	exts := filepath.SplitList(os.Getenv("PATHEXT"))
	if len(exts) == 0 {
		exts = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	if ext := filepath.Ext(path); ext != "" {
		return []string{path}
	}
	out := make([]string, 0, len(exts))
	for _, ext := range exts {
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out = append(out, path+strings.ToLower(ext), path+strings.ToUpper(ext))
	}
	return out
}

// probeVersion runs the server's cheap version flag and returns the first
// non-empty output line. Any failure degrades to an empty version rather than
// failing detection.
func probeVersion(ctx context.Context, path string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	vctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()

	cmd := exec.CommandContext(vctx, path, args...)
	cmd.WaitDelay = versionWaitDelay
	out, _ := cmd.CombinedOutput()
	if len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > maxVersionLine {
			line = line[:maxVersionLine] + "…"
		}
		return line
	}
	return ""
}
