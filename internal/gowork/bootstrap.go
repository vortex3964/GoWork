package gowork

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"GoWork/internal/appdir"
	"GoWork/internal/mcp"
	"GoWork/internal/netfetch"
	"GoWork/internal/remotesource"
	"GoWork/providers"
	"GoWork/tools"
	mcptool "GoWork/tools/MCPTool"
	"GoWork/tools/SkillsTool"
)

const (
	// BootstrapSkillName is the shipped skill that teaches the model how to
	// install skills and MCP servers. It is seeded on first run but never
	// loaded automatically.
	BootstrapSkillName = "gowork-bootstrap"

	// bootstrapSkillAsset is the bundled SKILL.md location relative to the
	// app directory / repository root.
	bootstrapSkillAsset = "skills/gowork-bootstrap/SKILL.md"
)

// setupSkillManager migrates the legacy executable-adjacent layout, builds the
// durable user manager, and seeds the bundled bootstrap skill when missing.
// It returns nil only when the user config directory is unavailable; the rest
// of the app keeps working without skills.
func setupSkillManager() *skillstool.Manager {
	if migrated, err := appdir.MigrateLegacySkillsDefault(); err != nil {
		log.Printf("legacy skill migration failed: %v", err)
	} else if migrated.Migrated() {
		log.Printf("migrated %d legacy skill file(s) into %s", len(migrated.Copied), mustSkillsDir())
	}

	mgr, err := skillstool.NewUserManager()
	if err != nil {
		log.Printf("skills are unavailable: %v", err)
		return nil
	}
	if seeded, err := seedBundledBootstrapSkill(mgr); err != nil {
		log.Printf("could not seed the bundled bootstrap skill: %v", err)
	} else if seeded {
		log.Printf("installed bundled skill %q in %s", BootstrapSkillName, mgr.Dir())
	}
	return mgr
}

func mustSkillsDir() string {
	dir, err := appdir.SkillsDir()
	if err != nil {
		return "the user skills directory"
	}
	return dir
}

// seedBundledBootstrapSkill reads the shipped SKILL.md and seeds it. A missing
// asset is reported but never fatal.
func seedBundledBootstrapSkill(mgr *skillstool.Manager) (bool, error) {
	data, err := readAsset(bootstrapSkillAsset)
	if err != nil {
		return false, err
	}
	return seedBootstrapSkill(mgr, string(data))
}

// seedBootstrapSkill installs content only when the skill does not exist yet.
// A user-modified copy is never overwritten and is not even parsed, so a
// broken local edit cannot prevent startup.
func seedBootstrapSkill(mgr *skillstool.Manager, content string) (bool, error) {
	if mgr == nil {
		return false, errors.New("skills are not available")
	}
	if mgr.Exists(BootstrapSkillName) {
		return false, nil
	}
	if _, err := mgr.Install(BootstrapSkillName, content, false); err != nil {
		if errors.Is(err, skillstool.ErrExists) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SeedBootstrapSkill installs the given SKILL.md content into the durable user
// skills directory when it is missing. It exists so integration tests can
// exercise the same first-run path as the app without depending on the
// executable's asset directory.
func SeedBootstrapSkill(content string) (bool, error) {
	mgr, err := skillstool.NewUserManager()
	if err != nil {
		return false, err
	}
	return seedBootstrapSkill(mgr, content)
}

// toolDefsFor converts the executable tool set into the provider definitions
// registered for the next model request. Keeping this in one place prevents
// the startup and Tools-tab rebuild paths from drifting.
func toolDefsFor(all []tools.AgentTool) []providers.ToolDef {
	defs := make([]providers.ToolDef, len(all))
	for i, tool := range all {
		defs[i] = providers.ToolDef{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.InputSchema(),
		}
	}
	return defs
}

// installTools returns the model-facing install/load tools. The MCP tools are
// bound to the app's single toolRuntime; no second MCP client pool is created.
func installTools(mcpRuntime mcptool.Runtime) []tools.AgentTool {
	out := []tools.AgentTool{
		skillstool.NewInstallTool(),
		skillstool.NewLoadTool(),
	}
	if mcpRuntime != nil {
		out = append(out, mcptool.New(mcpRuntime)...)
	}
	return out
}

// --- model-facing MCP runtime adapter --------------------------------

// List implements mcptool.Runtime with the sanitized view of the app's merged
// user+project configuration.
func (r *toolRuntime) List() []tools.MCPServerInfo {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mcpCfg == nil {
		return nil
	}
	names := r.mcpCfg.Names()
	out := make([]tools.MCPServerInfo, 0, len(names))
	for _, name := range names {
		server, _ := r.mcpCfg.Server(name)
		scope := r.mcpScopes[name]
		out = append(out, tools.MCPServerInfo{
			Name:      name,
			Transport: mcpTransportLabel(server),
			Enabled:   server.IsEnabled(),
			Connected: r.mcpClients[name] != nil,
			Tools:     len(r.mcpTools[name]),
			Scope:     scope,
			Trusted:   scope == tools.MCPScopeUser && server.AutoApproved(),
			Error:     r.mcpErrors[name],
		})
	}
	return out
}

func mcpTransportLabel(server mcp.ServerConfig) string {
	transport := string(server.TransportKind())
	switch transport {
	case "", string(mcp.TransportAuto):
		return "http"
	default:
		return transport
	}
}

// Remove implements mcptool.Runtime by delegating to the Tools-tab removal
// path, which already stops the client and unregisters its tools.
func (r *toolRuntime) Remove(name string) error {
	return r.RemoveMCP(name)
}

// Install implements mcptool.Runtime. It merges an inline or downloaded MCP
// config into the chosen scope, writes it atomically, and optionally starts
// the changed servers through the same clients the Tools tab owns.
func (r *toolRuntime) Install(ctx context.Context, opts tools.MCPInstallOptions) (result tools.MCPInstallResult, err error) {
	defer func() {
		if err != nil {
			err = sanitizeMCPInstallError(err)
		}
	}()
	if r == nil {
		return result, errors.New("MCP runtime is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	scope, err := normalizeAppMCPScope(opts.Scope)
	if err != nil {
		return result, err
	}
	cfg, configPath, err := r.scopeMCPConfigLocked(scope)
	if err != nil {
		return result, err
	}
	previousData, readErr := os.ReadFile(configPath)
	configExisted := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return result, readErr
	}
	previous := cloneMCPConfig(cfg)
	incoming, err := r.loadMCPInstallConfigLocked(ctx, opts)
	if err != nil {
		return result, err
	}
	selected, err := selectInstallMCPServers(incoming, opts.Servers)
	if err != nil {
		return result, err
	}

	// Work on a copy so a dry run or a failed merge/save cannot leave a
	// phantom server in the live cache.
	working := cloneMCPConfig(cfg)
	added, updated, err := mergeInstallMCPServers(working, selected, opts.Overwrite)
	if err != nil {
		return result, err
	}

	result = tools.MCPInstallResult{
		ConfigPath: configPath,
		Scope:      scope,
		Servers:    len(working.Servers),
		Installed:  added,
		Updated:    updated,
		DryRun:     opts.DryRun,
	}
	if opts.DryRun {
		return result, nil
	}

	changed := append(append([]string(nil), added...), updated...)
	sort.Strings(changed)
	preConnected := make(map[string]bool, len(changed))
	for _, name := range changed {
		preConnected[name] = r.mcpClients[name] != nil
	}

	r.setScopeMCPConfigLocked(scope, working)
	if err := r.saveMCPConfigScopeLocked(scope); err != nil {
		r.setScopeMCPConfigLocked(scope, cfg)
		return result, err
	}
	r.rebuildMCPConfigLocked()

	rollback := func(cause error, started []string) (tools.MCPInstallResult, error) {
		for _, name := range started {
			r.closeMCPClient(name)
		}
		r.setScopeMCPConfigLocked(scope, previous)
		r.rebuildMCPConfigLocked()

		var rollbackErrs []error
		if configExisted {
			if restoreErr := writeConfigFile(configPath, previousData); restoreErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("restoring mcp config: %w", restoreErr))
			}
		} else if removeErr := os.Remove(configPath); removeErr != nil && !os.IsNotExist(removeErr) {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("removing new mcp config: %w", removeErr))
		}
		for _, name := range changed {
			if !preConnected[name] {
				continue
			}
			server, ok := r.mcpCfg.Server(name)
			if !ok {
				continue
			}
			if restartErr := r.connectMCP(server); restartErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("restarting previous server %q: %w", name, restartErr))
			}
		}
		if len(rollbackErrs) > 0 {
			return result, fmt.Errorf("%w; rollback also failed: %v", cause, errors.Join(rollbackErrs...))
		}
		return result, fmt.Errorf("%w; install rolled back: no config was saved and no new server was left running", cause)
	}

	for _, name := range changed {
		r.closeMCPClient(name)
	}

	var started []string
	if opts.Start {
		var startErrs []error
		for _, name := range changed {
			server, ok := r.mcpCfg.Server(name)
			if !ok || !server.IsEnabled() {
				continue
			}
			if err := r.connectMCP(server); err != nil {
				startErrs = append(startErrs, fmt.Errorf("%s: %w", name, err))
				continue
			}
			started = append(started, name)
			result.Started = append(result.Started, name)
		}
		if len(startErrs) > 0 {
			return rollback(errors.Join(startErrs...), started)
		}
	}
	for _, name := range changed {
		count := len(r.mcpTools[name])
		result.Tools += count
		if count > 0 {
			if result.ServerTools == nil {
				result.ServerTools = map[string]int{}
			}
			result.ServerTools[name] = count
		}
	}
	sort.Strings(result.Started)
	return result, nil
}

func normalizeAppMCPScope(scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", tools.MCPScopeUser:
		return tools.MCPScopeUser, nil
	case tools.MCPScopeProject:
		return tools.MCPScopeProject, nil
	default:
		return "", fmt.Errorf("invalid mcp scope %q (want user or project)", scope)
	}
}

// scopeMCPConfigLocked returns the config and path for one scope, loading an
// unread file on demand. Callers hold r.mu.
func (r *toolRuntime) scopeMCPConfigLocked(scope string) (*mcp.Config, string, error) {
	var (
		cfg  *mcp.Config
		path string
	)
	switch scope {
	case tools.MCPScopeUser:
		cfg, path = r.mcpUserCfg, r.mcpPath
	case tools.MCPScopeProject:
		cfg, path = r.mcpProjectCfg, r.projectMCPPath
	default:
		return nil, "", fmt.Errorf("invalid mcp scope %q", scope)
	}
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("mcp %s config path is unavailable", scope)
	}
	if cfg == nil {
		loaded, err := loadMCPConfigFile(path)
		if err != nil {
			return nil, "", err
		}
		cfg = loaded
		switch scope {
		case tools.MCPScopeUser:
			r.mcpUserCfg = cfg
		case tools.MCPScopeProject:
			r.mcpProjectCfg = cfg
		}
	}
	return cfg, path, nil
}

// cloneMCPConfig copies the server map so a dry run or rejected merge cannot
// mutate the live configuration. Nested slices/maps are read-only here.
func cloneMCPConfig(cfg *mcp.Config) *mcp.Config {
	out := &mcp.Config{Servers: map[string]mcp.ServerConfig{}}
	if cfg == nil {
		return out
	}
	for name, server := range cfg.Servers {
		server.Name = name
		out.Servers[name] = server
	}
	return out
}

func (r *toolRuntime) setScopeMCPConfigLocked(scope string, cfg *mcp.Config) {
	switch scope {
	case tools.MCPScopeUser:
		r.mcpUserCfg = cfg
	case tools.MCPScopeProject:
		r.mcpProjectCfg = cfg
	}
}

func (r *toolRuntime) loadMCPInstallConfigLocked(ctx context.Context, opts tools.MCPInstallOptions) (*mcp.Config, error) {
	source := strings.TrimSpace(opts.Source)
	inline := len(bytes.TrimSpace(opts.Config)) > 0
	switch {
	case source == "" && !inline:
		return nil, errors.New("mcp_install requires source or config")
	case source != "" && inline:
		return nil, errors.New("mcp_install accepts either source or config, not both")
	}

	if inline {
		data, err := unwrapInlineMCPConfig(opts.Config)
		if err != nil {
			return nil, err
		}
		cfg, err := mcp.ParseConfig(data)
		if err != nil {
			return nil, fmt.Errorf("invalid mcp config: %w", err)
		}
		return cfg, nil
	}

	input, err := tools.ApplyMCPInstallRef(source, opts.Ref, r.root)
	if err != nil {
		return nil, err
	}
	resolver := remotesource.New(remotesource.Options{
		Root:  r.root,
		Token: netfetch.GitHubTokenFromEnv(),
	})
	remote, err := resolver.Resolve(ctx, input)
	if err != nil {
		return nil, err
	}

	all := append([]remotesource.Candidate(nil), remote.Candidates...)
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	candidates := all
	if wanted := strings.TrimSpace(opts.Path); wanted != "" {
		filtered := make([]remotesource.Candidate, 0, len(candidates))
		for _, candidate := range candidates {
			if matchMCPInstallPath(candidate.Path, wanted) {
				filtered = append(filtered, candidate)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("no mcp config matches path %q; candidates: %s\nnext call: repeat mcp_install with \"path\" set to one of the candidate paths above", wanted, mcpInstallCandidateList(all))
		}
		candidates = filtered
	}
	if len(candidates) == 0 {
		return nil, errors.New("source contains no mcp config\nnext call: pass a repository containing .mcp.json/mcp.json, a raw URL to one config file, a project-relative path, or use inline \"config\"")
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("source contains multiple files; set path to one of: %s\nnext call: repeat mcp_install with \"path\" set to one exact candidate path, \"start\":true", mcpInstallCandidateList(candidates))
	}

	data, err := remote.Fetch(ctx, candidates[0])
	if err != nil {
		return nil, err
	}
	cfg, err := mcp.ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("invalid mcp config in %s: %w\nnext call: fix the JSON to use one \"mcpServers\" object, or pass path to another candidate", candidates[0].Path, err)
	}
	return cfg, nil
}

func mcpInstallCandidateList(candidates []remotesource.Candidate) string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.Path)
	}
	sort.Strings(paths)
	return strings.Join(paths, ", ")
}

func matchMCPInstallPath(candidate, wanted string) bool {
	candidate = strings.Trim(strings.ReplaceAll(candidate, `\`, "/"), "/")
	wanted = strings.Trim(strings.ReplaceAll(wanted, `\`, "/"), "/")
	if candidate == wanted || strings.HasSuffix(candidate, "/"+wanted) {
		return true
	}
	return path.Base(candidate) == path.Base(wanted)
}

func unwrapInlineMCPConfig(raw json.RawMessage) ([]byte, error) {
	data := bytes.TrimSpace(raw)
	if len(data) == 0 {
		return nil, errors.New("mcp config is empty")
	}
	if data[0] == '"' {
		var encoded string
		if err := json.Unmarshal(data, &encoded); err != nil {
			return nil, fmt.Errorf("invalid mcp config: %w", err)
		}
		data = []byte(encoded)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("mcp config is empty")
	}
	return data, nil
}

func selectInstallMCPServers(cfg *mcp.Config, names []string) ([]mcp.ServerConfig, error) {
	if cfg == nil {
		return nil, errors.New("mcp config is empty")
	}
	if len(names) == 0 {
		return cfg.ListServers(), nil
	}
	seen := make(map[string]struct{}, len(names))
	selected := make([]mcp.ServerConfig, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		server, ok := cfg.Server(name)
		if !ok {
			return nil, fmt.Errorf("server %q is not present in the source config", name)
		}
		seen[name] = struct{}{}
		selected = append(selected, server)
	}
	if len(selected) == 0 {
		return nil, errors.New("no servers selected")
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	return selected, nil
}

func mergeInstallMCPServers(dest *mcp.Config, incoming []mcp.ServerConfig, overwrite bool) ([]string, []string, error) {
	var added, updated []string
	for _, server := range incoming {
		if _, exists := dest.Server(server.Name); exists {
			if !overwrite {
				return nil, nil, fmt.Errorf("server %q already exists; set overwrite to replace it", server.Name)
			}
			server.Name = strings.TrimSpace(server.Name)
			dest.Servers[server.Name] = server
			updated = append(updated, server.Name)
			continue
		}
		if err := dest.AddServer(server); err != nil {
			return nil, nil, err
		}
		added = append(added, server.Name)
	}
	sort.Strings(added)
	sort.Strings(updated)
	return added, updated, nil
}

var (
	mcpUserInfoInErrorRE   = regexp.MustCompile(`://[^/\s@]+@`)
	mcpQueryValueInErrorRE = regexp.MustCompile(`([?&][^=\s&]+=)([^&\s]+)`)
)

// sanitizeMCPInstallError removes credentials from download/merge failures.
func sanitizeMCPInstallError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if token := strings.TrimSpace(netfetch.GitHubTokenFromEnv()); token != "" {
		message = strings.ReplaceAll(message, token, "[redacted]")
	}
	message = mcpUserInfoInErrorRE.ReplaceAllString(message, "://[redacted]@")
	message = mcpQueryValueInErrorRE.ReplaceAllString(message, "${1}[redacted]")
	return errors.New(message)
}

// redactMCPServerSecrets removes configured env, header and long argument
// values from a server error before it is stored or shown. Child processes may
// echo their environment or arguments on failure.
func redactMCPServerSecrets(message string, server mcp.ServerConfig) string {
	secrets := make([]string, 0, len(server.Env)+len(server.Headers)+len(server.Args))
	for _, value := range server.Env {
		if len(value) >= 4 {
			secrets = append(secrets, value)
		}
	}
	for _, value := range server.Headers {
		if len(value) >= 4 {
			secrets = append(secrets, value)
		}
	}
	for _, value := range server.Args {
		if len(value) >= 8 {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		message = strings.ReplaceAll(message, secret, "[redacted]")
	}
	return message
}
