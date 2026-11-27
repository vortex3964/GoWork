package readfiletool

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
	"strings"

	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/internal/approot"
	"GoWork/tools"
)

const (
	DEFAULT_MAX_BYTES int = 50 * 1024
	DEFAULT_MAX_LINES int = 1800

	// pdfScriptRel is the pdf parser subprocess, relative to the app root.
	pdfScriptRel = "tools/Scripts/read_pdf.py"

	// pdfProjectScriptsEnv opts in to running PDF parser code from the
	// project being read. Project scripts and venvs are arbitrary code, so
	// they stay disabled unless the user explicitly trusts them.
	pdfProjectScriptsEnv = "GOWORK_ALLOW_PROJECT_PDF_SCRIPTS"

	// maxPDFStdoutBytes and maxPDFStderrBytes bound the memory a parser run
	// can hold. A hostile or buggy script must not be able to grow the
	// process without bound; excess output is discarded.
	maxPDFStdoutBytes = 4 << 20
	maxPDFStderrBytes = 64 << 10
)

// imageExtensions are refused with an error: the provider layer is text-only,
// so the model can never actually see an image, and dumping base64 into the
// context just wastes tokens.
var imageExtensions = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true,
	"bmp": true, "tiff": true, "svg": true, "ico": true,
}

type Input struct {
	Path   string `json:"path"`
	Start  int    `json:"starting_line"`
	Offset int    `json:"offset_lines"`
}

type Tool struct{}

// NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "read_file" }

func (t *Tool) Description() string {
	return `Read a range of lines from a text file.

Returns the requested lines with 1-indexed line numbers prefixed, so you can reference exact locations in later edits. Large files are not returned in full use starting_line and offset_lines to page through them.`
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `Path of the file to read, relative to the project root (e.g. "src/foo.go").`,
			},
			"starting_line": map[string]any{
				"type":        "integer",
				"description": "1-indexed line number to start reading from. Defaults to 1 if omitted.",
			},
			"offset_lines": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Number of lines to read starting from starting_line. Defaults to the rest of the file if omitted, capped at %d lines.", DEFAULT_MAX_LINES),
			},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

// readRange returns the requested line range from relPath.
//
// When the file has no pending WatchList changes, src is streamed through a
// scanner that stops after the requested page (or the byte cap), so the rest
// of the file is never read. Pending changes are stored as byte offsets into
// the on-disk marker file, so that path still has to materialise the whole
// file before applying them.
func readRange(root *os.Root, wl *cl.WatchList, relPath string, start, offset int) (content string, truncated bool, ok bool, lastLine int, err error) {
	//Open the original source file via the sandboxed root
	src, err := root.Open(relPath)
	if err != nil {
		return "", false, false, 0, fmt.Errorf("opening source %s: %w", relPath, err)
	}

	var changes []cl.Change
	if wl != nil {
		changes = wl.GetChanges(relPath)
	}
	if len(changes) > 0 {
		// Pending changes refer to offsets in the on-disk file, so the whole
		// file is needed to resolve them; apply them in memory as before.
		data, readErr := io.ReadAll(src)
		src.Close()
		if readErr != nil {
			return "", false, false, 0, fmt.Errorf("reading source %s: %w", relPath, readErr)
		}
		data = cl.AcceptAllChangesBytes(data, changes)
		return scanLineRange(bytes.NewReader(data), relPath, start, offset)
	}

	// Common case: no pending edits. Stream the file and stop as soon as the
	// requested range ends instead of slurping the whole file.
	defer src.Close()
	return scanLineRange(src, relPath, start, offset)
}

// scanLineRange scans r until the 1-indexed [start, start+offset-1] line
// range has been produced, the byte cap is reached, or the input ends. It
// never reads past the line after the requested page, so callers can pass a
// stream backed by a large file.
func scanLineRange(r io.Reader, relPath string, start, offset int) (content string, truncated bool, ok bool, lastLine int, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // Allow up to 10MB per line

	end := start + offset - 1
	var sb strings.Builder
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if lineNo > end {
			break
		}

		line := fmt.Sprintf("%d.%s\n", lineNo, scanner.Text())
		if sb.Len()+len(line) > DEFAULT_MAX_BYTES {
			truncated = true
			break
		}
		sb.WriteString(line)
	}

	if err := scanner.Err(); err != nil {
		return "", false, false, 0, fmt.Errorf("reading file %s: %w", relPath, err)
	}

	if lineNo < start {
		return "", false, false, lineNo, nil
	}

	return sb.String(), truncated, true, lineNo, nil
}

// pdfProjectScriptsAllowed reports whether the user opted in to running the
// PDF parser script and venv from the project being read.
func pdfProjectScriptsAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(pdfProjectScriptsEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// venvPython returns the interpreter inside root/tools/Scripts/venv for the
// unix and windows layouts, or "" when no regular file exists there.
func venvPython(root string) (string, bool) {
	if root == "" {
		return "", false
	}
	for _, rel := range []string{
		"tools/Scripts/venv/bin/python",
		"tools/Scripts/venv/Scripts/python.exe",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// findPDFScript locates the parser script. The app-root copy is trusted;
// a project-local script is only used when the user opted in, because it is
// code controlled by the project being read.
func findPDFScript(appRoot, projectRoot string, allowProject bool) (string, bool) {
	if appRoot != "" {
		if p, ok := approot.FindFile(pdfScriptRel, appRoot); ok {
			return p, true
		}
	}
	if allowProject && projectRoot != "" {
		if p, ok := approot.FindFile(pdfScriptRel, projectRoot); ok {
			return p, true
		}
	}
	return "", false
}

// findPDFPython returns the interpreter for the pdf parser. Trusted choices
// win: the app-root venv first, then system python3/python. A project venv is
// only considered when the user opted in, and even then it comes last.
func findPDFPython(appRoot, projectRoot string, allowProject bool) string {
	if p, ok := venvPython(appRoot); ok {
		return p
	}
	for _, c := range []string{"python3", "python"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	if allowProject {
		if p, ok := venvPython(projectRoot); ok {
			return p
		}
	}
	return ""
}

// cappedBuffer keeps at most max bytes and silently discards the rest, so a
// chatty subprocess can never grow our memory without bound. Each stream gets
// its own instance and os/exec writes to it from one goroutine.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newCappedBuffer(max int) *cappedBuffer {
	return &cappedBuffer{max: max}
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		if n > 0 {
			c.truncated = true
		}
		return n, nil
	}
	if n > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return n, nil
	}
	c.buf.Write(p)
	return n, nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

func (c *cappedBuffer) Len() int { return c.buf.Len() }

func (c *cappedBuffer) Truncated() bool { return c.truncated }

// readPDFViaScript runs the read_pdf.py parser over absPDF and returns its
// stdout. The parser script and optional venv are resolved from the installed
// app root and the system Python first; project-local parser code is only
// used when GOWORK_ALLOW_PROJECT_PDF_SCRIPTS enables it. There is no timeout:
// a conversion may legitimately take a long time (marker's first run downloads
// its models), so it runs until it finishes or the caller's context is
// cancelled (ctrl+i). Errors (missing parser, backend not installed,
// conversion failure) come back wrapped with the script's stderr so the model
// can act on them.
func readPDFViaScript(ctx context.Context, root, absPDF string) (string, error) {
	appRoot, err := approot.ExecutableDir()
	if err != nil {
		appRoot = ""
	}
	allowProject := pdfProjectScriptsAllowed()

	script, ok := findPDFScript(appRoot, root, allowProject)
	if !ok {
		if allowProject {
			return "", fmt.Errorf("pdf parser script %s not found next to the executable or in the project", pdfScriptRel)
		}
		return "", fmt.Errorf("pdf parser script %s is not installed next to the GoWork executable; project-local PDF parsers are disabled because they are project-controlled code. Install the parser in the app directory, or set %s=1 to explicitly trust this project's parser", pdfScriptRel, pdfProjectScriptsEnv)
	}

	python := findPDFPython(appRoot, root, allowProject)
	if python == "" {
		return "", fmt.Errorf("pdf parser not installed: initialize the venv in tools/Scripts (pip install pymupdf4llm, or marker-pdf for better accuracy)")
	}

	cmd := exec.CommandContext(ctx, python, script, absPDF)
	stdout := newCappedBuffer(maxPDFStdoutBytes)
	stderr := newCappedBuffer(maxPDFStderrBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("pdf parser failed: %s", msg)
	}
	if stdout.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "pdf parser produced no output"
		}
		return "", fmt.Errorf("pdf parser failed: %s", msg)
	}
	output := stdout.String()
	if stdout.Truncated() {
		output += fmt.Sprintf("\n\n...[pdf output truncated at %d bytes]...\n", maxPDFStdoutBytes)
	}
	return output, nil
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}

	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("read_file: invalid input: %w", err)
	}
	if input.Path == "" {
		return tools.Errf("path is required"), nil
	} else if input.Path == "." {
		return tools.Errf("path must point to a file"), nil
	}

	relPath := filepath.Clean(input.Path)
	if filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return tools.Errf("path must be relative to the project root, got %q", input.Path), nil
	}

	//check if the file exists first; if not, report the path is wrong rather
	//than suggesting the model create a file, so it goes back to exploring
	//NOTE: this goes through args.Root rather than os.Stat directly, so a
	// ".." or symlink escape gets rejected by the sandbox instead of us
	// touching a file outside the project
	info, err := args.Root.Stat(relPath)
	if errors.Is(err, os.ErrNotExist) {
		return tools.Errf("file in path %v does not exist - the path is wrong or the file was never created. Do NOT create it unless the task explicitly asks for a new file: locate it first with list_directory or file_search to find the real path.", input.Path), nil
	}
	if err != nil {
		return tools.Errf("checking %v: %v", input.Path, err), nil
	}
	if info.IsDir() {
		return tools.Errf("path %v is a directory, not a file", input.Path), nil
	}
	if info.Size() == 0 {
		return tools.Ok("(file is empty)"), nil
	}

	// Record what the agent saw so write tools can tell "changed since the
	// model last looked" from "the model wrote it itself".
	if args.ReadState != nil {
		args.ReadState.Record(args.PathKey(relPath), info.ModTime())
	}

	ext := strings.ToLower(filepath.Ext(relPath))

	// pdfs delegate to the python parser and return the full markdown
	if ext == ".pdf" {
		if err := ctx.Err(); err != nil {
			return tools.ToolResult{}, err
		}
		md, err := readPDFViaScript(ctx, *args.RootPath, filepath.Join(*args.RootPath, relPath))
		if err != nil {
			return tools.Errf("%v", err), nil
		}
		return tools.Ok(md), nil
	}

	// images are refused may change in the future.
	if imageExtensions[strings.TrimPrefix(ext, ".")] {
		return tools.Errf("cannot read image files (%s): the model has no way to see images. Do not attempt to read or analyze image files.", ext), nil
	}

	start := input.Start
	if start < 1 {
		start = 1
	}

	offset := input.Offset
	if offset <= 0 {
		offset = DEFAULT_MAX_LINES
	}
	offset = min(offset, DEFAULT_MAX_LINES)

	content, truncated, ok, lastLine, err := readRange(args.Root, args.WatchList, relPath, start, offset)
	if err != nil {
		return tools.Errf("reading %v: %v", input.Path, err), nil
	}
	if !ok {
		return tools.Errf("starting_line %d is past the end of the file (%d lines)", start, lastLine), nil
	}
	if truncated {
		content += fmt.Sprintf("\n...output truncated at %d bytes, raise starting_line to keep paging\n", DEFAULT_MAX_BYTES)
	}

	return tools.Ok(content), nil
}
