package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultConfigPath is the conventional project-level MCP configuration file.
// Callers that manage their own location pass an explicit path to LoadConfig
// and Config.Save instead.
const DefaultConfigPath = ".mcp.json"

// ErrServerNotFound reports a lookup or mutation of an unknown server name.
var ErrServerNotFound = errors.New("mcp: server not found")

// Default timeouts for a server that does not configure its own.
const (
	DefaultStartupTimeout  = 20 * time.Second
	DefaultRequestTimeout  = 60 * time.Second
	DefaultShutdownTimeout = 5 * time.Second
)

// TransportKind selects how a client reaches its server.
type TransportKind string

const (
	// TransportAuto uses stdio when Command is set, and tries Streamable HTTP
	// with an HTTP+SSE fallback when URL is set.
	TransportAuto  TransportKind = "auto"
	TransportStdio TransportKind = "stdio"
	TransportHTTP  TransportKind = "http"
	TransportSSE   TransportKind = "sse"
)

// Config is the on-disk MCP configuration. The accepted shape follows the
// widely used "mcpServers" object:
//
//	{"mcpServers": {"files": {"command": "npx", "args": ["-y", "pkg"]}}}
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// ServerConfig describes one MCP server. Command/Args/Env configure a stdio
// server; URL/Headers configure a remote server. Type may be empty, in which
// case it is inferred from which of Command and URL is set.
type ServerConfig struct {
	Name     string            `json:"-"`
	Type     string            `json:"type,omitempty"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`

	// Timeout is an alias for RequestTimeout. It exists because several MCP
	// clients use "timeout" for per-request deadlines.
	Timeout         Duration `json:"timeout,omitempty"`
	StartupTimeout  Duration `json:"startupTimeout,omitempty"`
	RequestTimeout  Duration `json:"requestTimeout,omitempty"`
	ShutdownTimeout Duration `json:"shutdownTimeout,omitempty"`
}

// Duration is a time.Duration that marshals as a duration string ("30s") and
// accepts either a string or a number of seconds when unmarshalling.
type Duration time.Duration

// Duration returns the value as a standard time.Duration.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*d = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(parsed)
		return nil
	}

	var seconds float64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return fmt.Errorf("duration must be a string like \"30s\" or a number of seconds: %w", err)
	}
	*d = Duration(time.Duration(seconds * float64(time.Second)))
	return nil
}

// ParseConfig parses an MCP configuration document. Server entries are
// validated and have default timeouts applied before they are returned.
func ParseConfig(data []byte) (*Config, error) {
	var file struct {
		Servers map[string]ServerConfig `json:"mcpServers"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			err = errors.New("unexpected data after top-level object")
		}
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}

	cfg := &Config{Servers: make(map[string]ServerConfig, len(file.Servers))}
	names := make([]string, 0, len(file.Servers))
	for name := range file.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, errors.New("mcp config: server name must not be empty"))
			continue
		}
		server := file.Servers[name]
		server.Name = name
		normalized, err := normalizeServerConfig(server)
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp config: server %q: %w", name, err))
			continue
		}
		cfg.Servers[name] = normalized
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// LoadConfig reads and parses an MCP configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp config %s: %w", path, err)
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// SaveConfig atomically writes cfg to path. It is equivalent to cfg.Save.
func SaveConfig(path string, cfg *Config) error {
	return cfg.Save(path)
}

// Server returns the named server configuration.
func (c *Config) Server(name string) (ServerConfig, bool) {
	if c == nil {
		return ServerConfig{}, false
	}
	server, ok := c.Servers[name]
	return server, ok
}

// Names returns the configured server names in sorted order.
func (c *Config) Names() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListServers returns every configured server in name order. The returned
// values are copies; their maps still refer to the underlying configuration.
func (c *Config) ListServers() []ServerConfig {
	names := c.Names()
	servers := make([]ServerConfig, 0, len(names))
	for _, name := range names {
		server := c.Servers[name]
		if server.Name == "" {
			server.Name = name
		}
		servers = append(servers, server)
	}
	return servers
}

// EnabledServers returns the enabled servers in name order.
func (c *Config) EnabledServers() []ServerConfig {
	all := c.ListServers()
	enabled := make([]ServerConfig, 0, len(all))
	for _, server := range all {
		if server.IsEnabled() {
			enabled = append(enabled, server)
		}
	}
	return enabled
}

// AddServer validates and inserts a server. It fails if the name is empty or
// already exists.
func (c *Config) AddServer(server ServerConfig) error {
	if c == nil {
		return errors.New("mcp: config is nil")
	}
	if strings.TrimSpace(server.Name) == "" {
		return errors.New("mcp: server name must not be empty")
	}
	normalized, err := normalizeServerConfig(server)
	if err != nil {
		return fmt.Errorf("mcp: server %q: %w", server.Name, err)
	}
	if c.Servers == nil {
		c.Servers = make(map[string]ServerConfig)
	}
	if _, exists := c.Servers[normalized.Name]; exists {
		return fmt.Errorf("mcp: server %q already exists", normalized.Name)
	}
	c.Servers[normalized.Name] = normalized
	return nil
}

// RemoveServer deletes a server by name.
func (c *Config) RemoveServer(name string) error {
	if c == nil {
		return ErrServerNotFound
	}
	if _, ok := c.Servers[name]; !ok {
		return fmt.Errorf("%w: %q", ErrServerNotFound, name)
	}
	delete(c.Servers, name)
	return nil
}

// SetServerEnabled enables or disables a server without removing its config.
func (c *Config) SetServerEnabled(name string, enabled bool) error {
	if c == nil {
		return fmt.Errorf("%w: %q", ErrServerNotFound, name)
	}
	server, ok := c.Servers[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrServerNotFound, name)
	}
	server.Disabled = !enabled
	c.Servers[name] = server
	return nil
}

// EnableServer re-enables a configured server.
func (c *Config) EnableServer(name string) error { return c.SetServerEnabled(name, true) }

// DisableServer keeps a configured server but stops it from being started.
func (c *Config) DisableServer(name string) error { return c.SetServerEnabled(name, false) }

// Save atomically writes the configuration to path. The file is validated
// before anything is written, created with mode 0600, and replaced via a
// same-directory temporary file and rename. Error messages never include
// environment values or header values.
func (c *Config) Save(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("mcp: config path must not be empty")
	}
	prepared, err := c.preparedCopy()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(prepared, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: encode config: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcp: create config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("mcp: create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("mcp: set config permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("mcp: write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("mcp: sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcp: close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("mcp: replace config %s: %w", path, err)
	}
	keepTemp = false
	return nil
}

func (c *Config) preparedCopy() (*Config, error) {
	if c == nil {
		return nil, errors.New("mcp: config is nil")
	}
	out := &Config{Servers: make(map[string]ServerConfig, len(c.Servers))}
	for _, name := range c.Names() {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("mcp: server name must not be empty")
		}
		server := c.Servers[name]
		server.Name = name
		normalized, err := normalizeServerConfig(server)
		if err != nil {
			return nil, fmt.Errorf("mcp: server %q: %w", name, err)
		}
		out.Servers[name] = normalized
	}
	return out, nil
}

// IsEnabled reports whether the server should be started. Servers are enabled
// unless explicitly disabled.
func (s ServerConfig) IsEnabled() bool { return !s.Disabled }

// TransportKind reports the effective transport after applying the Type
// override and inferring from Command/URL.
func (s ServerConfig) TransportKind() TransportKind {
	kind := TransportKind(strings.ToLower(strings.TrimSpace(s.Type)))
	switch kind {
	case "":
		switch {
		case strings.TrimSpace(s.Command) != "":
			return TransportStdio
		case strings.TrimSpace(s.URL) != "":
			return TransportAuto
		default:
			return ""
		}
	case TransportAuto, TransportStdio, TransportHTTP, TransportSSE:
		return kind
	case "streamable-http", "streamable_http":
		return TransportHTTP
	case "http+sse", "http-sse":
		return TransportSSE
	default:
		return kind
	}
}

func normalizeServerConfig(server ServerConfig) (ServerConfig, error) {
	server.Name = strings.TrimSpace(server.Name)
	server.Type = strings.ToLower(strings.TrimSpace(server.Type))
	server.Command = strings.TrimSpace(server.Command)
	server.URL = strings.TrimSpace(server.URL)

	if server.RequestTimeout == 0 {
		server.RequestTimeout = server.Timeout
	}
	server.Timeout = 0
	if server.StartupTimeout == 0 {
		server.StartupTimeout = Duration(DefaultStartupTimeout)
	}
	if server.RequestTimeout == 0 {
		server.RequestTimeout = Duration(DefaultRequestTimeout)
	}
	if server.ShutdownTimeout == 0 {
		server.ShutdownTimeout = Duration(DefaultShutdownTimeout)
	}

	if err := server.validate(); err != nil {
		return ServerConfig{}, err
	}
	return server, nil
}

func (s ServerConfig) validate() error {
	if s.Name == "" {
		return errors.New("name must not be empty")
	}
	if strings.ContainsRune(s.Command, 0) {
		return errors.New("command contains a null byte")
	}
	if s.Command != "" && s.URL != "" {
		return errors.New("set either command (stdio) or url (http), not both")
	}

	kind := s.TransportKind()
	if s.Type == "" && kind == "" {
		return errors.New("server requires command or url")
	}
	switch kind {
	case TransportStdio:
		if s.Command == "" {
			return errors.New("stdio server requires command")
		}
	case TransportHTTP, TransportSSE:
		if s.URL == "" {
			return errors.New("http server requires url")
		}
	case TransportAuto:
		if s.URL == "" && s.Command == "" {
			return errors.New("server requires command or url")
		}
	default:
		return fmt.Errorf("unsupported transport type %q (want auto, stdio, http, or sse)", s.Type)
	}

	if s.URL != "" {
		u, err := url.Parse(s.URL)
		if err != nil {
			return fmt.Errorf("invalid url: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("invalid url scheme %q (want http or https)", u.Scheme)
		}
		if u.Host == "" {
			return errors.New("url must include a host")
		}
	}

	for key, value := range s.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("invalid environment variable name %q", key)
		}
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("environment variable %q contains a null byte", key)
		}
	}
	for key, value := range s.Headers {
		if key == "" || strings.ContainsAny(key, "\r\n") {
			return fmt.Errorf("invalid header name %q", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header %q contains a newline", key)
		}
	}

	if s.StartupTimeout < 0 || s.RequestTimeout < 0 || s.ShutdownTimeout < 0 {
		return errors.New("timeouts must not be negative")
	}
	if s.ShutdownTimeout == 0 {
		return errors.New("shutdown timeout must be greater than zero")
	}
	return nil
}
