package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	const document = `{
		"mcpServers": {
			"local": {
				"command": "/bin/echo",
				"args": ["hello", "world"],
				"env": {"MCP_TOKEN": "secret"},
				"timeout": "5s",
				"startupTimeout": "2s",
				"shutdownTimeout": "1s"
			},
			"remote": {
				"url": "https://example.com/mcp",
				"headers": {"Authorization": "Bearer test"}
			}
		}
	}`

	cfg, err := ParseConfig([]byte(document))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	names := cfg.Names()
	if len(names) != 2 || names[0] != "local" || names[1] != "remote" {
		t.Fatalf("Names() = %v, want [local remote]", names)
	}

	local, ok := cfg.Server("local")
	if !ok {
		t.Fatal("local server missing")
	}
	if local.Name != "local" {
		t.Errorf("local.Name = %q, want local", local.Name)
	}
	if got := local.TransportKind(); got != TransportStdio {
		t.Errorf("local transport = %q, want stdio", got)
	}
	if local.Command != "/bin/echo" {
		t.Errorf("local.Command = %q", local.Command)
	}
	if len(local.Args) != 2 || local.Args[0] != "hello" || local.Args[1] != "world" {
		t.Errorf("local.Args = %v", local.Args)
	}
	if local.Env["MCP_TOKEN"] != "secret" {
		t.Errorf("local.Env = %v", local.Env)
	}
	if got := local.RequestTimeout.Duration(); got != 5*time.Second {
		t.Errorf("local.RequestTimeout = %s, want 5s", got)
	}
	if got := local.StartupTimeout.Duration(); got != 2*time.Second {
		t.Errorf("local.StartupTimeout = %s, want 2s", got)
	}
	if got := local.ShutdownTimeout.Duration(); got != time.Second {
		t.Errorf("local.ShutdownTimeout = %s, want 1s", got)
	}

	remote, ok := cfg.Server("remote")
	if !ok {
		t.Fatal("remote server missing")
	}
	if got := remote.TransportKind(); got != TransportAuto {
		t.Errorf("remote transport = %q, want auto", got)
	}
	if got := remote.RequestTimeout.Duration(); got != DefaultRequestTimeout {
		t.Errorf("remote.RequestTimeout = %s, want default %s", got, DefaultRequestTimeout)
	}
	if got := remote.StartupTimeout.Duration(); got != DefaultStartupTimeout {
		t.Errorf("remote.StartupTimeout = %s, want default %s", got, DefaultStartupTimeout)
	}
	if got := remote.ShutdownTimeout.Duration(); got != DefaultShutdownTimeout {
		t.Errorf("remote.ShutdownTimeout = %s, want default %s", got, DefaultShutdownTimeout)
	}
	if remote.Headers["Authorization"] != "Bearer test" {
		t.Errorf("remote.Headers = %v", remote.Headers)
	}
}

func TestParseConfigNumericTimeout(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"mcpServers": {
			"local": {"command": "echo", "timeout": 1.5}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	local, _ := cfg.Server("local")
	if got := local.RequestTimeout.Duration(); got != 1500*time.Millisecond {
		t.Errorf("RequestTimeout = %s, want 1.5s", got)
	}
}

func TestParseConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "command and url",
			doc:  `{"mcpServers":{"x":{"command":"echo","url":"https://example.com"}}}`,
			want: "not both",
		},
		{
			name: "neither command nor url",
			doc:  `{"mcpServers":{"x":{}}}`,
			want: "requires command or url",
		},
		{
			name: "unknown type",
			doc:  `{"mcpServers":{"x":{"command":"echo","type":"carrier-pigeon"}}}`,
			want: "unsupported transport type",
		},
		{
			name: "stdio without command",
			doc:  `{"mcpServers":{"x":{"url":"https://example.com","type":"stdio"}}}`,
			want: "stdio server requires command",
		},
		{
			name: "http without url",
			doc:  `{"mcpServers":{"x":{"command":"echo","type":"http"}}}`,
			want: "http server requires url",
		},
		{
			name: "bad url scheme",
			doc:  `{"mcpServers":{"x":{"url":"ftp://example.com/mcp"}}}`,
			want: "invalid url scheme",
		},
		{
			name: "url without host",
			doc:  `{"mcpServers":{"x":{"url":"https:///mcp"}}}`,
			want: "url must include a host",
		},
		{
			name: "bad timeout",
			doc:  `{"mcpServers":{"x":{"command":"echo","timeout":"soon"}}}`,
			want: "invalid duration",
		},
		{
			name: "negative timeout",
			doc:  `{"mcpServers":{"x":{"command":"echo","timeout":"-1s"}}}`,
			want: "must not be negative",
		},
		{
			name: "newline in header",
			doc:  `{"mcpServers":{"x":{"url":"https://example.com","headers":{"X-Bad":"a\nb"}}}}`,
			want: "contains a newline",
		},
		{
			name: "bad env name",
			doc:  `{"mcpServers":{"x":{"command":"echo","env":{"BAD=NAME":"x"}}}}`,
			want: "invalid environment variable name",
		},
		{
			name: "trailing data",
			doc:  `{"mcpServers":{}} {"mcpServers":{}}`,
			want: "unexpected data",
		},
		{
			name: "invalid json",
			doc:  `{"mcpServers":`,
			want: "parse mcp config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.doc))
			if err == nil {
				t.Fatal("ParseConfig succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	data := `{"mcpServers":{"local":{"command":"echo"}}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if _, ok := cfg.Server("local"); !ok {
		t.Fatal("local server missing")
	}

	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("LoadConfig missing file succeeded")
	}
}

func TestServerConfigTransportKindAliases(t *testing.T) {
	tests := []struct {
		typ  string
		want TransportKind
	}{
		{"", TransportAuto},
		{"http", TransportHTTP},
		{"streamable-http", TransportHTTP},
		{"streamable_http", TransportHTTP},
		{"sse", TransportSSE},
		{"http+sse", TransportSSE},
		{"HTTP", TransportHTTP},
	}
	for _, tt := range tests {
		cfg := ServerConfig{URL: "https://example.com", Type: tt.typ}
		if got := cfg.TransportKind(); got != tt.want {
			t.Errorf("TransportKind(%q) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}
