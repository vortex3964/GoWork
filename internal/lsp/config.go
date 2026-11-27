// Package lsp provides the foundation for language-server support in GoWork:
// PATH detection for common servers, a validated model for user-configured
// servers, and a minimal stdio JSON-RPC client.
package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

var (
	// ErrServerNotFound is returned when a configured server is missing.
	ErrServerNotFound = errors.New("lsp: server not found")
	// ErrDuplicateServer is returned when a name is configured twice.
	ErrDuplicateServer = errors.New("lsp: server already configured")
)

// ServerConfig describes one user-added language server. Command is resolved
// through PATH at start time unless it is an absolute/relative path. Enabled
// lets a server stay configured but not be started by the UI.
type ServerConfig struct {
	Name        string            `json:"name"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	LanguageIDs []string          `json:"language_ids"`
	Enabled     bool              `json:"enabled"`
}

// normalized validates a config and returns a defensive copy with trimmed
// names/commands and de-duplicated language IDs.
func (s ServerConfig) normalized() (ServerConfig, error) {
	out := ServerConfig{
		Name:        strings.TrimSpace(s.Name),
		Command:     strings.TrimSpace(s.Command),
		Args:        append([]string(nil), s.Args...),
		Enabled:     s.Enabled,
		LanguageIDs: nil,
	}
	if out.Name == "" {
		return ServerConfig{}, fmt.Errorf("lsp: server name is required")
	}
	if out.Command == "" {
		return ServerConfig{}, fmt.Errorf("lsp: server %q command is required", out.Name)
	}
	if len(s.Env) > 0 {
		out.Env = make(map[string]string, len(s.Env))
		for key, value := range s.Env {
			key = strings.TrimSpace(key)
			if key == "" {
				return ServerConfig{}, fmt.Errorf("lsp: server %q has an empty environment variable name", out.Name)
			}
			out.Env[key] = value
		}
	}

	seen := make(map[string]struct{}, len(s.LanguageIDs))
	for _, id := range s.LanguageIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		lower := strings.ToLower(id)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out.LanguageIDs = append(out.LanguageIDs, id)
	}
	if len(out.LanguageIDs) == 0 {
		return ServerConfig{}, fmt.Errorf("lsp: server %q must declare at least one language ID", out.Name)
	}
	return out, nil
}

// Validate reports whether the configuration has a name, command, at least
// one language ID, and no empty environment variable names.
func (s ServerConfig) Validate() error {
	_, err := s.normalized()
	return err
}

func (s ServerConfig) clone() ServerConfig {
	out := s
	out.Args = append([]string(nil), s.Args...)
	if s.Env != nil {
		out.Env = make(map[string]string, len(s.Env))
		for key, value := range s.Env {
			out.Env[key] = value
		}
	}
	out.LanguageIDs = append([]string(nil), s.LanguageIDs...)
	return out
}

// Config is the concurrency-safe in-memory collection of user-added language
// servers. Its zero value is ready to use.
type Config struct {
	mu      sync.RWMutex
	servers []ServerConfig
}

// NewConfig returns an empty user-server configuration.
func NewConfig() *Config {
	return &Config{}
}

// configFile is the JSON document shape accepted by ParseConfig:
//
//	{"servers": [{"name": "...", "command": "...", ...}]}
//
// A bare JSON array of servers is also accepted for convenience.
type configFile struct {
	Servers []ServerConfig `json:"servers"`
}

// ParseConfig parses and validates a user-server configuration document. It
// rejects unknown fields, duplicate names, and trailing data after the
// document.
func ParseConfig(data []byte) (*Config, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("lsp: config is empty")
	}

	var servers []ServerConfig
	if trimmed[0] == '[' {
		if err := decodeStrict(trimmed, &servers); err != nil {
			return nil, fmt.Errorf("lsp: parse config: %w", err)
		}
	} else {
		var doc configFile
		if err := decodeStrict(trimmed, &doc); err != nil {
			return nil, fmt.Errorf("lsp: parse config: %w", err)
		}
		servers = doc.Servers
	}

	cfg := NewConfig()
	for _, server := range servers {
		if err := cfg.Add(server); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// ParseConfigReader reads and parses a configuration document.
func ParseConfigReader(r io.Reader) (*Config, error) {
	if r == nil {
		return nil, fmt.Errorf("lsp: config reader is nil")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("lsp: read config: %w", err)
	}
	return ParseConfig(data)
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing data")
		}
		return err
	}
	return nil
}

// Add validates and appends a server. Names are unique case-insensitively.
func (c *Config) Add(server ServerConfig) error {
	normalized, err := server.normalized()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, existing := range c.servers {
		if strings.EqualFold(existing.Name, normalized.Name) {
			return fmt.Errorf("%w: %q", ErrDuplicateServer, normalized.Name)
		}
	}
	c.servers = append(c.servers, normalized)
	return nil
}

// Remove deletes the named server. Names are matched case-insensitively.
func (c *Config) Remove(name string) error {
	name = strings.TrimSpace(name)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, existing := range c.servers {
		if strings.EqualFold(existing.Name, name) {
			c.servers = append(c.servers[:i], c.servers[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrServerNotFound, name)
}

// List returns a defensive copy of every configured server in insertion order.
func (c *Config) List() []ServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ServerConfig, len(c.servers))
	for i, server := range c.servers {
		out[i] = server.clone()
	}
	return out
}

// Get returns a defensive copy of the named server.
func (c *Config) Get(name string) (ServerConfig, bool) {
	name = strings.TrimSpace(name)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, server := range c.servers {
		if strings.EqualFold(server.Name, name) {
			return server.clone(), true
		}
	}
	return ServerConfig{}, false
}

// Len reports how many servers are configured.
func (c *Config) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.servers)
}

// SetEnabled toggles the enabled flag of an existing server.
func (c *Config) SetEnabled(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.servers {
		if strings.EqualFold(c.servers[i].Name, name) {
			c.servers[i].Enabled = enabled
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrServerNotFound, name)
}
