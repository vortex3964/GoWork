//DESC: file_search locates files by NAME or path pattern by shelling out to
// ripgrep (rg --files) when available, with a portable Go walker fallback.
package filesearchtool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"GoWork/tools"

	ignore "github.com/sabhiram/go-gitignore"
)

const (
	DEFAULT_LIMIT     int = 100       // max matches returned, mirrors read_file's paging cap
	DEFAULT_MAX_BYTES int = 50 * 1024 // total output cap, same budget as read_file
)

type Input struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Include    string `json:"include"`
	IgnoreCase bool   `json:"ignore_case"`
	Literal    bool   `json:"literal"`
	Limit      int    `json:"limit"`
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "file_search" }

func (t *Tool) Description() string {
	return fmt.Sprintf(`Quickly locates files by NAME or path pattern. Uses ripgrep (rg) when it is available and a built-in fallback otherwise. Respects .gitignore and .agentignore, and skips hidden files and directories (dotfiles such as .env and .git) by default.

Returns one project-relative path per line. Use this to find where a file lives (pattern "todoList" returns "Tui/todoList.go"), to confirm a file exists before creating anything, or to glob for files by name.
Output is capped at %d matches or %d bytes, whichever is hit first. If you hit the match cap, raise limit or narrow pattern/include.

file_search only matches file PATHS, never file contents. To search what files SAY, use grep_search instead.`, DEFAULT_LIMIT, DEFAULT_MAX_BYTES)
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to match against file paths (or literal text if literal=true).",
			},
			"path": map[string]any{
				"type":        "string",
				"description": `Directory to search, relative to the project root. Defaults to the whole project.`,
			},
			"include": map[string]any{
				"type":        "string",
				"description": `Glob to filter which files are considered, e.g. "*.go" or "**/*_test.go".`,
			},
			"ignore_case": map[string]any{
				"type":        "boolean",
				"description": "Case-insensitive matching. Defaults to false.",
			},
			"literal": map[string]any{
				"type":        "boolean",
				"description": "Treat pattern as literal text instead of regex. Defaults to false.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Maximum number of paths to return. Defaults to %d.", DEFAULT_LIMIT),
			},
		},
		"required":               []string{"pattern"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

// cleanRelPath validates a user-supplied path the same way read_file does:
// reject absolute paths and ".." escapes so everything stays under the
// project root, even though here we're about to hand a real filesystem
// path to a subprocess rather than going through args.Root for the search
// itself (os.Root doesn't extend to child processes).
func cleanRelPath(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	rel := filepath.Clean(p)
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be relative to the project root, got %q", p)
	}
	return rel, nil
}

// relativizeToRoot turns an absolute path rg reports back into a
// project-root-relative, forward-slashed path, matching the "path"
// convention every other tool in this package uses.
func relativizeToRoot(root, absPath string) string {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// runRGFileList lists every file under searchPath with ripgrep. rg applies
// .gitignore/.agentignore itself; the caller filters the resulting relative
// paths against the pattern in Go.
func runRGFileList(ctx context.Context, rgPath, root, searchPath, include string) ([]string, error) {
	rgArgs := []string{"--files", "--color=never"}
	if include != "" {
		rgArgs = append(rgArgs, "--glob", include)
	}
	if agentIgnore := filepath.Join(root, ".agentignore"); fileExists(agentIgnore) {
		rgArgs = append(rgArgs, "--ignore-file", agentIgnore)
	}
	rgArgs = append(rgArgs, "--", searchPath)

	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("starting ripgrep: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting ripgrep: %w", err)
	}

	var paths []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		paths = append(paths, scanner.Text())
	}
	scanErr := scanner.Err()

	// The child only reads, so even if we stopped early it exits on its own;
	// still wait to reap it and to surface real failures.
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if scanErr != nil {
		return nil, fmt.Errorf("reading ripgrep output: %w", scanErr)
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return nil, fmt.Errorf("running ripgrep: %w", waitErr)
	}
	if exitErr != nil && exitErr.ExitCode() != 1 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("ripgrep exited with code %d", exitErr.ExitCode())
		}
		return nil, errors.New(msg)
	}
	return paths, nil
}

// fallbackIgnores loads the ignore files a directory scan would honor.
// .gitignore only applies inside a git repository (rg's default behavior), so
// it is skipped otherwise; .agentignore always applies.
func fallbackIgnores(root string) []*ignore.GitIgnore {
	var ignores []*ignore.GitIgnore
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		if gi, err := ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore")); err == nil {
			ignores = append(ignores, gi)
		}
	}
	if ai, err := ignore.CompileIgnoreFile(filepath.Join(root, ".agentignore")); err == nil {
		ignores = append(ignores, ai)
	}
	return ignores
}

func isIgnoredPath(ignores []*ignore.GitIgnore, rel string) bool {
	return tools.IsIgnored(ignores, rel) || tools.IsIgnored(ignores, rel+"/")
}

func isHiddenName(name string) bool {
	return len(name) > 1 && name[0] == '.'
}

// fallbackFileList walks the search scope in Go when ripgrep is unavailable.
// It returns absolute paths, matching rg --files, after applying the same
// hidden-file, ignore and include filters.
func fallbackFileList(ctx context.Context, root, searchPath string, scopeIsDir bool, include string) ([]string, error) {
	if !scopeIsDir {
		// An explicitly named file is returned as-is, just like rg --files.
		return []string{searchPath}, nil
	}

	var includeMatch func(string) bool
	if include != "" {
		includeMatch = ignore.CompileIgnoreLines(include).MatchesPath
	}
	ignores := fallbackIgnores(root)

	var paths []string
	err := filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d == nil {
				return walkErr // the scope itself is unreadable/missing
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == searchPath {
			return nil // an explicitly named scope is searched as-is
		}

		name := d.Name()
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if isHiddenName(name) || isIgnoredPath(ignores, rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if isHiddenName(name) || isIgnoredPath(ignores, rel) {
			return nil
		}
		if includeMatch != nil && !includeMatch(rel) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}

	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("file_search: invalid input: %w", err)
	}
	if input.Pattern == "" {
		return tools.Errf("pattern is required"), nil
	}

	relPath, err := cleanRelPath(input.Path)
	if err != nil {
		return tools.Errf("%s", err.Error()), nil
	}

	// Same existence check read_file does
	info, err := args.Root.Stat(relPath)
	if errors.Is(err, os.ErrNotExist) {
		return tools.Errf("path %v does not exist", input.Path), nil
	} else if err != nil {
		return tools.Errf("checking %v: %v", input.Path, err), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = DEFAULT_LIMIT
	}

	re, err := regexp.Compile(compilePattern(input))
	if err != nil {
		return tools.Errf("invalid pattern: %v", err), nil
	}

	// Prefer ripgrep. When it is missing, the portable Go walker below lists
	// the same files while applying the same hidden/ignore/include filters.
	rgPath, _ := exec.LookPath("rg")
	searchPath := filepath.Join(*args.RootPath, relPath)

	var paths []string
	if rgPath != "" {
		paths, err = runRGFileList(ctx, rgPath, *args.RootPath, searchPath, input.Include)
	} else {
		paths, err = fallbackFileList(ctx, *args.RootPath, searchPath, info.IsDir(), input.Include)
	}
	if err != nil {
		if ctx.Err() != nil {
			return tools.ToolResult{}, ctx.Err()
		}
		return tools.Errf("%v", err), nil
	}

	var out strings.Builder
	matched := 0
	limitHit := false
	bytesHit := false
	for _, p := range paths {
		rel := relativizeToRoot(*args.RootPath, p)
		if !re.MatchString(rel) {
			continue
		}
		if out.Len()+len(rel)+1 > DEFAULT_MAX_BYTES {
			bytesHit = true
			break
		}
		out.WriteString(rel)
		out.WriteByte('\n')
		matched++
		if matched >= limit {
			limitHit = true
			break
		}
	}

	if matched == 0 {
		return tools.Ok(fmt.Sprintf("No files found whose path matches %q. If you expected this file to exist, it does not - check with list_directory before creating or guessing paths.", input.Pattern)), nil
	}

	content := out.String()
	var notices []string
	if limitHit {
		notices = append(notices, fmt.Sprintf("%d matches limit reached, raise limit or narrow pattern/include", limit))
	}
	if bytesHit {
		notices = append(notices, fmt.Sprintf("%d byte limit reached, narrow the search", DEFAULT_MAX_BYTES))
	}
	if len(notices) > 0 {
		content += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
	}
	return tools.Ok(content), nil
}

func compilePattern(input Input) string {
	p := input.Pattern
	if input.Literal {
		p = regexp.QuoteMeta(p)
	}
	if input.IgnoreCase {
		p = "(?i)" + p
	}
	return p
}
