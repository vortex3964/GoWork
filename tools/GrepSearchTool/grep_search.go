//DESC: grep_search searches file contents for a pattern by shelling out to
// ripgrep (rg) instead of reimplementing search in Go.
package grepsearchtool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/tools"

	ignore "github.com/sabhiram/go-gitignore"
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
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "grep_search" }

func (t *Tool) Description() string {
	return fmt.Sprintf(`Search file CONTENTS for a regex pattern using ripgrep. Respects .gitignore and .agentignore, and skips hidden files and directories (dotfiles such as .env and .git) by default.

Returns matching lines as "path:line: text", the same format ripgrep prints to a terminal. Context lines (if requested via context_lines) are shown as "path-line- text" instead, so match lines always stand out.
Output is capped at %d matches or %d bytes, whichever is hit first. If you hit the match cap, raise limit or narrow pattern/include. If you hit the byte cap, narrow the search instead - raising limit won't help.

Use this to find where a symbol, string, or error message appears in code. To locate a file BY NAME or check a path exists, use file_search instead - grep_search never matches filenames.`, DEFAULT_LIMIT, DEFAULT_MAX_BYTES)
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
		},
		"required":               []string{"pattern"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

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

// searchFlags are the ripgrep options that affect how a pattern is matched.
// They are shared by the on-disk scan and the stdin scans done for files with
// pending changes, so both search with identical semantics.
func searchFlags(input Input) []string {
	flags := []string{"--json", "--line-number", "--color=never"}
	if input.IgnoreCase {
		flags = append(flags, "--ignore-case")
	}
	if input.Literal {
		flags = append(flags, "--fixed-strings")
	}
	if input.ContextLines > 0 {
		flags = append(flags, "--context", fmt.Sprintf("%d", input.ContextLines))
	}
	return flags
}

// pendingIgnores loads the ignore files rg would honor for a directory scan.
// .gitignore only applies inside a git repository (rg's default behavior), so
// it is skipped otherwise; .agentignore always applies.
func pendingIgnores(root string) []*ignore.GitIgnore {
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

// isHiddenPath reports whether rel has a dot-prefixed component, matching
// what ripgrep treats as hidden when --hidden is not passed.
func isHiddenPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if len(part) > 1 && part[0] == '.' {
			return true
		}
	}
	return false
}

// pendingFiles returns the root-relative, slash-separated paths of watched
// files that have pending changes and fall inside the search. scopeIsDir
// distinguishes a directory search (where include, hidden and ignore rules
// apply) from an explicitly named file, which rg searches regardless of them.
func pendingFiles(wl *cl.WatchList, root, scopeRel string, scopeIsDir bool, include string) []string {
	if wl == nil {
		return nil
	}

	scopeRel = filepath.ToSlash(filepath.Clean(scopeRel))
	scopeRoot := scopeRel == "."

	var includeMatch func(string) bool
	if include != "" {
		includeMatch = ignore.CompileIgnoreLines(include).MatchesPath
	}

	var ignores []*ignore.GitIgnore
	if scopeIsDir {
		ignores = pendingIgnores(root)
	}

	var files []string
	for _, watched := range wl.Files() {
		rel := filepath.ToSlash(filepath.Clean(watched))
		if filepath.IsAbs(watched) {
			if r, err := filepath.Rel(root, watched); err == nil {
				rel = filepath.ToSlash(r)
			}
		}

		if !scopeRoot {
			if scopeIsDir {
				if rel != scopeRel && !strings.HasPrefix(rel, scopeRel+"/") {
					continue
				}
			} else if rel != scopeRel {
				continue
			}
		}
		if scopeIsDir {
			// rg applies its hidden filter below an explicitly named
			// directory, not to the directory itself, so strip the scope
			// before checking for a dot-prefixed component.
			hiddenRel := rel
			if !scopeRoot && strings.HasPrefix(rel, scopeRel+"/") {
				hiddenRel = strings.TrimPrefix(rel, scopeRel+"/")
			}
			if isHiddenPath(hiddenRel) {
				continue
			}
			if includeMatch != nil && !includeMatch(rel) {
				continue
			}
			if len(ignores) > 0 && tools.IsIgnored(ignores, rel) {
				continue
			}
		}
		if len(wl.GetChanges(rel)) == 0 {
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files
}

// acceptedView reads relPath and applies its pending changes in memory,
// returning the same bytes read_file shows the model.
func acceptedView(root *os.Root, wl *cl.WatchList, relPath string) ([]byte, error) {
	f, err := root.Open(relPath)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if changes := wl.GetChanges(relPath); len(changes) > 0 {
		data = cl.AcceptAllChangesBytes(data, changes)
	}
	return data, nil
}

// resultSink accumulates formatted match/context lines while enforcing the
// same match, byte and line-length caps as a regular scan.
type resultSink struct {
	out               strings.Builder
	limit             int
	matchCount        int
	matchLimitReached bool
	bytesTruncated    bool
	linesTruncated    bool
	lastFile          string
	lastLine          int
}

// add appends one match/context event for relFile. It reports whether the
// scan should stop because a cap was reached.
func (s *resultSink) add(relFile string, evt rgEvent) bool {
	text, wasTruncated := truncateLine(strings.TrimRight(evt.Data.Lines.Text, "\n"))
	if wasTruncated {
		s.linesTruncated = true
	}

	sep := ":"
	if evt.Type == "context" {
		sep = "-"
	}

	var block strings.Builder
	if relFile != s.lastFile {
		if s.lastFile != "" {
			block.WriteString("\n") // blank line between files
		}
	} else if evt.Data.LineNumber > s.lastLine+1 {
		block.WriteString("--\n")
	}
	fmt.Fprintf(&block, "%s%s%d%s %s\n", relFile, sep, evt.Data.LineNumber, sep, text)

	if s.out.Len()+block.Len() > DEFAULT_MAX_BYTES {
		s.bytesTruncated = true
		return true
	}
	s.out.WriteString(block.String())
	s.lastFile, s.lastLine = relFile, evt.Data.LineNumber

	if evt.Type == "match" {
		s.matchCount++
		if s.matchCount >= s.limit {
			s.matchLimitReached = true
			return true
		}
	}
	return false
}

// runRGOnAccepted pipes content through rg on stdin so pending changes are
// searched in their accepted form rather than the marked-up file on disk. rg
// reports "<stdin>" as the path, so every event is relabeled with relFile.
// handle reports whether the scan should stop early.
func runRGOnAccepted(ctx context.Context, rgPath string, rgArgs []string, content []byte, relFile string, handle func(string, rgEvent) bool) (stopped bool, err error) {
	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	cmd.Stdin = bytes.NewReader(content)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, fmt.Errorf("starting ripgrep: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("starting ripgrep: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt rgEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		if evt.Type != "match" && evt.Type != "context" {
			continue
		}
		if handle(relFile, evt) {
			stopped = true
			break
		}
	}
	scanErr := scanner.Err()

	// Killing first is safe: SIGKILL unblocks any pending write, so Wait()
	// below won't deadlock even though we stopped reading before EOF.
	if stopped || scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return stopped, ctx.Err()
	}
	if scanErr != nil {
		return stopped, fmt.Errorf("reading ripgrep output: %w", scanErr)
	}

	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return stopped, fmt.Errorf("running ripgrep: %w", waitErr)
	}
	// Exit code 1 means "ran fine, found nothing" for rg.
	if exitErr != nil && exitErr.ExitCode() != 1 && !stopped {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("ripgrep exited with code %d", exitErr.ExitCode())
		}
		return stopped, errors.New(msg)
	}
	return stopped, nil
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}

	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("grep_search: invalid input: %w", err)
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

	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return tools.Errf("ripgrep (rg) is not installed or not on PATH"), nil
	}

	// Files with pending changes are marked up on disk, so rg would report
	// marker/old-side lines that read_file never shows. Skip them in the
	// on-disk scan and search their accepted view through stdin below.
	pending := pendingFiles(args.WatchList, *args.RootPath, relPath, info.IsDir(), input.Include)
	pendingSet := make(map[string]struct{}, len(pending))
	for _, relFile := range pending {
		pendingSet[relFile] = struct{}{}
	}

	searchPath := filepath.Join(*args.RootPath, relPath)

	rgArgs := searchFlags(input)
	if input.Include != "" {
		rgArgs = append(rgArgs, "--glob", input.Include)
	}

	if agentIgnore := filepath.Join(*args.RootPath, ".agentignore"); fileExists(agentIgnore) {
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

	sink := &resultSink{limit: limit}

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

		relFile := relativizeToRoot(*args.RootPath, evt.Data.Path.Text)
		if _, ok := pendingSet[relFile]; ok {
			continue // pending files are searched via their accepted view below
		}
		if sink.add(relFile, evt) {
			break
		}
	}
	scanErr := scanner.Err()

	// Stop rg immediately once we've got enough (or hit a read error) -
	// otherwise it's still busy walking the tree. Killing first is safe:
	// SIGKILL unblocks any pending write, so Wait() below won't deadlock
	// even though we stopped reading before EOF.
	if sink.matchLimitReached || sink.bytesTruncated || scanErr != nil {
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
	if exitErr != nil && exitErr.ExitCode() != 1 && !sink.matchLimitReached && !sink.bytesTruncated {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("ripgrep exited with code %d", exitErr.ExitCode())
		}
		return tools.Errf("%s", msg), nil
	}

	// Search the accepted view of every pending file. Each file goes through
	// rg on stdin so matching, context and line numbers behave exactly like
	// the on-disk scan, while the output caps stay shared.
	pendingArgs := append(searchFlags(input), "--", input.Pattern, "-")
	for _, relFile := range pending {
		if sink.matchLimitReached || sink.bytesTruncated {
			break
		}
		if err := ctx.Err(); err != nil {
			return tools.ToolResult{}, err
		}

		content, err := acceptedView(args.Root, args.WatchList, relFile)
		if err != nil {
			return tools.Errf("reading pending changes in %s: %v", relFile, err), nil
		}
		if len(content) == 0 {
			continue
		}
		if _, err := runRGOnAccepted(ctx, rgPath, pendingArgs, content, relFile, sink.add); err != nil {
			if ctx.Err() != nil {
				return tools.ToolResult{}, ctx.Err()
			}
			return tools.Errf("%v", err), nil
		}
	}

	if sink.matchCount == 0 {
		return tools.Ok("No matches found"), nil
	}

	content := sink.out.String()
	var notices []string
	if sink.matchLimitReached {
		notices = append(notices, fmt.Sprintf("%d matches limit reached, raise limit or narrow pattern/include", limit))
	}
	if sink.bytesTruncated {
		notices = append(notices, fmt.Sprintf("%d byte limit reached, narrow the search", DEFAULT_MAX_BYTES))
	}
	if sink.linesTruncated {
		notices = append(notices, fmt.Sprintf("some lines truncated to %d chars, use read_file to see full lines", MAX_LINE_LENGTH))
	}
	if len(notices) > 0 {
		content += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
	}

	return tools.Ok(content), nil
}
