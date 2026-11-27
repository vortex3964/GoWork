package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"GoWork/internal/appdir"
	"GoWork/internal/mcp"
	"GoWork/internal/remotesource"
	"GoWork/providers"
)

const (
	// MCPScopeUser is the durable per-user MCP configuration. Only this scope
	// may opt into automatic tool approval.
	MCPScopeUser = "user"

	// MCPScopeProject is a project-level .mcp.json. Project configuration is
	// never auto-approved, regardless of what the file says.
	MCPScopeProject = "project"

	// DefaultMCPStartConcurrency bounds how many servers start at once.
	DefaultMCPStartConcurrency = 4

	// maxMCPRuntimeTimeout keeps one startup/discovery attempt from stalling
	// the app indefinitely even when a server config asks for a long timeout.
	maxMCPRuntimeTimeout = 20 * time.Second
)

// MCPRuntimeClient is the subset of mcp.Client the runtime needs. Tests can
// replace it through MCPRuntimeOptions.ClientFactory without spawning servers.
type MCPRuntimeClient interface {
	Start(ctx context.Context) error
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	CallTool(ctx context.Context, name string, arguments any) (*mcp.CallResult, error)
	Close() error
}

var _ MCPRuntimeClient = (*mcp.Client)(nil)

// MCPRuntimeOptions configures a runtime. Zero values are safe: the user
// config defaults to appdir.MCPConfigPath and servers start four at a time.
type MCPRuntimeOptions struct {
	// ConfigPath is the user-scope config file. Empty uses appdir.MCPConfigPath.
	ConfigPath string

	// ProjectRoot is the project directory used for project-scope config and
	// for resolving local install sources.
	ProjectRoot string

	// ProjectConfigPath overrides the project-scope config file. Empty uses
	// <ProjectRoot>/.mcp.json when ProjectRoot is set.
	ProjectConfigPath string

	// Dispatcher receives discovered MCP tools as they start and loses them as
	// they stop. A nil dispatcher is allowed; provider definitions are still
	// registered.
	Dispatcher *Dispatcher

	// MaxConcurrent bounds parallel startup. Values <= 0 use the default.
	MaxConcurrent int

	// ClientFactory is a test seam. Nil uses mcp.NewClient.
	ClientFactory func(mcp.ServerConfig) (MCPRuntimeClient, error)

	// Fetcher/APIBaseURL/Token/UserAgent are forwarded to remotesource for
	// mcp_install source downloads. Fetcher is also a test seam.
	Fetcher    remotesource.Fetcher
	APIBaseURL string
	Token      string
	UserAgent  string
}

// MCPServerInfo is the sanitized list view of one configured server. It never
// contains environment or header values.
type MCPServerInfo struct {
	Name      string
	Transport string
	Enabled   bool
	Connected bool
	Tools     int
	Scope     string
	Trusted   bool
	Error     string
}

// MCPInstallOptions selects an inline or remote MCP configuration and merges
// it into one scope.
type MCPInstallOptions struct {
	Source    string
	Config    json.RawMessage
	Path      string
	Ref       string
	Scope     string
	Servers   []string
	Overwrite bool
	Start     bool
	DryRun    bool
}

// MCPInstallResult reports what an install did (or would do on a dry run).
// Errors are sanitized and never include env/header values.
type MCPInstallResult struct {
	ConfigPath string
	Scope      string
	Servers    int
	Tools      int
	Installed  []string
	Updated    []string
	Started    []string
	Errors     []string
	DryRun     bool
}

// MCPRuntime owns MCP client lifecycles and keeps the dispatcher and provider
// registries in sync with discovered tools. Init/StartAll never tear down the
// application: a broken config or server is recorded and the rest continue.
type MCPRuntime struct {
	lifecycleMu sync.Mutex
	mu          sync.Mutex

	options           MCPRuntimeOptions
	configPath        string
	projectConfigPath string
	root              string
	maxConcurrent     int

	userCfg    *mcp.Config
	projectCfg *mcp.Config
	cfg        *mcp.Config
	scopes     map[string]string

	clients      map[string]MCPRuntimeClient
	agentTools   map[string][]AgentTool
	providerDefs map[string][]providers.ToolDef
	errors       map[string]string
	configErr    string

	initialized bool
	closed      bool
}

// NewMCPRuntime builds a runtime without touching disk or the network. Call
// Init followed by StartAll during app startup.
func NewMCPRuntime(opts MCPRuntimeOptions) *MCPRuntime {
	r := &MCPRuntime{
		options:           opts,
		userCfg:           &mcp.Config{Servers: map[string]mcp.ServerConfig{}},
		projectCfg:        &mcp.Config{Servers: map[string]mcp.ServerConfig{}},
		cfg:               &mcp.Config{Servers: map[string]mcp.ServerConfig{}},
		scopes:            map[string]string{},
		clients:           map[string]MCPRuntimeClient{},
		agentTools:        map[string][]AgentTool{},
		providerDefs:      map[string][]providers.ToolDef{},
		errors:            map[string]string{},
		maxConcurrent:     opts.MaxConcurrent,
		configPath:        strings.TrimSpace(opts.ConfigPath),
		projectConfigPath: strings.TrimSpace(opts.ProjectConfigPath),
	}

	if r.maxConcurrent <= 0 {
		r.maxConcurrent = DefaultMCPStartConcurrency
	}
	if r.maxConcurrent > mcp.MaxServers {
		r.maxConcurrent = mcp.MaxServers
	}

	if root := strings.TrimSpace(opts.ProjectRoot); root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		r.root = root
	}
	if r.configPath == "" {
		if configPath, err := appdir.MCPConfigPath(); err == nil {
			r.configPath = configPath
		} else {
			r.configErr = err.Error()
		}
	}
	if r.projectConfigPath == "" && r.root != "" {
		r.projectConfigPath = filepath.Join(r.root, mcp.DefaultConfigPath)
	}
	return r
}

// ConfigPath returns the effective user-scope config path.
func (r *MCPRuntime) ConfigPath() string {
	if r == nil {
		return ""
	}
	return r.configPath
}

// ProjectConfigPath returns the effective project-scope config path (empty
// when the runtime has no project root).
func (r *MCPRuntime) ProjectConfigPath() string {
	if r == nil {
		return ""
	}
	return r.projectConfigPath
}

// ConfigError returns the last config-level load error, if any.
func (r *MCPRuntime) ConfigError() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configErr
}

// Init loads both configuration scopes. Missing files are empty configs; a
// malformed file is recorded and the other scope still loads. The returned
// error is diagnostic only and callers should keep the app running.
func (r *MCPRuntime) Init(ctx context.Context) error {
	if r == nil {
		return errors.New("mcp runtime is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return mcp.ErrClosed
	}
	configPath := r.configPath
	projectPath := r.projectConfigPath
	r.mu.Unlock()

	var errs []error
	userCfg, err := loadOptionalMCPConfig(configPath)
	if err != nil {
		errs = append(errs, err)
	}
	projectCfg, err := loadOptionalMCPConfig(projectPath)
	if err != nil {
		errs = append(errs, err)
	}

	r.mu.Lock()
	r.userCfg = userCfg
	r.projectCfg = projectCfg
	r.rebuildConfigLocked()
	r.configErr = ""
	if len(errs) > 0 {
		r.configErr = errors.Join(errs...).Error()
	}
	r.initialized = true
	r.mu.Unlock()
	return errors.Join(errs...)
}

func loadOptionalMCPConfig(path string) (*mcp.Config, error) {
	empty := &mcp.Config{Servers: map[string]mcp.ServerConfig{}}
	path = strings.TrimSpace(path)
	if path == "" {
		return empty, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty, nil
		}
		return empty, fmt.Errorf("read mcp config %s: %w", path, err)
	}
	cfg, err := mcp.ParseConfig(data)
	if err != nil {
		return empty, fmt.Errorf("%s: %w", path, err)
	}
	if cfg == nil {
		return empty, nil
	}
	return cfg, nil
}

// rebuildConfigLocked merges user and project scope. Project entries win name
// collisions and are always marked as project scope so they cannot inherit a
// trusted flag. Callers must hold r.mu.
func (r *MCPRuntime) rebuildConfigLocked() {
	merged := &mcp.Config{Servers: map[string]mcp.ServerConfig{}}
	scopes := map[string]string{}
	if r.userCfg != nil {
		for _, name := range r.userCfg.Names() {
			server, _ := r.userCfg.Server(name)
			merged.Servers[name] = server
			scopes[name] = MCPScopeUser
		}
	}
	if r.projectCfg != nil {
		for _, name := range r.projectCfg.Names() {
			server, _ := r.projectCfg.Server(name)
			merged.Servers[name] = server
			scopes[name] = MCPScopeProject
		}
	}
	r.cfg = merged
	r.scopes = scopes
}

// StartAll starts every enabled server with bounded concurrency. Failures are
// isolated: the server's config entry stays in place, its error (including a
// stderr tail from the client) is reported by List, and other servers continue
// starting. Callers may ignore the returned aggregate without affecting app
// startup.
func (r *MCPRuntime) StartAll(ctx context.Context) error {
	if r == nil {
		return errors.New("mcp runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	initialized := r.initialized
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return mcp.ErrClosed
	}
	if !initialized {
		_ = r.Init(ctx)
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return mcp.ErrClosed
	}
	var names []string
	for _, server := range r.cfg.EnabledServers() {
		if r.clients[server.Name] == nil {
			names = append(names, server.Name)
		}
	}
	r.mu.Unlock()
	if len(names) == 0 {
		return nil
	}

	sem := make(chan struct{}, r.maxConcurrent)
	errCh := make(chan error, len(names))
	var wg sync.WaitGroup
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errCh <- fmt.Errorf("mcp server %q: %w", name, ctx.Err())
				return
			}
			if err := r.startServer(ctx, name); err != nil {
				errCh <- fmt.Errorf("mcp server %q: %w", name, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (r *MCPRuntime) startServer(ctx context.Context, name string) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return mcp.ErrClosed
	}
	if r.clients[name] != nil {
		r.mu.Unlock()
		return nil
	}
	server, ok := r.cfg.Server(name)
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %q", mcp.ErrServerNotFound, name)
	}
	scope := r.scopes[name]
	factory := r.options.ClientFactory
	r.mu.Unlock()

	if factory == nil {
		factory = func(cfg mcp.ServerConfig) (MCPRuntimeClient, error) {
			return mcp.NewClient(cfg)
		}
	}
	client, err := factory(server)
	if err != nil {
		err = sanitizeMCPRuntimeError(err, server)
		r.recordServerError(name, err)
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = client.Close()
		}
	}()

	startCtx, cancel := context.WithTimeout(ctx, mcpStartupTimeout(server))
	err = client.Start(startCtx)
	cancel()
	if err != nil {
		err = sanitizeMCPRuntimeError(err, server)
		r.recordServerError(name, err)
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, mcpDiscoveryTimeout(server))
	definitions, err := client.ListTools(listCtx)
	cancel()
	if err != nil {
		err = sanitizeMCPRuntimeError(err, server)
		r.recordServerError(name, err)
		return err
	}

	kind := mcpBridgeKind(scope, server)
	agentTools, defs, err := MCPTools(name, client, definitions, WithMCPToolKind(kind))
	if err != nil {
		err = sanitizeMCPRuntimeError(err, server)
		r.recordServerError(name, err)
		return err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return mcp.ErrClosed
	}
	if r.clients[name] != nil {
		// Another caller connected this name while we were starting.
		r.mu.Unlock()
		return nil
	}
	r.clients[name] = client
	r.agentTools[name] = agentTools
	r.providerDefs[name] = defs
	delete(r.errors, name)
	r.mu.Unlock()

	registerMCPTools(r.options.Dispatcher, agentTools, defs)
	started = true
	return nil
}

func mcpStartupTimeout(server mcp.ServerConfig) time.Duration {
	timeout := server.StartupTimeout.Duration()
	if timeout <= 0 {
		timeout = mcp.DefaultStartupTimeout
	}
	if timeout > maxMCPRuntimeTimeout {
		timeout = maxMCPRuntimeTimeout
	}
	return timeout
}

func mcpDiscoveryTimeout(server mcp.ServerConfig) time.Duration {
	timeout := server.RequestTimeout.Duration()
	if timeout <= 0 {
		timeout = mcp.DefaultRequestTimeout
	}
	if timeout > maxMCPRuntimeTimeout {
		timeout = maxMCPRuntimeTimeout
	}
	return timeout
}

func mcpBridgeKind(scope string, server mcp.ServerConfig) Kind {
	if scope == MCPScopeUser && server.AutoApproved() {
		return KindAllowed
	}
	return KindNotAllowed
}

func registerMCPTools(dispatcher *Dispatcher, agentTools []AgentTool, defs []providers.ToolDef) {
	if dispatcher != nil {
		for _, tool := range agentTools {
			_ = dispatcher.Register(tool)
		}
	}
	for _, def := range defs {
		providers.AddToolDef(def)
	}
}

func unregisterMCPTools(dispatcher *Dispatcher, agentTools []AgentTool, defs []providers.ToolDef) {
	if dispatcher != nil {
		for _, tool := range agentTools {
			dispatcher.Unregister(tool.Name())
		}
	}
	for _, def := range defs {
		providers.RemoveToolDef(def.Name)
	}
}

// sanitizeMCPRuntimeError redacts configured secret values before an error is
// stored in List or returned to the model. Server stderr is useful for
// diagnostics, but a child process may echo its environment or headers.
func sanitizeMCPRuntimeError(err error, server mcp.ServerConfig) error {
	if err == nil {
		return nil
	}
	return errors.New(redactMCPSecrets(err.Error(), server))
}

func redactMCPSecrets(message string, server mcp.ServerConfig) string {
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

func (r *MCPRuntime) recordServerError(name string, err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.errors[name] = err.Error()
	r.mu.Unlock()
}

// stopServer unregisters and closes one server. It is a no-op for unknown
// names and always removes local tool registrations before closing the client.
func (r *MCPRuntime) stopServer(name string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	client := r.clients[name]
	agentTools := r.agentTools[name]
	defs := r.providerDefs[name]
	delete(r.clients, name)
	delete(r.agentTools, name)
	delete(r.providerDefs, name)
	delete(r.errors, name)
	r.mu.Unlock()

	unregisterMCPTools(r.options.Dispatcher, agentTools, defs)
	if client != nil {
		return client.Close()
	}
	return nil
}

// List returns the sanitized server view used by mcp_list. It never exposes
// env or header values.
func (r *MCPRuntime) List() []MCPServerInfo {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg == nil {
		return nil
	}
	names := r.cfg.Names()
	out := make([]MCPServerInfo, 0, len(names))
	for _, name := range names {
		server, _ := r.cfg.Server(name)
		scope := r.scopes[name]
		out = append(out, MCPServerInfo{
			Name:      name,
			Transport: mcpTransportLabel(server),
			Enabled:   server.IsEnabled(),
			Connected: r.clients[name] != nil,
			Tools:     len(r.agentTools[name]),
			Scope:     scope,
			Trusted:   scope == MCPScopeUser && server.AutoApproved(),
			Error:     r.errors[name],
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

// ToolCount returns the number of MCP tools currently registered.
func (r *MCPRuntime) ToolCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, tools := range r.agentTools {
		total += len(tools)
	}
	return total
}

// Install merges an inline or downloaded MCP config into the selected scope,
// saves it atomically with mode 0600, and optionally starts the selected
// servers. A dry run validates and reports but writes and starts nothing.
func (r *MCPRuntime) Install(ctx context.Context, opts MCPInstallOptions) (MCPInstallResult, error) {
	var result MCPInstallResult
	if r == nil {
		return result, errors.New("mcp runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scope, err := normalizeMCPScope(opts.Scope)
	if err != nil {
		return result, err
	}

	r.mu.Lock()
	initialized := r.initialized
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return result, mcp.ErrClosed
	}
	if !initialized {
		if err := r.Init(ctx); err != nil {
			// A broken existing config must not make a fresh install
			// impossible; the destination is still validated on its own.
			_ = err
		}
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	destinationPath, err := r.pathForScope(scope)
	if err != nil {
		return result, err
	}
	existing, err := loadOptionalMCPConfig(destinationPath)
	if err != nil {
		return result, err
	}
	incoming, err := r.loadInstallConfig(ctx, opts)
	if err != nil {
		return result, err
	}
	selected, err := selectInstallServers(incoming, opts.Servers)
	if err != nil {
		return result, err
	}

	added, updated, err := mergeInstallServers(existing, selected, opts.Overwrite)
	if err != nil {
		return result, err
	}
	result.ConfigPath = destinationPath
	result.Scope = scope
	result.Servers = len(existing.Servers)
	result.Installed = added
	result.Updated = updated
	result.DryRun = opts.DryRun
	if opts.DryRun {
		return result, nil
	}

	if err := existing.Save(destinationPath); err != nil {
		return result, err
	}

	r.mu.Lock()
	if scope == MCPScopeUser {
		r.userCfg = existing
	} else {
		r.projectCfg = existing
	}
	r.rebuildConfigLocked()
	r.configErr = ""
	r.mu.Unlock()

	names := append(append([]string(nil), added...), updated...)
	sort.Strings(names)
	for _, name := range names {
		_ = r.stopServer(name)
	}

	if opts.Start {
		for _, name := range names {
			r.mu.Lock()
			server, ok := r.cfg.Server(name)
			enabled := ok && server.IsEnabled()
			r.mu.Unlock()
			if !enabled {
				continue
			}
			if err := r.startServer(ctx, name); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			result.Started = append(result.Started, name)
		}
	}

	r.mu.Lock()
	for _, name := range names {
		result.Tools += len(r.agentTools[name])
	}
	r.mu.Unlock()
	sort.Strings(result.Started)
	sort.Strings(result.Errors)
	return result, nil
}

func normalizeMCPScope(scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", MCPScopeUser:
		return MCPScopeUser, nil
	case MCPScopeProject:
		return MCPScopeProject, nil
	default:
		return "", fmt.Errorf("invalid mcp scope %q (want user or project)", scope)
	}
}

func (r *MCPRuntime) pathForScope(scope string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch scope {
	case MCPScopeUser:
		if strings.TrimSpace(r.configPath) == "" {
			return "", errors.New("user mcp config path is unavailable")
		}
		return r.configPath, nil
	case MCPScopeProject:
		if strings.TrimSpace(r.projectConfigPath) == "" {
			return "", errors.New("project mcp config path is unavailable; set ProjectRoot or ProjectConfigPath")
		}
		return r.projectConfigPath, nil
	default:
		return "", fmt.Errorf("invalid mcp scope %q", scope)
	}
}

func (r *MCPRuntime) loadInstallConfig(ctx context.Context, opts MCPInstallOptions) (*mcp.Config, error) {
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

	input := source
	ref := strings.TrimSpace(opts.Ref)
	if ref != "" && !strings.Contains(input, "://") && !strings.Contains(input, "@") && len(strings.FieldsFunc(input, func(r rune) bool { return r == '/' || r == '\\' })) == 2 {
		input += "@" + ref
	}
	resolver := remotesource.New(remotesource.Options{
		Root:       r.root,
		Token:      r.options.Token,
		Fetcher:    r.options.Fetcher,
		APIBaseURL: r.options.APIBaseURL,
		UserAgent:  r.options.UserAgent,
	})
	remote, err := resolver.Resolve(ctx, input)
	if err != nil {
		return nil, err
	}

	candidates := remote.Candidates
	if wanted := strings.TrimSpace(opts.Path); wanted != "" {
		filtered := make([]remotesource.Candidate, 0, len(candidates))
		for _, candidate := range candidates {
			if matchSourcePath(candidate.Path, wanted) {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	if len(candidates) == 0 {
		if strings.TrimSpace(opts.Path) != "" {
			return nil, fmt.Errorf("no mcp config matching %q in source", opts.Path)
		}
		return nil, errors.New("source contains no mcp config")
	}
	if len(candidates) > 1 {
		paths := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			paths = append(paths, candidate.Path)
		}
		sort.Strings(paths)
		return nil, fmt.Errorf("source contains multiple files; set path to one of: %s", strings.Join(paths, ", "))
	}

	data, err := remote.Fetch(ctx, candidates[0])
	if err != nil {
		return nil, err
	}
	cfg, err := mcp.ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("invalid mcp config: %w", err)
	}
	return cfg, nil
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

func matchSourcePath(candidate, wanted string) bool {
	candidate = strings.Trim(strings.ReplaceAll(candidate, `\`, "/"), "/")
	wanted = strings.Trim(strings.ReplaceAll(wanted, `\`, "/"), "/")
	if candidate == wanted || strings.HasSuffix(candidate, "/"+wanted) {
		return true
	}
	return path.Base(candidate) == path.Base(wanted)
}

func selectInstallServers(cfg *mcp.Config, names []string) ([]mcp.ServerConfig, error) {
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

func mergeInstallServers(dest *mcp.Config, incoming []mcp.ServerConfig, overwrite bool) ([]string, []string, error) {
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

// Remove deletes one server from the scope that currently provides it, saves
// the config, unregisters its tools, and stops its client.
func (r *MCPRuntime) Remove(name string) error {
	if r == nil {
		return mcp.ErrServerNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: empty name", mcp.ErrServerNotFound)
	}

	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return mcp.ErrClosed
	}
	scope, ok := r.scopes[name]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", mcp.ErrServerNotFound, name)
	}

	configPath, err := r.pathForScope(scope)
	if err != nil {
		return err
	}
	cfg, err := loadOptionalMCPConfig(configPath)
	if err != nil {
		return err
	}
	if err := cfg.RemoveServer(name); err != nil {
		return err
	}
	if err := cfg.Save(configPath); err != nil {
		return err
	}

	r.mu.Lock()
	if scope == MCPScopeUser {
		r.userCfg = cfg
	} else {
		r.projectCfg = cfg
	}
	r.rebuildConfigLocked()
	r.mu.Unlock()

	return r.stopServer(name)
}

// Close stops every client and unregisters its tools. It is idempotent and
// safe to call from the quit path.
func (r *MCPRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	names := make([]string, 0, len(r.clients))
	for name := range r.clients {
		names = append(names, name)
	}
	sort.Strings(names)
	type closing struct {
		client MCPRuntimeClient
		tools  []AgentTool
		defs   []providers.ToolDef
	}
	pending := make([]closing, 0, len(names))
	for _, name := range names {
		pending = append(pending, closing{
			client: r.clients[name],
			tools:  r.agentTools[name],
			defs:   r.providerDefs[name],
		})
	}
	r.clients = map[string]MCPRuntimeClient{}
	r.agentTools = map[string][]AgentTool{}
	r.providerDefs = map[string][]providers.ToolDef{}
	r.errors = map[string]string{}
	r.mu.Unlock()

	var errs []error
	for _, entry := range pending {
		unregisterMCPTools(r.options.Dispatcher, entry.tools, entry.defs)
		if entry.client != nil {
			if err := entry.client.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
