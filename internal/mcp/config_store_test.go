package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".mcp.json")
	cfg := &Config{}

	if err := cfg.AddServer(ServerConfig{
		Name:            "local",
		Command:         "echo",
		Args:            []string{"hello"},
		Env:             map[string]string{"MCP_TOKEN": "env-secret"},
		StartupTimeout:  Duration(5 * time.Second),
		ShutdownTimeout: Duration(2 * time.Second),
		Disabled:        true,
	}); err != nil {
		t.Fatalf("AddServer local: %v", err)
	}
	if err := cfg.AddServer(ServerConfig{
		Name:    "remote",
		Type:    "http",
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer header-secret"},
	}); err != nil {
		t.Fatalf("AddServer remote: %v", err)
	}

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("config mode = %o, want 600", got)
		}
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".*tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"disabled": true`) {
		t.Errorf("saved config does not record disabled state:\n%s", raw)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	names := loaded.Names()
	if len(names) != 2 || names[0] != "local" || names[1] != "remote" {
		t.Fatalf("Names() = %v", names)
	}

	local, _ := loaded.Server("local")
	if local.IsEnabled() {
		t.Error("local is enabled, want disabled")
	}
	if local.Command != "echo" || len(local.Args) != 1 || local.Args[0] != "hello" {
		t.Errorf("local process config = %+v", local)
	}
	if local.Env["MCP_TOKEN"] != "env-secret" {
		t.Errorf("local env = %v", local.Env)
	}
	if got := local.StartupTimeout.Duration(); got != 5*time.Second {
		t.Errorf("local StartupTimeout = %s", got)
	}
	if got := local.RequestTimeout.Duration(); got != DefaultRequestTimeout {
		t.Errorf("local RequestTimeout = %s, want default", got)
	}

	remote, _ := loaded.Server("remote")
	if !remote.IsEnabled() {
		t.Error("remote is disabled, want enabled")
	}
	if remote.TransportKind() != TransportHTTP {
		t.Errorf("remote transport = %q", remote.TransportKind())
	}
	if remote.Headers["Authorization"] != "Bearer header-secret" {
		t.Errorf("remote headers = %v", remote.Headers)
	}

	if err := loaded.EnableServer("local"); err != nil {
		t.Fatalf("EnableServer: %v", err)
	}
	if err := loaded.RemoveServer("remote"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	if got := len(loaded.EnabledServers()); got != 1 {
		t.Errorf("EnabledServers() = %d, want 1", got)
	}
	if err := loaded.Save(path); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("second LoadConfig: %v", err)
	}
	if got := reloaded.Names(); len(got) != 1 || got[0] != "local" {
		t.Errorf("reloaded names = %v, want [local]", got)
	}
	local, _ = reloaded.Server("local")
	if !local.IsEnabled() {
		t.Error("local is still disabled after enable + save")
	}
}

func TestConfigEmptyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	cfg := &Config{}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if names := loaded.Names(); len(names) != 0 {
		t.Errorf("Names() = %v, want empty", names)
	}
	if got := loaded.ListServers(); len(got) != 0 {
		t.Errorf("ListServers() = %v, want empty", got)
	}
}

func TestConfigAddRemoveAndEnable(t *testing.T) {
	cfg := &Config{}
	if err := cfg.AddServer(ServerConfig{Name: "zeta", Command: "echo"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddServer(ServerConfig{Name: "alpha", URL: "https://example.com/mcp"}); err != nil {
		t.Fatal(err)
	}

	servers := cfg.ListServers()
	if len(servers) != 2 || servers[0].Name != "alpha" || servers[1].Name != "zeta" {
		t.Fatalf("ListServers() = %+v, want alpha then zeta", servers)
	}
	// Returned structs are copies.
	servers[0].Disabled = true
	alpha, _ := cfg.Server("alpha")
	if !alpha.IsEnabled() {
		t.Error("mutating ListServers result changed the config")
	}

	if err := cfg.DisableServer("alpha"); err != nil {
		t.Fatalf("DisableServer: %v", err)
	}
	alpha, _ = cfg.Server("alpha")
	if alpha.IsEnabled() {
		t.Error("alpha still enabled after DisableServer")
	}
	if err := cfg.EnableServer("alpha"); err != nil {
		t.Fatalf("EnableServer: %v", err)
	}
	alpha, _ = cfg.Server("alpha")
	if !alpha.IsEnabled() {
		t.Error("alpha still disabled after EnableServer")
	}

	if err := cfg.AddServer(ServerConfig{Name: "alpha", Command: "echo"}); err == nil {
		t.Fatal("AddServer accepted a duplicate name")
	}
	if err := cfg.AddServer(ServerConfig{Name: "bad", Command: "echo", URL: "https://example.com"}); err == nil {
		t.Fatal("AddServer accepted command and url together")
	}
	if _, exists := cfg.Server("bad"); exists {
		t.Error("invalid server was inserted")
	}
	if err := cfg.AddServer(ServerConfig{Name: "  ", Command: "echo"}); err == nil {
		t.Fatal("AddServer accepted an empty name")
	}

	if err := cfg.RemoveServer("zeta"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	if err := cfg.RemoveServer("zeta"); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("second RemoveServer error = %v, want ErrServerNotFound", err)
	}
	if err := cfg.SetServerEnabled("missing", true); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("SetServerEnabled error = %v, want ErrServerNotFound", err)
	}
}

func TestConfigSaveValidatesBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	const original = "this file must not be replaced\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Servers: map[string]ServerConfig{
		"broken": {
			Command: "echo",
			URL:     "https://example.com",
			Headers: map[string]string{"Authorization": "Bearer super-secret-token"},
		},
	}}
	err := cfg.Save(path)
	if err == nil {
		t.Fatal("Save accepted an invalid config")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("Save error leaked a secret: %q", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Errorf("invalid save changed the file:\n%s", data)
	}

	if err := (*Config)(nil).Save(path); err == nil {
		t.Fatal("nil Config.Save succeeded")
	}
	if err := (&Config{}).Save(""); err == nil {
		t.Fatal("Save with an empty path succeeded")
	}
}

func TestLoadConfigMalformedFile(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "invalid json",
			data: `{"mcpServers":`,
			want: "parse mcp config",
		},
		{
			name: "wrong servers type",
			data: `{"mcpServers": []}`,
			want: "parse mcp config",
		},
		{
			name: "invalid server",
			data: `{"mcpServers":{"bad":{"command":"echo","url":"https://example.com"}}}`,
			want: "not both",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+".json")
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestServerIsEnabledDefault(t *testing.T) {
	if !(ServerConfig{}).IsEnabled() {
		t.Error("zero ServerConfig should be enabled")
	}
	if (ServerConfig{Disabled: true}).IsEnabled() {
		t.Error("Disabled server reported enabled")
	}
}

func TestParseConfigDoesNotLeakURLCredentials(t *testing.T) {
	const secret = "tokensecret"
	const malformed = "https://user:" + secret + "@example.com:bad port/mcp"

	document, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"bad": map[string]any{"url": malformed},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ParseConfig(document)
	if err == nil {
		t.Fatal("ParseConfig accepted a malformed url")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("ParseConfig error leaked the url password: %q", err)
	}
	if strings.Contains(err.Error(), malformed) {
		t.Errorf("ParseConfig error leaked the raw url: %q", err)
	}
}
