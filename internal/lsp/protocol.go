package lsp

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC envelopes ---------------------------------------------------------

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcOutgoingRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcOutgoingNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcOutgoingResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type rpcResponse struct {
	result json.RawMessage
	rpcErr *RPCError
	err    error
}

// RPCError is the error object returned by a JSON-RPC server.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "lsp: unknown RPC error"
	}
	if e.Code == 0 {
		return fmt.Sprintf("lsp: RPC error: %s", e.Message)
	}
	return fmt.Sprintf("lsp: RPC error %d: %s", e.Code, e.Message)
}

// Initialize ----------------------------------------------------------------

// InitializeParams is the subset of LSP initialize parameters GoWork sends.
type InitializeParams struct {
	ProcessID        int                `json:"processId"`
	ClientInfo       *ClientInfo        `json:"clientInfo,omitempty"`
	RootPath         string             `json:"rootPath,omitempty"`
	RootURI          string             `json:"rootUri,omitempty"`
	Capabilities     ClientCapabilities `json:"capabilities"`
	WorkspaceFolders []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// ClientCapabilities advertises only the client features GoWork uses.
type ClientCapabilities struct {
	General      *GeneralClientCapabilities      `json:"general,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

type TextDocumentClientCapabilities struct {
	Synchronization    *TextDocumentSyncClientCapabilities   `json:"synchronization,omitempty"`
	PublishDiagnostics *PublishDiagnosticsClientCapabilities `json:"publishDiagnostics,omitempty"`
	DocumentSymbol     *DocumentSymbolClientCapabilities     `json:"documentSymbol,omitempty"`
	Definition         *DefinitionClientCapabilities         `json:"definition,omitempty"`
}

type TextDocumentSyncClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

type PublishDiagnosticsClientCapabilities struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
	VersionSupport     bool `json:"versionSupport,omitempty"`
}

type DocumentSymbolClientCapabilities struct {
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

type DefinitionClientCapabilities struct {
	LinkSupport bool `json:"linkSupport,omitempty"`
}

// InitializeResult is the subset of the server's initialize response GoWork
// exposes. Capability values are kept raw because LSP allows either a boolean
// or an options object for most of them.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerMetadata    `json:"serverInfo,omitempty"`
}

type ServerCapabilities struct {
	TextDocumentSync       json.RawMessage `json:"textDocumentSync,omitempty"`
	DocumentSymbolProvider json.RawMessage `json:"documentSymbolProvider,omitempty"`
	DefinitionProvider     json.RawMessage `json:"definitionProvider,omitempty"`
}

type ServerMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Text document + positions -------------------------------------------------

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type LocationLink struct {
	OriginSelectionRange *Range `json:"originSelectionRange,omitempty"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange *Range `json:"targetSelectionRange,omitempty"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// Document symbols ----------------------------------------------------------

// DocumentSymbol is a superset of LSP DocumentSymbol and SymbolInformation so
// both server response shapes decode without a second pass.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Tags           []int            `json:"tags,omitempty"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          *Range           `json:"range,omitempty"`
	SelectionRange *Range           `json:"selectionRange,omitempty"`
	Location       *Location        `json:"location,omitempty"`
	ContainerName  string           `json:"containerName,omitempty"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// Diagnostics ---------------------------------------------------------------

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
	Tags     []int           `json:"tags,omitempty"`
}
