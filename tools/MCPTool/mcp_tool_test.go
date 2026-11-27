package mcptool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"GoWork/tools"
)

type fakeRuntime struct {
	opts       tools.MCPInstallOptions
	installRes tools.MCPInstallResult
	installErr error
	servers    []tools.MCPServerInfo
	removed    string
	removeErr  error
}

func (f *fakeRuntime) Install(_ context.Context, opts tools.MCPInstallOptions) (tools.MCPInstallResult, error) {
	f.opts = opts
	return f.installRes, f.installErr
}

func (f *fakeRuntime) List() []tools.MCPServerInfo { return f.servers }

func (f *fakeRuntime) Remove(name string) error {
	f.removed = name
	return f.removeErr
}

func TestToolNamesKindsAndSchemas(t *testing.T) {
	runtime := &fakeRuntime{}
	tests := []struct {
		name string
		kind tools.Kind
	}{
		{InstallToolName, tools.KindNotAllowed},
		{ListToolName, tools.KindAllowed},
		{RemoveToolName, tools.KindNotAllowed},
	}
	all := New(runtime)
	if len(all) != len(tests) {
		t.Fatalf("New returned %d tools", len(all))
	}
	for i, tt := range tests {
		if all[i].Name() != tt.name {
			t.Errorf("tool[%d].Name() = %q, want %q", i, all[i].Name(), tt.name)
		}
		if all[i].Kind() != tt.kind {
			t.Errorf("%s Kind() = %v, want %v", tt.name, all[i].Kind(), tt.kind)
		}
		var schema map[string]any
		if err := json.Unmarshal(all[i].InputSchema(), &schema); err != nil {
			t.Errorf("%s schema is not JSON: %v", tt.name, err)
		}
		if schema["type"] != "object" {
			t.Errorf("%s schema type = %v", tt.name, schema["type"])
		}
	}

	// mcp_install accepts every field named by the bootstrap contract.
	var installSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(NewInstall(runtime).InputSchema(), &installSchema); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "config", "path", "ref", "scope", "servers", "overwrite", "start", "dry_run"} {
		if _, ok := installSchema.Properties[field]; !ok {
			t.Errorf("mcp_install schema missing %q", field)
		}
	}
}

func TestInstallToolPassesOptionsAndReportsCounts(t *testing.T) {
	runtime := &fakeRuntime{installRes: tools.MCPInstallResult{
		ConfigPath: "/tmp/.mcp.json",
		Scope:      tools.MCPScopeUser,
		Servers:    2,
		Tools:      3,
		Installed:  []string{"files"},
		Updated:    []string{"remote"},
		Started:    []string{"files", "remote"},
	}}
	tool := NewInstall(runtime)
	input := json.RawMessage(`{
		"source":"acme/repo",
		"path":"mcp.json",
		"ref":"main",
		"scope":"user",
		"servers":["files","remote"],
		"overwrite":true,
		"start":true,
		"dry_run":false
	}`)
	result, err := tool.Run(context.Background(), tools.DispatchArgs{}, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("Run result = %+v", result)
	}
	if runtime.opts.Source != "acme/repo" || runtime.opts.Path != "mcp.json" || runtime.opts.Ref != "main" ||
		runtime.opts.Scope != "user" || len(runtime.opts.Servers) != 2 || !runtime.opts.Overwrite || !runtime.opts.Start || runtime.opts.DryRun {
		t.Fatalf("options = %+v", runtime.opts)
	}
	for _, want := range []string{"2 server(s)", "Installed: files", "Updated: remote", "Started: 2 server(s), 3 tool(s)"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("result %q missing %q", result.Content, want)
		}
	}
}

func TestInstallToolErrorsAreModelReadable(t *testing.T) {
	runtime := &fakeRuntime{installErr: errors.New("private or loopback address")}
	result, err := NewInstall(runtime).Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{"config":{"mcpServers":{}}}`))
	if err != nil {
		t.Fatalf("Run returned a Go error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "private or loopback address") {
		t.Fatalf("result = %+v", result)
	}

	runtime.installErr = nil
	if _, err := NewInstall(runtime).Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{"config":`)); err != nil {
		t.Fatalf("invalid JSON returned a Go error: %v", err)
	}
}

func TestListToolNeverShowsSecrets(t *testing.T) {
	runtime := &fakeRuntime{servers: []tools.MCPServerInfo{
		{Name: "files", Transport: "stdio", Enabled: true, Connected: true, Tools: 4, Scope: "user", Trusted: true},
		{Name: "remote", Transport: "http", Enabled: false, Tools: 0, Scope: "project", Error: "start failed"},
	}}
	result, err := NewList(runtime).Run(context.Background(), tools.DispatchArgs{}, nil)
	if err != nil || result.IsError {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	for _, want := range []string{"MCP servers (2)", "files: transport=stdio", "tools=4", "remote: transport=http", "status=disabled"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("list result %q missing %q", result.Content, want)
		}
	}
	if strings.Contains(result.Content, "TOKEN") || strings.Contains(result.Content, "Authorization") {
		t.Fatalf("list leaked secret field names/values: %q", result.Content)
	}
}

func TestRemoveTool(t *testing.T) {
	runtime := &fakeRuntime{}
	result, err := NewRemove(runtime).Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{"name":" files "}`))
	if err != nil || result.IsError {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	if runtime.removed != "files" {
		t.Fatalf("removed = %q", runtime.removed)
	}
	missing, err := NewRemove(runtime).Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{}`))
	if err != nil || !missing.IsError {
		t.Fatalf("missing-name result = %+v, %v", missing, err)
	}
}
