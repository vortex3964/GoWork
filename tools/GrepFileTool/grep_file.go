//DESC: grep_file searches file contents for a pattern by shelling out to
// ripgrep (rg) instead of reimplementing search in Go. 
package grepfiletool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"GoWork/tools"
)

const (
	DEFAULT_LIMIT     int = 100       // max matches returned, mirrors read_file's paging cap
	DEFAULT_MAX_BYTES int = 50 * 1024 // total output cap, same budget as read_file
	MAX_LINE_LENGTH   int = 500       // per-line cap so one huge minified line can't eat the whole budget
)

type Input struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path"`
	Include      string `json:"include"`
	IgnoreCase   bool   `json:"ignore_case"`
	Literal      bool   `json:"literal"`
	ContextLines int    `json:"context_lines"`
	Limit        int    `json:"limit"`
	InFilenames  bool   `json:"in_filenames"`
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "grep_file" }

func (t *Tool) Description() string {
	return fmt.Sprintf(`Search file contents for a regex pattern using ripgrep. Respects .gitignore and .agentignore.

Returns matching lines as "path:line: text", the same format ripgrep prints to a terminal. Context lines (if requested via context_lines) are shown as "path-line- text" instead, so match lines always stand out.
Output is capped at %d matches or %d bytes, whichever is hit first. If you hit the match cap, raise limit or narrow pattern/include. If you hit the byte cap, narrow the search instead - raising limit won't help.

Searching by FILE NAME (in_filenames=true): by default grep_file only searches file CONTENTS, so a filename is never matched. Set in_filenames=true to match against file paths instead - use this to find where a file lives (pattern "todoList" returns "Tui/todoList.go"), to check a file exists before creating anything, or to glob for files by name. It returns one matching path per line.`, DEFAULT_LIMIT, DEFAULT_MAX_BYTES)
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to search for (or literal text if literal=true).",
			},
			"path": map[string]any{
				"type":        "string",
				"description": `Directory or file to search, relative to the project root. Defaults to the whole project.`,
			},
			"include": map[string]any{
				"type":        "string",
				"description": `Glob to filter which files are searched, e.g. "*.go" or "**/*_test.go".`,
			},
			"ignore_case": map[string]any{
				"type":        "boolean",
				"description": "Case-insensitive search. Defaults to false.",
			},
			"literal": map[string]any{
				"type":        "boolean",
				"description": "Treat pattern as literal text instead of regex. Defaults to false.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"description": "Lines of context to show before and after each match. Defaults to 0.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Maximum number of matches to return. Defaults to %d.", DEFAULT_LIMIT),
			},
			"in_filenames": map[string]any{
				"type":        "boolean",
				"description": "If true, match the pattern against file paths (names) instead of file contents. Use this to find where a file lives or confirm it exists. Defaults to false.",
			},
		},
		"required":               []string{"pattern"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindSearch }

// rgEvent mirrors the subset of ripgrep's --json event schema we care
// about. rg emits one of these per line: "begin"/"end" per file, "match"
// and "context" per matched/surrounding line, and a final "summary" we
// only read the two perline types and ignore the rest.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

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

// truncateLine caps a single line's length, trimming back to the nearest
// full rune so we never split a multi-byte UTF-8 character in half.
func truncateLine(s string) (string, bool) {
	if len(s) <= MAX_LINE_LENGTH {
		return s, false
	}
	cut := s[:MAX_LINE_LENGTH]
	for len(cut) > 0 && !utf8.RuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1]
	}
	return cut + "...", true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}

	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("grep_file: invalid input: %w", err)
	}
	if input.Pattern == "" {
		return tools.Errf("pattern is required"), nil
	}

	relPath, err := cleanRelPath(input.Path)
	if err != nil {
		return tools.Errf("%s", err.Error()), nil
	}

	// Same existence check read_file does 
	if _, err := args.Root.Stat(relPath); errors.Is(err, os.ErrNotExist) {
		return tools.Errf("path %v does not exist", input.Path), nil
	} else if err != nil {
		return tools.Errf("checking %v: %v", input.Path, err), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = DEFAULT_LIMIT
	}

	if input.InFilenames {
		return t.searchFilenames(ctx, args, input, relPath, limit)
	}

	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return tools.Errf("ripgrep (rg) is not installed or not on PATH"), nil
	}

	searchPath := filepath.Join(args.RootPath, relPath)

	rgArgs := []string{"--json", "--line-number", "--color=never", "--hidden"}
	if input.IgnoreCase {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if input.Literal {
		rgArgs = append(rgArgs, "--fixed-strings")
	}
	if input.ContextLines > 0 {
		rgArgs = append(rgArgs, "--context", fmt.Sprintf("%d", input.ContextLines))
	}
	if input.Include != "" {
		rgArgs = append(rgArgs, "--glob", input.Include)
	}

	if agentIgnore := filepath.Join(args.RootPath, ".agentignore"); fileExists(agentIgnore) {
		rgArgs = append(rgArgs, "--ignore-file", agentIgnore)
	}
	// NOTE: no --follow, deliberately - keeps rg from walking symlinks
	// out of the project root.
	rgArgs = append(rgArgs, "--", input.Pattern, searchPath)

	// CommandContext ties the child's lifetime to ctx: if the caller
	// cancels, the process is killed for us with no manual signal wiring.
	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return tools.Errf("starting ripgrep: %v", err), nil
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return tools.Errf("starting ripgrep: %v", err), nil
	}

	var out strings.Builder
	matchCount := 0
	matchLimitReached := false
	bytesTruncated := false
	linesTruncated := false
	lastFile, lastLine := "", 0

	scanner := bufio.NewScanner(stdout)
	// default token (line) size is 64KB; bump it so a single huge rg JSON
	// line (e.g. from a minified source file) doesn't trip ErrTooLong
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt rgEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue // not a JSON line we understand, skip it
		}
		if evt.Type != "match" && evt.Type != "context" {
			continue // ignore begin/end/summary events
		}

		relFile := relativizeToRoot(args.RootPath, evt.Data.Path.Text)
		text, wasTruncated := truncateLine(strings.TrimRight(evt.Data.Lines.Text, "\n"))
		if wasTruncated {
			linesTruncated = true
		}

		sep := ":"
		if evt.Type == "context" {
			sep = "-"
		}

		var block strings.Builder
		if relFile != lastFile {
			if lastFile != "" {
				block.WriteString("\n") // blank line between files
			}
		} else if evt.Data.LineNumber > lastLine+1 {
			block.WriteString("--\n")
		}
		fmt.Fprintf(&block, "%s%s%d%s %s\n", relFile, sep, evt.Data.LineNumber, sep, text)

		if out.Len()+block.Len() > DEFAULT_MAX_BYTES {
			bytesTruncated = true
			break
		}
		out.WriteString(block.String())
		lastFile, lastLine = relFile, evt.Data.LineNumber

		if evt.Type == "match" {
			matchCount++
			if matchCount >= limit {
				matchLimitReached = true
				break
			}
		}
	}
	scanErr := scanner.Err()

	// Stop rg immediately once we've got enough (or hit a read error) -
	// otherwise it's still busy walking the tree. Killing first is safe:
	// SIGKILL unblocks any pending write, so Wait() below won't deadlock
	// even though we stopped reading before EOF.
	if matchLimitReached || bytesTruncated || scanErr != nil {
		_ = cmd.Process.Kill()
	}

	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return tools.ToolResult{}, ctx.Err()
	}
	if scanErr != nil {
		return tools.Errf("reading ripgrep output: %v", scanErr), nil
	}

	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return tools.Errf("running ripgrep: %v", waitErr), nil
	}
	// Exit code 1 means "ran fine, found nothing" for rg - not a failure.
	// Any other non-zero code only counts as a real failure if we didn't
	// kill the process ourselves.
	if exitErr != nil && exitErr.ExitCode() != 1 && !matchLimitReached && !bytesTruncated {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("ripgrep exited with code %d", exitErr.ExitCode())
		}
		return tools.Errf("%s", msg), nil
	}

	if matchCount == 0 {
		return tools.Ok("No matches found"), nil
	}

	content := out.String()
	var notices []string
	if matchLimitReached {
		notices = append(notices, fmt.Sprintf("%d matches limit reached, raise limit or narrow pattern/include", limit))
	}
	if bytesTruncated {
		notices = append(notices, fmt.Sprintf("%d byte limit reached, narrow the search", DEFAULT_MAX_BYTES))
	}
	if linesTruncated {
		notices = append(notices, fmt.Sprintf("some lines truncated to %d chars, use read_file to see full lines", MAX_LINE_LENGTH))
	}
	if len(notices) > 0 {
		content += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
	}

	return tools.Ok(content), nil
}

// searchFilenames lists every file under the search path (via rg --files,
// so .gitignore/.agentignore still apply) and reports the ones whose path
// matches the pattern.
func (t *Tool) searchFilenames(ctx context.Context, args tools.DispatchArgs, input Input, relPath string, limit int) (tools.ToolResult, error) {
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return tools.Errf("ripgrep (rg) is not installed or not on PATH"), nil
	}

	rgArgs := []string{"--files", "--hidden", "--color=never"}
	if input.Include != "" {
		rgArgs = append(rgArgs, "--glob", input.Include)
	}
	if agentIgnore := filepath.Join(args.RootPath, ".agentignore"); fileExists(agentIgnore) {
		rgArgs = append(rgArgs, "--ignore-file", agentIgnore)
	}
	rgArgs = append(rgArgs, "--", filepath.Join(args.RootPath, relPath))

	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return tools.Errf("starting ripgrep: %v", err), nil
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return tools.Errf("starting ripgrep: %v", err), nil
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
		return tools.ToolResult{}, ctx.Err()
	}
	if scanErr != nil {
		return tools.Errf("reading ripgrep output: %v", scanErr), nil
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return tools.Errf("running ripgrep: %v", waitErr), nil
	}
	if exitErr != nil && exitErr.ExitCode() != 1 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("ripgrep exited with code %d", exitErr.ExitCode())
		}
		return tools.Errf("%s", msg), nil
	}

	re, err := regexp.Compile(compilePattern(input))
	if err != nil {
		return tools.Errf("invalid pattern: %v", err), nil
	}

	var out strings.Builder
	matched := 0
	limitHit := false
	bytesHit := false
	for _, p := range paths {
		rel := relativizeToRoot(args.RootPath, p)
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
