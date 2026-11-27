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
}

var _ AgentTool = (*MCPBridge)(nil)

// NewMCPBridge validates a discovered MCP tool and adapts it to AgentTool.
func NewMCPBridge(client MCPCaller, server string, tool mcp.Tool) (*MCPBridge, error) {
	if client == nil {
		return nil, errors.New("mcp bridge: client is nil")
	}
	server = strings.TrimSpace(server)
	toolName := strings.TrimSpace(tool.Name)
	if toolName == "" {
		return nil, errors.New("mcp bridge: tool name must not be empty")
	}
	schema, err := normalizeMCPBridgeSchema(tool.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("mcp bridge: tool %q: %w", toolName, err)
	}

	description := strings.TrimSpace(tool.Description)
	if description == "" {
		if server != "" {
			description = fmt.Sprintf("MCP tool %q from server %q.", toolName, server)
		} else {
			description = fmt.Sprintf("MCP tool %q.", toolName)
		}
	}

	return &MCPBridge{
		client:      client,
		server:      server,
		toolName:    toolName,
		name:        MCPToolName(server, toolName),
		description: description,
		schema:      schema,
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

// Kind marks MCP tools as callable by the dispatcher. Approval policy belongs
// to the configuration/UI layer, not to the bridge.
func (b *MCPBridge) Kind() Kind { return KindAllowed }

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
// AgentTools, so registration can use the slices together.
func MCPTools(server string, client MCPCaller, definitions []mcp.Tool) ([]AgentTool, []providers.ToolDef, error) {
	agentTools := make([]AgentTool, 0, len(definitions))
	providerDefs := make([]providers.ToolDef, 0, len(definitions))
	usedNames := make(map[string]string, len(definitions))

	for _, definition := range definitions {
		bridge, err := NewMCPBridge(client, server, definition)
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
// outside letters, digits, underscores, and hyphens are replaced, and names
// longer than the common provider limit are shortened with a stable hash.
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
	if len(base) <= mcpToolNameLimit {
		return base
	}

	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	suffix := fmt.Sprintf("_%x", sum[:4])
	prefix := strings.TrimRight(base[:mcpToolNameLimit-len(suffix)], "_-")
	if prefix == "" {
		prefix = "mcp"
	}
	return prefix + suffix
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
