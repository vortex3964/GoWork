// Package mcptool exposes the model-facing MCP management tools. Installing
// and removing servers changes security-relevant configuration, so both are
// KindNotAllowed and always pass through the permission box. Listing is
// read-only and safe to run without approval.
package mcptool

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"GoWork/internal/netfetch"
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
	return `Installs MCP servers into GoWork's user (default) or project configuration. This tool is self-contained; no separate bootstrap skill is needed.

Provide exactly one of:
- source: "owner/repo", "https://github.com/owner/repo", a GitHub blob/raw URL, a raw https:// URL, or a project-relative path to an MCP JSON file (recognized names include .mcp.json, mcp.json, mcp-config.json, mcp_config.json, mcp-servers.json, and *.mcp.json). When discovery finds several, path selects one and the error lists the sorted candidates. ref applies only to owner/repo or a bare GitHub repository URL and accepts a branch, tag, or full commit SHA (prefer a full SHA).
- config: an inline object such as {"mcpServers":{"files":{"command":"npx","args":["-y","pkg"]}}}; "servers" is also accepted.

Workflow: call with dry_run=true first to validate the source, path, ref, JSON, and server commands/URLs. Then repeat without dry_run and with start=true to save and connect. Installation requires approval and is atomic; if start=true and any server fails to start, the previous config is restored, new processes are stopped, and nothing is left partially installed. Existing server names require overwrite=true. Success reports the connected server and tool count; new tools become available on the next model turn. Project-scope servers never receive automatic tool approval. Never put tokens in source; use environment variables.`
}

func (t *InstallTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": `"owner/repo", "https://github.com/owner/repo", a GitHub blob/raw URL, a raw https:// URL, or a project-relative path to an MCP JSON file. Never include credentials.`,
			},
			"config": map[string]any{
				"type":        "object",
				"description": `Inline MCP config, for example {"mcpServers":{"files":{"command":"npx","args":["-y","pkg"]}}}. Do not combine with source.`,
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Repository-relative config file path when the source has multiple candidates. Use one of the candidate paths from the error.",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Branch, tag, or full commit SHA for an owner/repo or bare GitHub repository URL. Rejected for raw URLs, local paths, and sources that already pin a ref.",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{tools.MCPScopeUser, tools.MCPScopeProject},
				"description": "Where to save the config. Defaults to user. Project-scope tools are never auto-approved.",
			},
			"servers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only install these server names; empty installs every server in the source.",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "Replace existing servers with the same name. Defaults to false; inspect them with mcp_list first.",
			},
			"start": map[string]any{
				"type":        "boolean",
				"description": "Start the changed servers immediately and verify the connection and tool count. A startup failure rolls the install back.",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Validate and report without writing config or starting servers. Call this first, then repeat without dry_run to install.",
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
		return tools.Errf("mcp_install: %s", formatMCPInstallError(err)), nil
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
		b.WriteString("\nnext call: repeat mcp_install without \"dry_run\" and with \"start\":true to save and connect")
	} else if len(result.Started) > 0 {
		fmt.Fprintf(&b, "\nStarted: %d server(s), %d tool(s).", len(result.Started), result.Tools)
		started := append([]string(nil), result.Started...)
		sort.Strings(started)
		for _, name := range started {
			if count, ok := result.ServerTools[name]; ok {
				fmt.Fprintf(&b, "\nVerified: server %q connected, %d tool(s) available.", name, count)
			} else {
				fmt.Fprintf(&b, "\nVerified: server %q connected.", name)
			}
		}
		b.WriteString("\nNew MCP tools are registered now and become available on the next model turn.")
	} else {
		b.WriteString("\nVerification: config saved but no server was started (start=false).")
		b.WriteString("\nnext call: repeat mcp_install with \"start\":true to connect and verify, or call mcp_list to inspect configured servers")
	}
	if len(result.Errors) > 0 {
		fmt.Fprintf(&b, "\nErrors:\n - %s", strings.Join(sanitizeMCPToolStrings(result.Errors), "\n - "))
		b.WriteString("\nnext call: read the sanitized error above, fix the server config, then retry mcp_install with \"start\":true; run mcp_list to inspect configured servers")
	}
	return tools.Ok(b.String()), nil
}

// formatMCPInstallError keeps failures model-readable without credentials and
// appends the concrete next call for the errors a model can recover from.
func formatMCPInstallError(err error) string {
	if err == nil {
		return ""
	}
	message := sanitizeMCPToolText(err.Error())
	if strings.Contains(strings.ToLower(message), "next call:") {
		return message
	}
	return message + "\nnext call: " + mcpInstallNextStep(message)
}

func mcpInstallNextStep(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "already exists"):
		return `run mcp_list to inspect the existing server, then repeat mcp_install with "overwrite":true after approval, or omit those names from "servers"`
	case strings.Contains(lower, "rate limit"):
		return `wait until the reset time shown above, set GITHUB_TOKEN or GH_TOKEN in the environment (never in source), or use a local path or raw URL`
	case strings.Contains(lower, "authentication failed") || strings.Contains(lower, "unauthorized"):
		return `set a valid GITHUB_TOKEN or GH_TOKEN in the environment, or use a public source`
	case strings.Contains(lower, "forbidden"):
		return `check that the token can read the repository, or use a public source or a project-relative path`
	case strings.Contains(lower, "not found") || strings.Contains(lower, "private"):
		return `verify source, path and ref; set GITHUB_TOKEN or GH_TOKEN for private repositories, or pass a project-relative path to the config`
	case strings.Contains(lower, "multiple files") || strings.Contains(lower, "set path to one of"):
		return `repeat mcp_install with "path" set to one exact candidate path from the list, "start":true`
	case strings.Contains(lower, "no mcp config") || strings.Contains(lower, "matches path"):
		return `check the file name and ref, pass "path" as one of the candidate paths, or use inline "config"`
	case strings.Contains(lower, "invalid ref") || strings.Contains(lower, "ref is only") || strings.Contains(lower, "ref was supplied") || strings.Contains(lower, "pins a ref"):
		return `use ref only with owner/repo or a bare GitHub repository URL, without whitespace or "/"; prefer a full commit SHA, and omit it for raw URLs or local paths`
	case strings.Contains(lower, "scope"):
		return `use "scope":"user" (default) or "scope":"project"`
	case strings.Contains(lower, "requires source or config") || strings.Contains(lower, "either source or config"):
		return `pass exactly one of "source" (repo/URL/path) or "config" (inline mcpServers object)`
	case strings.Contains(lower, "invalid mcp config") || strings.Contains(lower, "parse mcp config"):
		return `fix the JSON so it contains one "mcpServers" (or "servers") object, then repeat mcp_install with "dry_run":true`
	case strings.Contains(lower, "rolled back") || strings.Contains(lower, "failed to start") || strings.Contains(lower, "could not start") || strings.Contains(lower, "startup"):
		return `read the error, fix the command/url/environment variable names in config, then retry mcp_install with "start":true; the failed install left no config or process behind`
	default:
		return `run mcp_install with "dry_run":true to validate without writing, or call mcp_list to inspect configured servers`
	}
}

func sanitizeMCPToolStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, sanitizeMCPToolText(value))
	}
	return out
}

var (
	mcpToolUserInfoRE   = regexp.MustCompile(`://[^/\s@]+@`)
	mcpToolQueryValueRE = regexp.MustCompile(`([?&][^=\s&]+=)([^&\s]+)`)
)

func sanitizeMCPToolText(message string) string {
	if token := strings.TrimSpace(netfetch.GitHubTokenFromEnv()); token != "" {
		message = strings.ReplaceAll(message, token, "[redacted]")
	}
	message = mcpToolUserInfoRE.ReplaceAllString(message, "://[redacted]@")
	message = mcpToolQueryValueRE.ReplaceAllString(message, "${1}[redacted]")
	return message
}

// ListTool reports configured servers without exposing secrets.
type ListTool struct {
	runtime Runtime
}

// NewList builds the mcp_list tool.
func NewList(runtime Runtime) tools.AgentTool { return &ListTool{runtime: runtime} }

func (t *ListTool) Name() string { return ListToolName }

func (t *ListTool) Description() string {
	return "Lists configured MCP servers with their transport, enabled/connected state, scope, trust policy, tool counts, and sanitized startup errors. Use it to verify an install/remove or to find the exact server name for mcp_remove. Environment variables and header values are never shown."
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
			fmt.Fprintf(&b, " error=%s", sanitizeMCPToolText(server.Error))
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
	return "Removes a configured MCP server from the scope that currently provides it, saves the config, and stops its process and tools. Requires approval. Use mcp_list first to get the exact server name; its tools disappear immediately."
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
		return tools.Errf("mcp_remove: %s\nnext call: mcp_list to see configured server names, then retry with the exact name", sanitizeMCPToolText(err.Error())), nil
	}
	return tools.Ok(fmt.Sprintf("Removed MCP server %q. It is stopped and its tools are no longer registered.\nnext call: mcp_list to verify the remaining servers", name)), nil
}
