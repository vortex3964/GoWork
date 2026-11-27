package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"GoWork/internal/lsp"
)

// LSP bridge tuning. Output caps keep a noisy server from flooding the model's
// context; the diagnostics wait is how long lsp_diagnostics gives a server to
// publish after didOpen.
const (
	lspMaxDiagnostics    = 50
	lspMaxSymbols        = 200
	lspMaxSymbolNodes    = 2000
	lspMaxDefinitions    = 20
	lspMaxOutputBytes    = 12 * 1024
	lspMaxSourceBytes    = 4 << 20
	lspMaxMessageRunes   = 300
	lspDiagnosticsWait   = 500 * time.Millisecond
	lspShutdownTimeout   = 3 * time.Second
	lspQueryPreviewRunes = 60
)

// LSPBridge owns one language-server client per (server, language, root)
// tuple and exposes read-only LSP operations as AgentTools. Call Close or
// Shutdown when the app exits to stop every server process.
type LSPBridge struct {
	cfg *lsp.Config

	mu      sync.Mutex
	clients map[lspClientKey]*lspBridgeClient
	closed  bool
	diagCh  chan struct{}
}

type lspClientKey struct {
	server     string
	languageID string
	root       string
}

type lspBridgeClient struct {
	client     *lsp.Client
	languageID string
	// mu serializes didOpen and requests on this server. The LSP client
	// itself supports concurrent requests, but avoiding a second didOpen for
	// the same document keeps the server's view consistent.
	mu     sync.Mutex
	opened map[string]bool
}

// NewLSPBridge builds a bridge over cfg. A nil config means no servers are
// configured. The returned bridge must be closed when the app exits.
func NewLSPBridge(cfg *lsp.Config) *LSPBridge {
	if cfg == nil {
		cfg = lsp.NewConfig()
	}
	return &LSPBridge{
		cfg:     cfg,
		clients: make(map[lspClientKey]*lspBridgeClient),
		diagCh:  make(chan struct{}, 16),
	}
}

// NewLSPTools returns the three LSP-backed agent tools sharing one bridge.
func NewLSPTools(bridge *LSPBridge) []AgentTool {
	return []AgentTool{
		NewLSPDiagnosticsTool(bridge),
		NewLSPSymbolsTool(bridge),
		NewLSPDefinitionTool(bridge),
	}
}

// NewLSPDiagnosticsTool returns the lsp_diagnostics tool.
func NewLSPDiagnosticsTool(bridge *LSPBridge) AgentTool {
	return &lspDiagnosticsTool{bridge: bridge}
}

// NewLSPSymbolsTool returns the lsp_symbols tool.
func NewLSPSymbolsTool(bridge *LSPBridge) AgentTool {
	return &lspSymbolsTool{bridge: bridge}
}

// NewLSPDefinitionTool returns the lsp_definition tool.
func NewLSPDefinitionTool(bridge *LSPBridge) AgentTool {
	return &lspDefinitionTool{bridge: bridge}
}

// Shutdown gracefully stops every cached language server, bounded by ctx and
// an internal timeout. It is idempotent.
func (b *LSPBridge) Shutdown(ctx context.Context) error {
	if b == nil {
		return nil
	}
	clients := b.takeClients()
	if len(clients) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var errs []error
	for _, entry := range clients {
		sctx, cancel := context.WithTimeout(ctx, lspShutdownTimeout)
		err := entry.client.Shutdown(sctx)
		cancel()
		if err != nil {
			errs = append(errs, err)
			if cerr := entry.client.Close(); cerr != nil {
				errs = append(errs, cerr)
			}
		}
	}
	return errors.Join(errs...)
}

// Close force-stops every cached language server. It is safe after Shutdown
// and is the fast path for app exit.
func (b *LSPBridge) Close() error {
	if b == nil {
		return nil
	}
	clients := b.takeClients()
	var errs []error
	for _, entry := range clients {
		if err := entry.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *LSPBridge) takeClients() []*lspBridgeClient {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if len(b.clients) == 0 {
		return nil
	}
	out := make([]*lspBridgeClient, 0, len(b.clients))
	for _, entry := range b.clients {
		out = append(out, entry)
	}
	b.clients = make(map[lspClientKey]*lspBridgeClient)
	return out
}

// clientForPath resolves the file's language, looks up an enabled server, and
// returns an initialized client plus the file's absolute path.
func (b *LSPBridge) clientForPath(ctx context.Context, args DispatchArgs, relPath string) (*lspBridgeClient, string, error) {
	if b == nil {
		return nil, "", errors.New("LSP bridge is not configured")
	}
	languageID := languageIDForPath(relPath)
	if languageID == "" {
		return nil, "", fmt.Errorf("no language ID mapping for %q; supported extensions include .go, .py, .rs, .ts, .js, .c, .cpp, .java, .lua", relPath)
	}
	server, ok := b.serverFor(languageID)
	if !ok {
		return nil, "", fmt.Errorf("no enabled language server configured for language %q", languageID)
	}

	root := projectRoot(args)
	absPath := filepath.Join(root, relPath)
	if abs, err := filepath.Abs(absPath); err == nil {
		absPath = abs
	}

	key := lspClientKey{server: server.Name, languageID: languageID, root: root}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, "", errors.New("LSP bridge is closed")
	}
	if b.clients == nil {
		b.clients = make(map[lspClientKey]*lspBridgeClient)
	}
	if entry := b.clients[key]; entry != nil {
		select {
		case <-entry.client.Done():
			delete(b.clients, key)
			_ = entry.client.Close()
		default:
			return entry, absPath, nil
		}
	}

	if err := ctxErr(ctx); err != nil {
		return nil, "", err
	}
	client, err := lsp.Start(lsp.Options{
		Command: server.Command,
		Args:    server.Args,
		Env:     server.Env,
		OnDiagnostics: func(lsp.PublishDiagnosticsParams) {
			select {
			case b.diagCh <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("start language server %q: %w", server.Name, err)
	}
	if _, err := client.Initialize(ctx, root); err != nil {
		_ = client.Close()
		return nil, "", fmt.Errorf("initialize language server %q: %w", server.Name, err)
	}

	entry := &lspBridgeClient{
		client:     client,
		languageID: languageID,
		opened:     make(map[string]bool),
	}
	b.clients[key] = entry
	return entry, absPath, nil
}

func (b *LSPBridge) serverFor(languageID string) (lsp.ServerConfig, bool) {
	if b == nil || b.cfg == nil {
		return lsp.ServerConfig{}, false
	}
	for _, server := range b.cfg.List() {
		if !server.Enabled {
			continue
		}
		for _, id := range server.LanguageIDs {
			if strings.EqualFold(id, languageID) {
				return server, true
			}
		}
	}
	return lsp.ServerConfig{}, false
}

func (b *LSPBridge) waitForDiagnostics(ctx context.Context, client *lsp.Client, path string) ([]lsp.Diagnostic, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if diagnostics := client.Diagnostics(path); diagnostics != nil {
		return diagnostics, nil
	}
	timer := time.NewTimer(lspDiagnosticsWait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-b.diagCh:
			if diagnostics := client.Diagnostics(path); diagnostics != nil {
				return diagnostics, nil
			}
		case <-timer.C:
			return client.Diagnostics(path), nil
		}
	}
}

// ensureOpen reads the on-disk file and sends didOpen once per client.
func (e *lspBridgeClient) ensureOpen(ctx context.Context, args DispatchArgs, relPath, absPath string) error {
	if e.opened[absPath] {
		return nil
	}
	data, err := readProjectFile(args, relPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", relPath, err)
	}
	if err := e.client.DidOpen(ctx, absPath, e.languageID, string(data)); err != nil {
		return err
	}
	e.opened[absPath] = true
	return nil
}

func readProjectFile(args DispatchArgs, relPath string) ([]byte, error) {
	var (
		file io.ReadCloser
		err  error
	)
	if args.Root != nil {
		file, err = args.Root.Open(relPath)
	} else {
		file, err = os.Open(filepath.Join(projectRoot(args), relPath))
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, lspMaxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > lspMaxSourceBytes {
		return nil, fmt.Errorf("file exceeds the %d-byte LSP source limit", lspMaxSourceBytes)
	}
	return data, nil
}

func projectRoot(args DispatchArgs) string {
	if args.RootPath != nil && strings.TrimSpace(*args.RootPath) != "" {
		if abs, err := filepath.Abs(*args.RootPath); err == nil {
			return abs
		}
		return *args.RootPath
	}
	return "."
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cleanLSPPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be relative to the project root, got %q", path)
	}
	if clean == "." {
		return "", errors.New("path must point to a file, not the project root")
	}
	return clean, nil
}

// languageIDForPath maps a file extension to the LSP languageId used when
// opening the document.
func languageIDForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" || strings.HasSuffix(base, ".dockerfile") || strings.HasPrefix(base, "dockerfile.") {
		return "dockerfile"
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".rs":
		return "rust"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return "cpp"
	case ".java":
		return "java"
	case ".lua":
		return "lua"
	case ".sh", ".bash":
		return "shellscript"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".jsonc":
		return "jsonc"
	case ".css":
		return "css"
	case ".scss":
		return "scss"
	case ".less":
		return "less"
	case ".html", ".htm":
		return "html"
	case ".rb":
		return "ruby"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".hs":
		return "haskell"
	case ".ml", ".mli":
		return "ocaml"
	case ".ex", ".exs":
		return "elixir"
	case ".zig":
		return "zig"
	case ".tf", ".tfvars":
		return "terraform"
	case ".dart":
		return "dart"
	default:
		return ""
	}
}

// Diagnostics tool -----------------------------------------------------------

type lspDiagnosticsTool struct {
	bridge *LSPBridge
}

func (t *lspDiagnosticsTool) Name() string { return "lsp_diagnostics" }

func (t *lspDiagnosticsTool) Kind() Kind { return KindAllowed }

func (t *lspDiagnosticsTool) Description() string {
	return "Return language-server diagnostics (errors and warnings) for a project file. The file is read from disk and opened in the configured LSP server. Use after edits to catch type, syntax, and import errors."
}

func (t *lspDiagnosticsTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `File to check, relative to the project root (e.g. "src/main.go").`,
			},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *lspDiagnosticsTool) Run(ctx context.Context, args DispatchArgs, rawInput json.RawMessage) (ToolResult, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return Errf("lsp_diagnostics: invalid input: %v", err), nil
	}
	relPath, err := cleanLSPPath(input.Path)
	if err != nil {
		return Errf("lsp_diagnostics: %v", err), nil
	}

	entry, absPath, err := t.bridge.clientForPath(ctx, args, relPath)
	if err != nil {
		return lspToolFailure(ctx, "lsp_diagnostics", err), nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if err := entry.ensureOpen(ctx, args, relPath, absPath); err != nil {
		return lspToolFailure(ctx, "lsp_diagnostics", err), nil
	}
	diagnostics, err := t.bridge.waitForDiagnostics(ctx, entry.client, absPath)
	if err != nil {
		return lspToolFailure(ctx, "lsp_diagnostics", err), nil
	}
	return Ok(formatDiagnostics(relPath, diagnostics)), nil
}

// Symbols tool ---------------------------------------------------------------

type lspSymbolsTool struct {
	bridge *LSPBridge
}

func (t *lspSymbolsTool) Name() string { return "lsp_symbols" }

func (t *lspSymbolsTool) Kind() Kind { return KindAllowed }

func (t *lspSymbolsTool) Description() string {
	return "List symbols (functions, types, methods, fields) in a project file using the configured language server. Use the optional query to filter by name. Line and column numbers in the result are 1-based."
}

func (t *lspSymbolsTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `File to inspect, relative to the project root (e.g. "src/main.go").`,
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Optional case-insensitive name filter.",
			},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *lspSymbolsTool) Run(ctx context.Context, args DispatchArgs, rawInput json.RawMessage) (ToolResult, error) {
	var input struct {
		Path  string `json:"path"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return Errf("lsp_symbols: invalid input: %v", err), nil
	}
	relPath, err := cleanLSPPath(input.Path)
	if err != nil {
		return Errf("lsp_symbols: %v", err), nil
	}

	entry, absPath, err := t.bridge.clientForPath(ctx, args, relPath)
	if err != nil {
		return lspToolFailure(ctx, "lsp_symbols", err), nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if err := entry.ensureOpen(ctx, args, relPath, absPath); err != nil {
		return lspToolFailure(ctx, "lsp_symbols", err), nil
	}
	symbols, err := entry.client.DocumentSymbols(ctx, absPath)
	if err != nil {
		return lspToolFailure(ctx, "lsp_symbols", err), nil
	}
	return Ok(formatSymbols(relPath, symbols, input.Query)), nil
}

// Definition tool ------------------------------------------------------------

type lspDefinitionTool struct {
	bridge *LSPBridge
}

func (t *lspDefinitionTool) Name() string { return "lsp_definition" }

func (t *lspDefinitionTool) Kind() Kind { return KindAllowed }

func (t *lspDefinitionTool) Description() string {
	return "Find where a symbol is defined using the configured language server. Line and column are 1-based, matching editor line numbers. Returns definition locations inside or outside the project."
}

func (t *lspDefinitionTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": `File containing the symbol, relative to the project root.`,
			},
			"line": map[string]any{
				"type":        "integer",
				"description": "1-based line number of the symbol.",
			},
			"character": map[string]any{
				"type":        "integer",
				"description": "1-based column number of the symbol.",
			},
		},
		"required":             []string{"path", "line", "character"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *lspDefinitionTool) Run(ctx context.Context, args DispatchArgs, rawInput json.RawMessage) (ToolResult, error) {
	var input struct {
		Path      string `json:"path"`
		Line      *int   `json:"line"`
		Character *int   `json:"character"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return Errf("lsp_definition: invalid input: %v", err), nil
	}
	relPath, err := cleanLSPPath(input.Path)
	if err != nil {
		return Errf("lsp_definition: %v", err), nil
	}
	if input.Line == nil || input.Character == nil {
		return Errf("lsp_definition: line and character are required"), nil
	}
	if *input.Line < 1 || *input.Character < 1 {
		return Errf("lsp_definition: line and character must be 1-based positive numbers"), nil
	}

	entry, absPath, err := t.bridge.clientForPath(ctx, args, relPath)
	if err != nil {
		return lspToolFailure(ctx, "lsp_definition", err), nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if err := entry.ensureOpen(ctx, args, relPath, absPath); err != nil {
		return lspToolFailure(ctx, "lsp_definition", err), nil
	}
	locations, err := entry.client.Definition(ctx, absPath, *input.Line-1, *input.Character-1)
	if err != nil {
		return lspToolFailure(ctx, "lsp_definition", err), nil
	}
	return Ok(formatDefinitions(relPath, *input.Line, *input.Character, projectRoot(args), locations)), nil
}

func lspToolFailure(ctx context.Context, tool string, err error) ToolResult {
	if ctx != nil && ctx.Err() != nil {
		return Errf("%s: canceled: %v", tool, ctx.Err())
	}
	return Errf("%s: %v", tool, err)
}

// Formatting -----------------------------------------------------------------

func formatDiagnostics(path string, diagnostics []lsp.Diagnostic) string {
	if len(diagnostics) == 0 {
		return fmt.Sprintf("no diagnostics for %s", path)
	}
	limit := len(diagnostics)
	if limit > lspMaxDiagnostics {
		limit = lspMaxDiagnostics
	}

	var b strings.Builder
	fmt.Fprintf(&b, "diagnostics for %s: %d\n", path, len(diagnostics))
	for _, diagnostic := range diagnostics[:limit] {
		position := diagnostic.Range.Start
		message := oneLine(diagnostic.Message, lspMaxMessageRunes)
		fmt.Fprintf(&b, "- %d:%d [%s] %s", position.Line+1, position.Character+1, severityLabel(diagnostic.Severity), message)
		var details []string
		if diagnostic.Source != "" {
			details = append(details, oneLine(diagnostic.Source, 40))
		}
		if code := diagnosticCode(diagnostic.Code); code != "" {
			details = append(details, oneLine(code, 40))
		}
		if len(details) > 0 {
			b.WriteString(" (")
			b.WriteString(strings.Join(details, ", "))
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if len(diagnostics) > limit {
		fmt.Fprintf(&b, "… %d more diagnostics omitted\n", len(diagnostics)-limit)
	}
	return clipOutput(b.String())
}

func severityLabel(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "information"
	case 4:
		return "hint"
	default:
		return "diagnostic"
	}
}

func diagnosticCode(code json.RawMessage) string {
	if len(code) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(code, &text); err == nil {
		return text
	}
	return strings.TrimSpace(string(code))
}

type flatSymbol struct {
	name      string
	kind      int
	line      int
	character int
	depth     int
}

func formatSymbols(path string, symbols []lsp.DocumentSymbol, query string) string {
	flat := make([]flatSymbol, 0, len(symbols))
	flattenSymbols(symbols, 0, &flat)

	query = strings.TrimSpace(query)
	if query != "" {
		filtered := flat[:0]
		lower := strings.ToLower(query)
		for _, symbol := range flat {
			if strings.Contains(strings.ToLower(symbol.name), lower) {
				filtered = append(filtered, symbol)
			}
		}
		flat = filtered
		if len(flat) == 0 {
			return fmt.Sprintf("no symbols matching %q in %s", oneLine(query, lspQueryPreviewRunes), path)
		}
	}
	if len(flat) == 0 {
		return fmt.Sprintf("no symbols in %s", path)
	}

	limit := len(flat)
	if limit > lspMaxSymbols {
		limit = lspMaxSymbols
	}
	var b strings.Builder
	fmt.Fprintf(&b, "symbols for %s: %d\n", path, len(flat))
	for _, symbol := range flat[:limit] {
		indent := strings.Repeat("  ", symbol.depth)
		kind := symbolKindLabel(symbol.kind)
		fmt.Fprintf(&b, "%s- %s (%s) line %d:%d\n", indent, oneLine(symbol.name, 200), kind, symbol.line+1, symbol.character+1)
	}
	if len(flat) > limit {
		fmt.Fprintf(&b, "… %d more symbols omitted\n", len(flat)-limit)
	}
	return clipOutput(b.String())
}

func flattenSymbols(symbols []lsp.DocumentSymbol, depth int, out *[]flatSymbol) {
	for _, symbol := range symbols {
		if len(*out) >= lspMaxSymbolNodes {
			return
		}
		position := firstPosition(symbol)
		*out = append(*out, flatSymbol{
			name:      symbol.Name,
			kind:      symbol.Kind,
			line:      position.Line,
			character: position.Character,
			depth:     depth,
		})
		// Keep pathological trees from producing huge indentation/output.
		if depth < 8 {
			flattenSymbols(symbol.Children, depth+1, out)
		}
	}
}

func firstPosition(symbol lsp.DocumentSymbol) lsp.Position {
	if symbol.SelectionRange != nil {
		return symbol.SelectionRange.Start
	}
	if symbol.Range != nil {
		return symbol.Range.Start
	}
	if symbol.Location != nil {
		return symbol.Location.Range.Start
	}
	return lsp.Position{}
}

var lspSymbolKindNames = map[int]string{
	1:  "file",
	2:  "module",
	3:  "namespace",
	4:  "package",
	5:  "class",
	6:  "method",
	7:  "property",
	8:  "field",
	9:  "constructor",
	10: "enum",
	11: "interface",
	12: "function",
	13: "variable",
	14: "constant",
	15: "string",
	16: "number",
	17: "boolean",
	18: "array",
	19: "object",
	20: "key",
	21: "null",
	22: "enum member",
	23: "struct",
	24: "event",
	25: "operator",
	26: "type parameter",
}

func symbolKindLabel(kind int) string {
	if name, ok := lspSymbolKindNames[kind]; ok {
		return name
	}
	return "symbol"
}

func formatDefinitions(path string, line, character int, root string, locations []lsp.Location) string {
	if len(locations) == 0 {
		return fmt.Sprintf("no definition found for %s at %d:%d", path, line, character)
	}
	limit := len(locations)
	if limit > lspMaxDefinitions {
		limit = lspMaxDefinitions
	}
	var b strings.Builder
	fmt.Fprintf(&b, "definition for %s at %d:%d: %d\n", path, line, character, len(locations))
	for _, location := range locations[:limit] {
		locationPath := displayURIPath(location.URI, root)
		start := location.Range.Start
		fmt.Fprintf(&b, "- %s:%d:%d\n", locationPath, start.Line+1, start.Character+1)
	}
	if len(locations) > limit {
		fmt.Fprintf(&b, "… %d more locations omitted\n", len(locations)-limit)
	}
	return clipOutput(b.String())
}

func displayURIPath(uri, root string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		path = u.Path
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
		path = path[1:]
	}
	if root != "" && path != "" {
		if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

func oneLine(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRunes]) + "…"
}

func clipOutput(text string) string {
	if len(text) <= lspMaxOutputBytes {
		return text
	}
	cut := text[:lspMaxOutputBytes]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n… output truncated\n"
}
