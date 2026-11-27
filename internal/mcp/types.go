package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol revisions understood by this client, newest first. 2025-11-25 is
// the newest revision that still uses the initialize handshake; later
// revisions are stateless and are intentionally not negotiated here.
const (
	ProtocolVersion20251125 = "2025-11-25"
	ProtocolVersion20250618 = "2025-06-18"
	ProtocolVersion20250326 = "2025-03-26"
	ProtocolVersion20241105 = "2024-11-05"

	// PreferredProtocolVersion is sent in the initialize request. A server may
	// negotiate down to any version in SupportedProtocolVersions.
	PreferredProtocolVersion = ProtocolVersion20251125
)

var supportedProtocolVersions = []string{
	ProtocolVersion20251125,
	ProtocolVersion20250618,
	ProtocolVersion20250326,
	ProtocolVersion20241105,
}

// SupportedProtocolVersions returns a copy of the protocol revisions this
// client can speak.
func SupportedProtocolVersions() []string {
	out := make([]string, len(supportedProtocolVersions))
	copy(out, supportedProtocolVersions)
	return out
}

func isSupportedProtocolVersion(v string) bool {
	for _, supported := range supportedProtocolVersions {
		if v == supported {
			return true
		}
	}
	return false
}

// ClientInfo identifies this implementation during initialization.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities is empty because this client only consumes server
// features; it does not advertise roots, sampling, or elicitation.
type ClientCapabilities struct{}

// InitializeParams is the payload of the initialize request.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
}

// ServerInfo describes the MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// ToolsCapability is the server's tools capability object.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability is the server's prompts capability object.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability is the server's resources capability object.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerCapabilities captures the features a server declared at initialize
// time. Unknown future capabilities are kept in Experimental but ignored.
type ServerCapabilities struct {
	Tools        *ToolsCapability           `json:"tools,omitempty"`
	Prompts      *PromptsCapability         `json:"prompts,omitempty"`
	Resources    *ResourcesCapability       `json:"resources,omitempty"`
	Logging      *struct{}                  `json:"logging,omitempty"`
	Completions  *struct{}                  `json:"completions,omitempty"`
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
}

// InitializeResult is the result of the initialize request.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// Tool is a normalized tool definition. Name, Description, and InputSchema are
// the fields the agent needs; other server metadata is intentionally dropped.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ResourceContents is the embedded resource payload of a content block.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ContentBlock is one entry of a tools/call result. Text is the common case;
type ContentBlock struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	Data        string            `json:"data,omitempty"`
	MIMEType    string            `json:"mimeType,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Resource    *ResourceContents `json:"resource,omitempty"`
}

// CallResult is a tools/call result normalized for an agent loop. Text is the
// readable concatenation of all content blocks, and IsError mirrors the MCP
// tool-execution error flag. StructuredContent is preserved for callers that
// want the original JSON object.
type CallResult struct {
	Content           []ContentBlock  `json:"content"`
	Text              string          `json:"text"`
	IsError           bool            `json:"isError,omitempty"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

// listToolsResult is one page of tools/list.
type listToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// callToolResult is the wire shape of a tools/call result.
type callToolResult struct {
	Content           []ContentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// normalizeContent flattens MCP content blocks into text an LLM can read.
// Binary payloads are represented by a short marker instead of their base64
// body so a large image cannot flood the context window.
func normalizeContent(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		var part string
		switch block.Type {
		case "text":
			part = block.Text
		case "image", "audio":
			if block.MIMEType != "" {
				part = fmt.Sprintf("[%s: %s]", block.Type, block.MIMEType)
			} else {
				part = fmt.Sprintf("[%s]", block.Type)
			}
		case "resource":
			part = normalizeResource(block)
		case "resource_link":
			switch {
			case block.Name != "" && block.URI != "":
				part = fmt.Sprintf("[resource: %s (%s)]", block.Name, block.URI)
			case block.URI != "":
				part = fmt.Sprintf("[resource: %s]", block.URI)
			default:
				part = "[resource]"
			}
		default:
			if block.Type != "" {
				part = fmt.Sprintf("[unsupported content type: %s]", block.Type)
			}
		}
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n")
}

func normalizeResource(block ContentBlock) string {
	if block.Resource == nil {
		if block.URI != "" {
			return fmt.Sprintf("[resource: %s]", block.URI)
		}
		return "[resource]"
	}
	if block.Resource.Text != "" {
		return block.Resource.Text
	}
	label := block.Resource.URI
	if label == "" {
		label = block.Resource.MIMEType
	}
	if label == "" {
		return "[resource]"
	}
	return fmt.Sprintf("[resource: %s]", label)
}
