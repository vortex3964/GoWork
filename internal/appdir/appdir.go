// Package appdir resolves the durable, per-user locations where GoWork keeps
// configuration, skills and caches.
//
// Application data must never live in the user's project directory. The
// canonical root is the platform user-config directory (for example
// ~/.config/GoWork on Linux) unless GOWORK_CONFIG_DIR overrides it. Every
// directory this package returns is created with mode 0700 for the current
// user only.
package appdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvConfigDir overrides the platform configuration root. It is useful for
	// portable installs and for tests.
	EnvConfigDir = "GOWORK_CONFIG_DIR"

	// AppName is the directory GoWork owns below the platform config and cache
	// roots.
	AppName = "GoWork"

	// SkillsDirName is the directory that holds installed skills.
	SkillsDirName = "skills"

	// MCPConfigFile is the user-level MCP configuration file name. It matches
	// the existing app-data convention used by the MCP runtime.
	MCPConfigFile = ".mcp.json"

	// LSPConfigFile is the user-level LSP configuration file name.
	LSPConfigFile = ".lsp.json"
)

// ConfigDir returns the per-user configuration directory and ensures it
// exists with mode 0700. GOWORK_CONFIG_DIR wins over the platform default.
func ConfigDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvConfigDir)); override != "" {
		return ensureDir(override)
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("appdir: locate user config directory: %w", err)
	}
	return ensureDir(filepath.Join(base, AppName))
}

// SkillsDir returns the durable skills directory and ensures it exists with
// mode 0700. Installed skills live in <dir>/<name>/SKILL.md.
func SkillsDir() (string, error) {
	config, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return ensureDir(filepath.Join(config, SkillsDirName))
}

// MCPConfigPath returns the user-level MCP configuration path. The config
// directory is created (0700); the file itself is not created.
func MCPConfigPath() (string, error) {
	config, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, MCPConfigFile), nil
}

// LSPConfigPath returns the user-level LSP configuration path. The config
// directory is created (0700); the file itself is not created.
func LSPConfigPath() (string, error) {
	config, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, LSPConfigFile), nil
}

// CacheDir returns the per-user cache directory and ensures it exists with
// mode 0700. Cached downloads are disposable and must never be treated as
// configuration.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("appdir: locate user cache directory: %w", err)
	}
	return ensureDir(filepath.Join(base, AppName))
}

// ensureDir creates path and verifies that it is a directory. MkdirAll uses
// 0700 for every component it creates. An existing directory keeps its
// permissions; an explicit override such as GOWORK_CONFIG_DIR may point at a
// directory the user already manages.
func ensureDir(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return "", errors.New("appdir: directory path must not be empty")
	}

	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("appdir: create directory %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("appdir: inspect directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("appdir: %s is not a directory", path)
	}
	return path, nil
}
