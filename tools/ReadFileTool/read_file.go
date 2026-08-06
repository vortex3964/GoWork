package readfiletool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"GoWork/tools"
	cl "GoWork/Tui/Components/ChangesList"
)

const DEFAULT_MAX_BYTES int = 50 * 1024
const DEFAULT_MAX_LINES int = 1800

// pdfScriptRel is the pdf parser subprocess, relative to the project root.
const pdfScriptRel = "tools/Scripts/read_pdf.py"

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

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
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
		"required":               []string{"path"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Kind() tools.Kind { return tools.KindRead }

// readRange reads relPath from the sandboxed root into memory, applies any
// pending changes from the WatchList in memory and scans forward through the 
//buffer to return the requested line range.
func readRange(root *os.Root, wl *cl.WatchList, relPath string, start, offset int) (content string, truncated bool, ok bool, lastLine int, err error) {
	//Open the original source file via the sandboxed root
	src, err := root.Open(relPath)
	if err != nil {
		return "", false, false, 0, fmt.Errorf("opening source %s: %w", relPath, err)
	}

	//Read the whole file into memory; src is always closed right after,
	//whether the read succeeded or not
	data, readErr := io.ReadAll(src)
	src.Close()
	if readErr != nil {
		return "", false, false, 0, fmt.Errorf("reading source %s: %w", relPath, readErr)
	}

	//Apply any pending changes in memory. Only bother if there are any -
	//this keeps the common case (no pending edits) a single read with
	//no extra allocation/copy pass over the file.
	if wl != nil {
		if changes := wl.GetChanges(relPath); len(changes) > 0 {
			data = cl.AcceptAllChangesBytes(data, changes)
		}
	}

	//Scan the requested line range from the in-memory buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
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

// findPDFPython returns the venv interpreter for the pdf parser, preferring
// the project's own venv (unix and windows layouts) and falling back to the
// system PATH, or "" if nothing usable exists.
func findPDFPython(root string) string {
	for _, c := range []string{
		filepath.Join(root, "tools", "Scripts", "venv", "bin", "python"),
		filepath.Join(root, "tools", "Scripts", "venv", "Scripts", "python.exe"),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	for _, c := range []string{"python3", "python"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// readPDFViaScript runs the read_pdf.py parser over absPDF and returns its
// stdout verbatim. There is no timeout: a conversion may legitimately take a
// long time (marker's first run downloads its models), so it runs until it
// finishes or the caller's context is cancelled (ctrl+i). Errors (missing
// venv, backend not installed, conversion failure) come back wrapped with
// the script's stderr so the model can act on them.
func readPDFViaScript(ctx context.Context, root, absPDF string) (string, error) {
	python := findPDFPython(root)
	if python == "" {
		return "", fmt.Errorf("pdf parser not installed: initialize the venv in tools/Scripts (pip install pymupdf4llm, or marker-pdf for better accuracy)")
	}

	cmd := exec.CommandContext(ctx, python, filepath.Join(root, pdfScriptRel), absPDF)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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
	return stdout.String(), nil
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

	content, truncated, ok, lastLine, err := readRange(args.Root, args.WatchList ,relPath, start, offset)
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
