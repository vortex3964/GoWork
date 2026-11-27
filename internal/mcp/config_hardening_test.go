package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseConfigAcceptsServersAliasAndSecurityFields(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"servers": {
			"local": {
				"command": "echo",
				"cwd": "/tmp/work",
				"trusted": true,
				"autoApprove": true
			},
			"remote": {
				"url": "http://127.0.0.1:9000/mcp",
				"allowPrivate": true
			}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	local, ok := cfg.Server("local")
	if !ok {
		t.Fatal("local server missing")
	}
	if local.CWD != "/tmp/work" {
		t.Errorf("CWD = %q", local.CWD)
	}
	if !local.AutoApproved() {
		t.Error("AutoApproved() = false, want true")
	}
	remote, _ := cfg.Server("remote")
	if !remote.AllowPrivate {
		t.Error("AllowPrivate = false")
	}
}

func TestParseConfigRejectsMixedServerKeys(t *testing.T) {
	for _, doc := range []string{
		`{"mcpServers":{},"servers":{}}`,
		`{"mcpServers":{"a":{"command":"echo"}},"servers":{"b":{"command":"echo"}}}`,
	} {
		if _, err := ParseConfig([]byte(doc)); err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("ParseConfig(%s) error = %v, want mixed-key rejection", doc, err)
		}
	}
}

func TestConfigUnmarshalJSONAcceptsAlias(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"servers":{"x":{"command":"echo"}}}`), &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := cfg.Server("x"); !ok {
		t.Fatal("alias server not decoded")
	}
}

func TestParseConfigPrivateURLPolicy(t *testing.T) {
	private := []string{
		"http://127.0.0.1:8080/mcp",
		"http://localhost:8080/mcp",
		"http://[::1]:8080/mcp",
		"http://10.1.2.3/mcp",
		"http://192.168.1.2/mcp",
		"http://169.254.1.1/mcp",
		"http://0.0.0.0/mcp",
	}
	for _, rawURL := range private {
		t.Run(rawURL, func(t *testing.T) {
			doc := fmt.Sprintf(`{"mcpServers":{"x":{"url":%q}}}`, rawURL)
			if _, err := ParseConfig([]byte(doc)); err == nil || !strings.Contains(err.Error(), "allowPrivate") {
				t.Fatalf("ParseConfig error = %v, want private address rejection", err)
			}
			allowed := fmt.Sprintf(`{"mcpServers":{"x":{"url":%q,"allowPrivate":true}}}`, rawURL)
			if _, err := ParseConfig([]byte(allowed)); err != nil {
				t.Fatalf("ParseConfig with allowPrivate: %v", err)
			}
		})
	}
}

func TestParseConfigRejectsURLCredentialsWithoutLeaking(t *testing.T) {
	const secret = "top-secret-token"
	doc := fmt.Sprintf(`{"mcpServers":{"x":{"url":"https://user:%s@example.com/mcp"}}}`, secret)
	_, err := ParseConfig([]byte(doc))
	if err == nil {
		t.Fatal("ParseConfig accepted URL credentials")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), doc) {
		t.Fatalf("error leaked URL credentials: %v", err)
	}
}

func TestConfigServerAndFieldCaps(t *testing.T) {
	if MaxServers < 1 || MaxArgs < 1 || MaxEnvVars < 1 || MaxHeaders < 1 || MaxToolsPerServer < 1 {
		t.Fatal("invalid test constants")
	}

	var servers []string
	for i := 0; i <= MaxServers; i++ {
		servers = append(servers, fmt.Sprintf(`"s%d":{"command":"echo"}`, i))
	}
	if _, err := ParseConfig([]byte(`{"mcpServers":{` + strings.Join(servers, ",") + `}}`)); err == nil || !strings.Contains(err.Error(), "too many servers") {
		t.Fatalf("server cap error = %v", err)
	}

	tooManyArgs := make([]string, MaxArgs+1)
	for i := range tooManyArgs {
		tooManyArgs[i] = `"a"`
	}
	if _, err := ParseConfig([]byte(`{"mcpServers":{"x":{"command":"echo","args":[` + strings.Join(tooManyArgs, ",") + `]}}}`)); err == nil || !strings.Contains(err.Error(), "too many args") {
		t.Fatalf("args cap error = %v", err)
	}

	var env []string
	for i := 0; i <= MaxEnvVars; i++ {
		env = append(env, fmt.Sprintf(`"K%d":"v"`, i))
	}
	if _, err := ParseConfig([]byte(`{"mcpServers":{"x":{"command":"echo","env":{` + strings.Join(env, ",") + `}}}}`)); err == nil || !strings.Contains(err.Error(), "too many environment variables") {
		t.Fatalf("env cap error = %v", err)
	}

	var headers []string
	for i := 0; i <= MaxHeaders; i++ {
		headers = append(headers, fmt.Sprintf(`"X-%d":"v"`, i))
	}
	if _, err := ParseConfig([]byte(`{"mcpServers":{"x":{"url":"https://example.com","headers":{` + strings.Join(headers, ",") + `}}}}`)); err == nil || !strings.Contains(err.Error(), "too many headers") {
		t.Fatalf("headers cap error = %v", err)
	}

	longName := strings.Repeat("n", MaxServerNameBytes+1)
	if _, err := ParseConfig([]byte(fmt.Sprintf(`{"mcpServers":{%q:{"command":"echo"}}}`, longName))); err == nil || !strings.Contains(err.Error(), "name exceeds") {
		t.Fatalf("name cap error = %v", err)
	}
}

func TestConfigAddServerCapsCount(t *testing.T) {
	cfg := &Config{}
	for i := 0; i < MaxServers; i++ {
		if err := cfg.AddServer(ServerConfig{Name: fmt.Sprintf("s%d", i), Command: "echo"}); err != nil {
			t.Fatalf("AddServer %d: %v", i, err)
		}
	}
	if err := cfg.AddServer(ServerConfig{Name: "overflow", Command: "echo"}); err == nil {
		t.Fatal("AddServer accepted more than MaxServers")
	}
}

func TestConfigCWDAndTrustedRoundTrip(t *testing.T) {
	path := t.TempDir() + "/mcp.json"
	cfg := &Config{}
	if err := cfg.AddServer(ServerConfig{
		Name:        "local",
		Command:     "echo",
		CWD:         "/tmp/project",
		Trusted:     true,
		AutoApprove: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	server, _ := loaded.Server("local")
	if server.CWD != "/tmp/project" || !server.Trusted || !server.AutoApprove {
		t.Fatalf("round-trip server = %+v", server)
	}
}

func TestNormalizeToolCaps(t *testing.T) {
	if _, err := normalizeTool(Tool{Name: strings.Repeat("n", MaxToolNameBytes+1)}); err == nil {
		t.Fatal("normalizeTool accepted an oversized name")
	}
	if _, err := normalizeTool(Tool{
		Name:        "x",
		Description: strings.Repeat("d", MaxToolDescriptionBytes+1),
	}); err == nil {
		t.Fatal("normalizeTool accepted an oversized description")
	}
	if _, err := normalizeTool(Tool{
		Name:        "x",
		InputSchema: json.RawMessage(`{"type":"object","description":"` + strings.Repeat("s", MaxToolSchemaBytes) + `"}`),
	}); err == nil {
		t.Fatal("normalizeTool accepted an oversized schema")
	}
}

func TestCWDDefaultsAndDurationStillWork(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"mcpServers":{"x":{"command":"echo","timeout":"3s"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	server, _ := cfg.Server("x")
	if server.CWD != "" {
		t.Errorf("CWD = %q, want empty", server.CWD)
	}
	if got := server.RequestTimeout.Duration(); got != 3*time.Second {
		t.Errorf("RequestTimeout = %s", got)
	}
}
