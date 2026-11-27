package lsp

import (
	"errors"
	"strings"
	"testing"
)

func TestConfigAddListRemove(t *testing.T) {
	cfg := NewConfig()
	server := ServerConfig{
		Name:        "  custom-ls  ",
		Command:     "  custom-ls  ",
		Args:        []string{"--stdio"},
		Env:         map[string]string{"CUSTOM_MODE": "1"},
		LanguageIDs: []string{" go ", "Go", "golang", ""},
		Enabled:     true,
	}
	if err := cfg.Add(server); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := cfg.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1", got)
	}

	got, ok := cfg.Get("CUSTOM-LS")
	if !ok {
		t.Fatal("Get did not match the name case-insensitively")
	}
	if got.Name != "custom-ls" || got.Command != "custom-ls" {
		t.Fatalf("server was not normalized: %+v", got)
	}
	if len(got.LanguageIDs) != 2 || got.LanguageIDs[0] != "go" || got.LanguageIDs[1] != "golang" {
		t.Fatalf("language IDs = %v, want [go golang]", got.LanguageIDs)
	}
	if !got.Enabled {
		t.Fatal("Enabled flag was lost")
	}

	// List must be a defensive copy: mutating it must not change the config.
	list := cfg.List()
	list[0].Name = "mutated"
	list[0].Args[0] = "mutated"
	list[0].Env["CUSTOM_MODE"] = "mutated"
	list[0].LanguageIDs[0] = "mutated"
	again := cfg.List()
	if again[0].Name != "custom-ls" || again[0].Args[0] != "--stdio" {
		t.Fatalf("List exposed internal state: %+v", again[0])
	}
	if again[0].Env["CUSTOM_MODE"] != "1" || again[0].LanguageIDs[0] != "go" {
		t.Fatalf("List exposed nested internal state: %+v", again[0])
	}

	if err := cfg.SetEnabled("Custom-LS", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, _ = cfg.Get("custom-ls")
	if got.Enabled {
		t.Fatal("SetEnabled did not persist")
	}

	if err := cfg.Remove("CUSTOM-LS"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if cfg.Len() != 0 {
		t.Fatalf("Len after Remove = %d, want 0", cfg.Len())
	}
	if err := cfg.Remove("custom-ls"); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("Remove missing = %v, want ErrServerNotFound", err)
	}
	if err := cfg.SetEnabled("custom-ls", true); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("SetEnabled missing = %v, want ErrServerNotFound", err)
	}
}

func TestConfigRejectsDuplicateNames(t *testing.T) {
	cfg := NewConfig()
	first := ServerConfig{Name: "my-ls", Command: "my-ls", LanguageIDs: []string{"go"}}
	if err := cfg.Add(first); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	duplicate := ServerConfig{Name: "MY-LS", Command: "other", LanguageIDs: []string{"rust"}}
	if err := cfg.Add(duplicate); !errors.Is(err, ErrDuplicateServer) {
		t.Fatalf("duplicate Add = %v, want ErrDuplicateServer", err)
	}
	if cfg.Len() != 1 {
		t.Fatalf("Len = %d, want 1", cfg.Len())
	}
}

func TestServerConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		server  ServerConfig
		wantErr string
	}{
		{
			name:   "valid",
			server: ServerConfig{Name: "ls", Command: "ls", LanguageIDs: []string{"go"}},
		},
		{
			name:    "missing name",
			server:  ServerConfig{Command: "ls", LanguageIDs: []string{"go"}},
			wantErr: "name is required",
		},
		{
			name:    "blank name",
			server:  ServerConfig{Name: "  ", Command: "ls", LanguageIDs: []string{"go"}},
			wantErr: "name is required",
		},
		{
			name:    "missing command",
			server:  ServerConfig{Name: "ls", LanguageIDs: []string{"go"}},
			wantErr: "command is required",
		},
		{
			name:    "no language IDs",
			server:  ServerConfig{Name: "ls", Command: "ls"},
			wantErr: "at least one language ID",
		},
		{
			name:    "blank language IDs",
			server:  ServerConfig{Name: "ls", Command: "ls", LanguageIDs: []string{" ", ""}},
			wantErr: "at least one language ID",
		},
		{
			name: "empty env key",
			server: ServerConfig{
				Name: "ls", Command: "ls", LanguageIDs: []string{"go"},
				Env: map[string]string{" ": "value"},
			},
			wantErr: "empty environment variable name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.server.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseConfigDocument(t *testing.T) {
	data := []byte(`{
		"servers": [
			{
				"name": "typescript-ls",
				"command": "typescript-language-server",
				"args": ["--stdio"],
				"env": {"NODE_OPTIONS": "--max-old-space-size=2048"},
				"language_ids": ["typescript", "javascript"],
				"enabled": true
			},
			{
				"name": "gopls",
				"command": "gopls",
				"language_ids": ["go"],
				"enabled": false
			}
		]
	}`)
	cfg, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Len() != 2 {
		t.Fatalf("Len = %d, want 2", cfg.Len())
	}
	ts, ok := cfg.Get("typescript-ls")
	if !ok {
		t.Fatal("typescript-ls missing")
	}
	if ts.Command != "typescript-language-server" || len(ts.Args) != 1 || ts.Args[0] != "--stdio" {
		t.Fatalf("typescript-ls = %+v", ts)
	}
	if ts.Env["NODE_OPTIONS"] != "--max-old-space-size=2048" || !ts.Enabled {
		t.Fatalf("typescript-ls env/enabled = %+v", ts)
	}
	if len(ts.LanguageIDs) != 2 || ts.LanguageIDs[1] != "javascript" {
		t.Fatalf("typescript-ls languages = %v", ts.LanguageIDs)
	}
	gopls, _ := cfg.Get("gopls")
	if gopls.Enabled {
		t.Fatal("gopls should be disabled")
	}
}

func TestParseConfigBareArray(t *testing.T) {
	data := []byte(`[{"name":"ls","command":"ls","language_ids":["go"],"enabled":true}]`)
	cfg, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Len() != 1 {
		t.Fatalf("Len = %d, want 1", cfg.Len())
	}
}

func TestParseConfigReader(t *testing.T) {
	cfg, err := ParseConfigReader(strings.NewReader(`{"servers":[{"name":"ls","command":"ls","language_ids":["go"]}]}`))
	if err != nil {
		t.Fatalf("ParseConfigReader: %v", err)
	}
	if cfg.Len() != 1 {
		t.Fatalf("Len = %d, want 1", cfg.Len())
	}
	if _, err := ParseConfigReader(nil); err == nil {
		t.Fatal("nil reader should error")
	}
}

func TestParseConfigRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"empty", "  \n\t "},
		{"invalid JSON", `{"servers":[`},
		{"unknown field", `{"servers":[{"name":"ls","command":"ls","language_ids":["go"],"typo":true}]}`},
		{"trailing data", `{"servers":[]} {"servers":[]}`},
		{"duplicate names", `{"servers":[
			{"name":"ls","command":"ls","language_ids":["go"]},
			{"name":"LS","command":"other","language_ids":["rust"]}
		]}`},
		{"missing command", `{"servers":[{"name":"ls","language_ids":["go"]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tc.data)); err == nil {
				t.Fatal("ParseConfig accepted invalid input")
			}
		})
	}
}
