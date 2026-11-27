// Package mcptool exposes the model-facing MCP management tools. Installing
// and removing servers changes security-relevant configuration, so both are
// KindNotAllowed and always pass through the permission box. Listing is
// read-only and safe to run without approval.
package mcptool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"GoWork/tools"
)

const (
	// InstallToolName is the mcp_install tool's wire name.
	InstallToolName = "mcp_install"

	// ListToolName is the mcp_list tool's wire name.
	ListToolName = "mcp_list"

	// RemoveToolName is the mcp_remove tool's wire name.
	RemoveToolName = "mcp_remove"
)

// Runtime is the subset of tools.MCPRuntime these tools need. Tests can use a
// small fake instead of starting real servers.
type Runtime interface {
	Install(ctx context.Context, opts tools.MCPInstallOptions) (tools.MCPInstallResult, error)
	List() []tools.MCPServerInfo
	Remove(name string) error
}

var _ Runtime = (*tools.MCPRuntime)(nil)

// New returns all three MCP management tools.
func New(runtime Runtime) []tools.AgentTool {
	return []tools.AgentTool{
		NewInstall(runtime),
		NewList(runtime),
		NewRemove(runtime),
	}
}

// InstallTool installs an MCP server from a remote source or inline JSON.
type InstallTool struct {
	runtime Runtime
}

// NewInstall builds the mcp_install tool.
func NewInstall(runtime Runtime) tools.AgentTool { return &InstallTool{runtime: runtime} }

func (t *InstallTool) Name() string { return InstallToolName }

func (t *InstallTool) Description() string {
	return `Installs MCP servers into GoWork's user or project configuration.

Provide exactly one of "source" (a GitHub repository/file, raw HTTPS URL, or project-relative local path containing an MCP JSON config) or "config" (an inline MCP config object). The config may use "mcpServers" or "servers". Existing server names are kept unless overwrite is true. The file is written atomically with private permissions; dry_run validates and reports without writing or starting anything. Project-scope servers never receive automatic tool approval.`
}

func (t *InstallTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Remote or project-relative source containing an MCP config file.",
			},
			"config": map[string]any{
				"type":        "object",
				"description": "Inline MCP config, for example {\"mcpServers\":{\"files\":{\"command\":\"npx\",\"args\":[\"-y\",\"pkg\"]}}}.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File path inside the source when it contains more than one candidate.",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Git branch, tag or commit for an owner/repo source.",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{tools.MCPScopeUser, tools.MCPScopeProject},
				"description": "Where to save the config. Defaults to user.",
			},
			"servers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only install these server names; empty installs every server in the source.",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "Replace existing servers with the same name.",
			},
			"start": map[string]any{
				"type":        "boolean",
				"description": "Start the installed servers immediately after saving.",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Validate and report without writing config or starting servers.",
			},
		},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *InstallTool) Kind() tools.Kind { return tools.KindNotAllowed }

func (t *InstallTool) Run(ctx context.Context, _ tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if t == nil || t.runtime == nil {
		return tools.Errf("mcp_install: MCP runtime is not available"), nil
	}
	var input struct {
		Source    string          `json:"source"`
		Config    json.RawMessage `json:"config"`
		Path      string          `json:"path"`
		Ref       string          `json:"ref"`
		Scope     string          `json:"scope"`
		Servers   []string        `json:"servers"`
		Overwrite bool            `json:"overwrite"`
		Start     bool            `json:"start"`
		DryRun    bool            `json:"dry_run"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.Errf("mcp_install: invalid input: %v", err), nil
	}

	result, err := t.runtime.Install(ctx, tools.MCPInstallOptions{
		Source:    strings.TrimSpace(input.Source),
		Config:    input.Config,
		Path:      strings.TrimSpace(input.Path),
		Ref:       strings.TrimSpace(input.Ref),
		Scope:     input.Scope,
		Servers:   input.Servers,
		Overwrite: input.Overwrite,
		Start:     input.Start,
		DryRun:    input.DryRun,
	})
	if err != nil {
		return tools.Errf("mcp_install: %v", err), nil
	}

	var b strings.Builder
	if result.DryRun {
		fmt.Fprintf(&b, "mcp_install dry run: would write %d server(s) to %s (scope %s).", result.Servers, result.ConfigPath, result.Scope)
	} else {
		fmt.Fprintf(&b, "mcp_install: %d server(s) configured in %s (scope %s).", result.Servers, result.ConfigPath, result.Scope)
	}
	if len(result.Installed) > 0 {
		fmt.Fprintf(&b, "\nInstalled: %s", strings.Join(result.Installed, ", "))
	}
	if len(result.Updated) > 0 {
		fmt.Fprintf(&b, "\nUpdated: %s", strings.Join(result.Updated, ", "))
	}
	if result.DryRun {
		b.WriteString("\nNo files were written and no servers were started.")
	} else if result.Started != nil {
		fmt.Fprintf(&b, "\nStarted: %d server(s), %d tool(s).", len(result.Started), result.Tools)
	}
	if len(result.Errors) > 0 {
		fmt.Fprintf(&b, "\nErrors:\n - %s", strings.Join(result.Errors, "\n - "))
	}
	return tools.Ok(b.String()), nil
}

// ListTool reports configured servers without exposing secrets.
type ListTool struct {
	runtime Runtime
}

// NewList builds the mcp_list tool.
func NewList(runtime Runtime) tools.AgentTool { return &ListTool{runtime: runtime} }

func (t *ListTool) Name() string { return ListToolName }

func (t *ListTool) Description() string {
	return "Lists configured MCP servers with their transport, enabled/connected state, scope, trust policy and tool counts. Environment variables and header values are never shown."
}

func (t *ListTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *ListTool) Kind() tools.Kind { return tools.KindAllowed }

func (t *ListTool) Run(ctx context.Context, _ tools.DispatchArgs, _ json.RawMessage) (tools.ToolResult, error) {
	if t == nil || t.runtime == nil {
		return tools.Errf("mcp_list: MCP runtime is not available"), nil
	}
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}
	servers := t.runtime.List()
	if len(servers) == 0 {
		return tools.Ok("No MCP servers configured."), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "MCP servers (%d):", len(servers))
	for _, server := range servers {
		status := "disabled"
		if server.Enabled {
			status = "enabled"
		}
		if server.Connected {
			status += ", connected"
		}
		fmt.Fprintf(&b, "\n- %s: transport=%s status=%s tools=%d scope=%s trusted=%t",
			server.Name, server.Transport, status, server.Tools, server.Scope, server.Trusted)
		if server.Error != "" {
			fmt.Fprintf(&b, " error=%s", server.Error)
		}
	}
	return tools.Ok(b.String()), nil
}

// RemoveTool removes one configured MCP server.
type RemoveTool struct {
	runtime Runtime
}

// NewRemove builds the mcp_remove tool.
func NewRemove(runtime Runtime) tools.AgentTool { return &RemoveTool{runtime: runtime} }

func (t *RemoveTool) Name() string { return RemoveToolName }

func (t *RemoveTool) Description() string {
	return "Removes a configured MCP server from the scope that currently provides it, saving the config and stopping its tools."
}

func (t *RemoveTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Configured server name from mcp_list.",
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *RemoveTool) Kind() tools.Kind { return tools.KindNotAllowed }

func (t *RemoveTool) Run(ctx context.Context, _ tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if t == nil || t.runtime == nil {
		return tools.Errf("mcp_remove: MCP runtime is not available"), nil
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.Errf("mcp_remove: invalid input: %v", err), nil
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return tools.Errf("mcp_remove: missing required field \"name\""), nil
	}
	if err := t.runtime.Remove(name); err != nil {
		return tools.Errf("mcp_remove: %v", err), nil
	}
	return tools.Ok(fmt.Sprintf("Removed MCP server %q.", name)), nil
}
