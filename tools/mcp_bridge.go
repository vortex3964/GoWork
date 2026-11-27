package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"GoWork/internal/mcp"
	"GoWork/providers"
)

const mcpToolNameLimit = 64

// MCPCaller is the part of mcp.Client used by a bridge. Keeping it an
// interface makes the bridge straightforward to test without a live server.
type MCPCaller interface {
	CallTool(ctx context.Context, name string, arguments any) (*mcp.CallResult, error)
}

var _ MCPCaller = (*mcp.Client)(nil)

// MCPBridgeOptions controls the approval policy attached to a bridge. The zero
// value is deliberately KindNotAllowed: a server discovered from a downloaded
// or project-scope config must not be able to grant itself permission.
type MCPBridgeOptions struct {
	Kind Kind
}

// MCPToolOption customizes MCPTools.
type MCPToolOption func(*MCPBridgeOptions)

// WithMCPToolKind sets the approval kind for every bridge produced by
// MCPTools. Runtimes pass KindAllowed only for trusted user-scope servers.
func WithMCPToolKind(kind Kind) MCPToolOption {
	return func(opts *MCPBridgeOptions) { opts.Kind = kind }
}

// MCPBridge exposes one MCP tool as a regular AgentTool. The model-facing name
// is namespaced by server so tools from different servers do not collide; the
// original MCP name is preserved for the actual tools/call request.
type MCPBridge struct {
	client      MCPCaller
	server      string
	toolName    string
	name        string
	description string
	schema      json.RawMessage
	kind        Kind
}

var _ AgentTool = (*MCPBridge)(nil)

// NewMCPBridge validates a discovered MCP tool and adapts it to AgentTool. The
// bridge defaults to KindNotAllowed; use NewMCPBridgeWithOptions or
// NewMCPBridgeWithKind for a trusted server.
func NewMCPBridge(client MCPCaller, server string, tool mcp.Tool) (*MCPBridge, error) {
	return NewMCPBridgeWithOptions(client, server, tool, MCPBridgeOptions{})
}

// NewMCPBridgeWithKind is shorthand for NewMCPBridgeWithOptions with a kind.
func NewMCPBridgeWithKind(client MCPCaller, server string, tool mcp.Tool, kind Kind) (*MCPBridge, error) {
	return NewMCPBridgeWithOptions(client, server, tool, MCPBridgeOptions{Kind: kind})
}

// NewMCPBridgeWithOptions validates a discovered MCP tool and applies the
// supplied approval policy.
func NewMCPBridgeWithOptions(client MCPCaller, server string, tool mcp.Tool, opts MCPBridgeOptions) (*MCPBridge, error) {
	if client == nil {
		return nil, errors.New("mcp bridge: client is nil")
	}
	server = strings.TrimSpace(server)
	toolName := strings.TrimSpace(tool.Name)
	if toolName == "" {
		return nil, errors.New("mcp bridge: tool name must not be empty")
	}
	if len(toolName) > mcp.MaxToolNameBytes {
		return nil, fmt.Errorf("mcp bridge: tool name exceeds %d bytes", mcp.MaxToolNameBytes)
	}
	description := strings.TrimSpace(tool.Description)
	if len(description) > mcp.MaxToolDescriptionBytes {
		return nil, fmt.Errorf("mcp bridge: tool %q description exceeds %d bytes", toolName, mcp.MaxToolDescriptionBytes)
	}
	if len(tool.InputSchema) > mcp.MaxToolSchemaBytes {
		return nil, fmt.Errorf("mcp bridge: tool %q input schema exceeds %d bytes", toolName, mcp.MaxToolSchemaBytes)
	}
	schema, err := normalizeMCPBridgeSchema(tool.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("mcp bridge: tool %q: %w", toolName, err)
	}

	if description == "" {
		if server != "" {
			description = fmt.Sprintf("MCP tool %q from server %q.", toolName, server)
		} else {
			description = fmt.Sprintf("MCP tool %q.", toolName)
		}
	}

	kind := opts.Kind
	if kind != KindAllowed {
		kind = KindNotAllowed
	}

	return &MCPBridge{
		client:      client,
		server:      server,
		toolName:    toolName,
		name:        MCPToolName(server, toolName),
		description: description,
		schema:      schema,
		kind:        kind,
	}, nil
}

// Name returns the namespaced, provider-safe tool name.
func (b *MCPBridge) Name() string { return b.name }

// Description returns the server-supplied description or a small fallback.
func (b *MCPBridge) Description() string { return b.description }

// InputSchema returns a copy of the MCP tool's input schema.
func (b *MCPBridge) InputSchema() json.RawMessage {
	return append(json.RawMessage(nil), b.schema...)
}

// Kind reports the approval policy fixed when the bridge was created. MCP
// tools require per-call approval unless their server explicitly opted in as a
// trusted user-scope configuration.
func (b *MCPBridge) Kind() Kind { return b.kind }

// Server returns the configured server name.
func (b *MCPBridge) Server() string { return b.server }

// MCPName returns the original tool name sent in tools/call.
func (b *MCPBridge) MCPName() string { return b.toolName }

// Run invokes the MCP tool and maps its result to ToolResult. Tool execution
// failures set IsError; transport/protocol failures and cancellation return a
// Go error so the agent loop can stop or retry.
func (b *MCPBridge) Run(ctx context.Context, _ DispatchArgs, input json.RawMessage) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, fmt.Errorf("mcp tool %q canceled: %w", b.name, err)
	}

	arguments, err := parseMCPArguments(input)
	if err != nil {
		return Errf("mcp tool %q: %v", b.name, err), nil
	}

	result, err := b.client.CallTool(ctx, b.toolName, arguments)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ToolResult{}, fmt.Errorf("mcp tool %q canceled: %w", b.name, ctxErr)
		}
		return ToolResult{}, fmt.Errorf("mcp tool %q: %w", b.name, err)
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, fmt.Errorf("mcp tool %q canceled: %w", b.name, err)
	}
	if result == nil {
		return ToolResult{}, fmt.Errorf("mcp tool %q returned no result", b.name)
	}

	text := result.Text
	if text == "" {
		text = mcp.TextFromContentBlocks(result.Content)
	}
	if text == "" {
		structured := bytes.TrimSpace(result.StructuredContent)
		if len(structured) > 0 && !bytes.Equal(structured, []byte("null")) {
			text = string(structured)
		}
	}

	return ToolResult{Content: text, IsError: result.IsError}, nil
}

// MCPTools converts one server's discovered tools into dispatcher tools and
// provider definitions. The provider definitions use the same names as the
// AgentTools, so registration can use the slices together. Bridges default to
// KindNotAllowed; pass WithMCPToolKind(KindAllowed) only for a trusted
// user-scope server.
func MCPTools(server string, client MCPCaller, definitions []mcp.Tool, options ...MCPToolOption) ([]AgentTool, []providers.ToolDef, error) {
	if len(definitions) > mcp.MaxToolsPerServer {
		return nil, nil, fmt.Errorf("mcp server %q listed more than %d tools", server, mcp.MaxToolsPerServer)
	}
	opts := MCPBridgeOptions{}
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	agentTools := make([]AgentTool, 0, len(definitions))
	providerDefs := make([]providers.ToolDef, 0, len(definitions))
	usedNames := make(map[string]string, len(definitions))

	for _, definition := range definitions {
		bridge, err := NewMCPBridgeWithOptions(client, server, definition, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("mcp server %q: %w", server, err)
		}
		if previous, exists := usedNames[bridge.Name()]; exists {
			return nil, nil, fmt.Errorf("mcp server %q: tools %q and %q both map to %q", server, previous, definition.Name, bridge.Name())
		}
		usedNames[bridge.Name()] = definition.Name

		agentTools = append(agentTools, bridge)
		providerDefs = append(providerDefs, providers.ToolDef{
			Name:        bridge.Name(),
			Description: bridge.Description(),
			InputSchema: bridge.InputSchema(),
		})
	}

	return agentTools, providerDefs, nil
}

// MCPToolName builds a provider-safe, server-namespaced tool name. Characters
// outside letters, digits, underscores, and hyphens are replaced. A short hash
// of the raw server and tool names is always appended so distinct names that
// sanitize to the same base remain unambiguous, and long names are shortened
// to the common provider limit.
func MCPToolName(server, tool string) string {
	serverPart := sanitizeMCPNamePart(server)
	toolPart := sanitizeMCPNamePart(tool)

	var base string
	switch {
	case serverPart != "" && toolPart != "":
		base = "mcp_" + serverPart + "_" + toolPart
	case toolPart != "":
		base = "mcp_" + toolPart
	case serverPart != "":
		base = "mcp_" + serverPart
	default:
		base = "mcp_tool"
	}

	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	suffix := fmt.Sprintf("_%x", sum[:4])
	if maxBase := mcpToolNameLimit - len(suffix); len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "_-")
		if base == "" {
			base = "mcp"
		}
	}
	return base + suffix
}

func sanitizeMCPNamePart(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	lastUnderscore := false
	for _, r := range strings.TrimSpace(value) {
		allowed := r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '_' || r == '-'
		switch {
		case allowed:
			out.WriteRune(r)
			lastUnderscore = r == '_'
		case !lastUnderscore:
			out.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(out.String(), "_-")
}

func normalizeMCPBridgeSchema(schema json.RawMessage) (json.RawMessage, error) {
	schema = bytes.TrimSpace(schema)
	if len(schema) == 0 || bytes.Equal(schema, []byte("null")) {
		schema = []byte(`{"type":"object"}`)
	}
	if !json.Valid(schema) {
		return nil, errors.New("input schema is not valid JSON")
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(schema, &object); err != nil || object == nil {
		return nil, errors.New("input schema must be a JSON object")
	}

	rawType, hasType := object["type"]
	if !hasType {
		object["type"] = json.RawMessage(`"object"`)
		normalized, err := json.Marshal(object)
		if err != nil {
			return nil, errors.New("could not normalize input schema")
		}
		schema = normalized
	} else {
		var typ string
		if err := json.Unmarshal(rawType, &typ); err != nil || typ != "object" {
			return nil, errors.New(`input schema type must be "object"`)
		}
	}
	return append(json.RawMessage(nil), schema...), nil
}

func parseMCPArguments(input json.RawMessage) (any, error) {
	input = bytes.TrimSpace(input)
	if len(input) == 0 || bytes.Equal(input, []byte("null")) {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	return json.RawMessage(append([]byte(nil), input...)), nil
}
