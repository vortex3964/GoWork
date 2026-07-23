package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const defaultEnvPath = ".env"

// updateEnvKeys rewrites or appends the given keys in .env without dropping
// unrelated lines or comments. Keys with an empty value are left unchanged
// (useful when the user chose "use existing" for API_KEY).
func updateEnvKeys(path string, updates map[string]string) error {
	if path == "" {
		path = defaultEnvPath
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	seen := make(map[string]bool, len(updates))
	var out strings.Builder

	if len(existing) > 0 {
		sc := bufio.NewScanner(strings.NewReader(string(existing)))
		for sc.Scan() {
			line := sc.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				out.WriteString(line)
				out.WriteByte('\n')
				continue
			}

			key, _, ok := strings.Cut(trimmed, "=")
			if !ok {
				out.WriteString(line)
				out.WriteByte('\n')
				continue
			}
			key = strings.TrimSpace(key)
			if val, want := updates[key]; want {
				seen[key] = true
				if val == "" {
					// keep existing line when caller asked not to overwrite
					out.WriteString(line)
					out.WriteByte('\n')
					continue
				}
				out.WriteString(key)
				out.WriteByte('=')
				out.WriteString(val)
				out.WriteByte('\n')
				continue
			}
			out.WriteString(line)
			out.WriteByte('\n')
		}
		if err := sc.Err(); err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
	}

	for key, val := range updates {
		if seen[key] || val == "" {
			continue
		}
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(val)
		out.WriteByte('\n')
	}

	return os.WriteFile(path, []byte(out.String()), 0644)
}

// saveProviderPrefs writes PROVIDER and model (and optionally API_KEY) to .env.
// Pass apiKey="" to leave the existing API_KEY line alone.
func saveProviderPrefs(provider, modelID, apiKey string) error {
	updates := map[string]string{
		"PROVIDER": provider,
		"model":    modelID,
	}
	if apiKey != "" {
		updates["API_KEY"] = apiKey
	}
	return updateEnvKeys(defaultEnvPath, updates)
}
