//DESC: launch/detection metadata for the local providers (llama.cpp, LM Studio)

package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LocalServerSpec describes how GoWork can start and probe one of the
// OpenAI-compatible local servers. Command and arguments are deliberately
// configurable through the environment: the binaries are not always on PATH
// (LM Studio installs its CLI under ~/.lmstudio/bin) and llama-server needs at
// least a model path.
type LocalServerSpec struct {
	// Provider is the canonical lower-case provider name.
	Provider    string
	DisplayName string

	// CommandNames are the executable names looked up on PATH, in order.
	CommandNames []string
	// CommandEnv overrides the executable entirely (path or name).
	CommandEnv string
	// ArgsEnv overrides DefaultArgs. It accepts a JSON array of strings or a
	// whitespace-separated argument list.
	ArgsEnv     string
	DefaultArgs []string

	// BaseURL is the server root used for model requests.
	BaseURL string
	// HealthPaths are appended to BaseURL and probed until one returns 200.
	HealthPaths []string

	// ServiceCLI marks launchers such as `lms server start` that hand the
	// server to a background daemon and exit. A zero exit is then expected
	// before the health endpoint becomes ready.
	ServiceCLI bool
	// StopArgs are run when a ServiceCLI launcher exited successfully and the
	// managed service has to be stopped again.
	StopArgs []string

	// StartTimeout bounds how long readiness may take. Zero means use the
	// manager default.
	StartTimeout time.Duration
}

// LocalServerStartTimeout reads LOCAL_SERVER_START_TIMEOUT. It accepts a Go
// duration ("3m", "45s") or a plain number of seconds. Invalid values fall
// back to two minutes.
func LocalServerStartTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("LOCAL_SERVER_START_TIMEOUT"))
	if raw == "" {
		return 2 * time.Minute
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 2 * time.Minute
}

// ParseLocalServerArgs parses LLAMACPP_ARGS/LMSTUDIO_ARGS. A JSON array is
// preferred because it preserves paths containing spaces; anything else is
// split on whitespace.
func ParseLocalServerArgs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err == nil {
			return args
		}
	}
	return strings.Fields(raw)
}

// ResolveCommand returns the executable to run plus its configured arguments.
// The command override wins, then the PATH candidates. Errors name the
// environment variable to set rather than dumping the environment itself.
func (s LocalServerSpec) ResolveCommand() (string, []string, error) {
	if override := strings.TrimSpace(os.Getenv(s.CommandEnv)); override != "" {
		if path, err := exec.LookPath(override); err == nil {
			return path, s.resolvedArgs(), nil
		}
		return "", nil, fmt.Errorf("configured %s executable is not available (check %s)", s.DisplayName, s.CommandEnv)
	}
	for _, name := range s.CommandNames {
		if path, err := exec.LookPath(name); err == nil {
			return path, s.resolvedArgs(), nil
		}
	}
	return "", nil, fmt.Errorf("%s executable not found on PATH (set %s to its location)", s.DisplayName, s.CommandEnv)
}

func (s LocalServerSpec) resolvedArgs() []string {
	if raw := strings.TrimSpace(os.Getenv(s.ArgsEnv)); raw != "" {
		return ParseLocalServerArgs(raw)
	}
	return append([]string(nil), s.DefaultArgs...)
}

// HealthURLs expands HealthPaths against the configured base URL.
func (s LocalServerSpec) HealthURLs() []string {
	base := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if base == "" {
		return nil
	}
	urls := make([]string, 0, len(s.HealthPaths))
	for _, p := range s.HealthPaths {
		urls = append(urls, base+"/"+strings.TrimLeft(p, "/"))
	}
	return urls
}

// CanonicalLocalProviderName folds the spellings used by the picker and the
// provider registry into the lower-case key shared by the launch manager.
func CanonicalLocalProviderName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(n)
	switch n {
	case "llamacpp":
		return "llamacpp"
	case "lmstudio":
		return "lmstudio"
	default:
		return n
	}
}

// LocalServerSpecFor returns the launch spec for a local provider that GoWork
// knows how to start. Ollama deliberately has no spec: it manages its own
// daemon and already documents `ollama serve`.
func LocalServerSpecFor(provider string) (LocalServerSpec, bool) {
	switch CanonicalLocalProviderName(provider) {
	case "llamacpp":
		return LlamaCppLaunchSpec(), true
	case "lmstudio":
		return LMStudioLaunchSpec(), true
	default:
		return LocalServerSpec{}, false
	}
}
