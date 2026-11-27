package gowork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	toolstab "GoWork/Tui/Components/ToolsTab"
	"GoWork/internal/lsp"
	"GoWork/internal/mcp"
	"GoWork/providers"
	"GoWork/tools"
)

// Config files live in the app's own data directory (next to the executable),
// never in the project the user is working on.
const (
	mcpConfigFileName = mcp.DefaultConfigPath
	lspConfigFileName = ".lsp.json"

	// mcpConnectTimeout caps one interactive/startup connection attempt. The
	// per-server mcp timeouts still apply when they are shorter, so a broken
	// server can never stall app startup or the update loop indefinitely.
	mcpConnectTimeout = 10 * time.Second
)

// mcpClient is the subset of mcp.Client the runtime needs. Keeping it an
// interface lets tests drive registration and shutdown without spawning
// servers.
type mcpClient interface {
	Start(ctx context.Context) error
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	CallTool(ctx context.Context, name string, arguments any) (*mcp.CallResult, error)
	Close() error
}

// toolRuntime owns the live MCP clients and the LSP bridge, and keeps the
// configuration files in sync with what the Tools tab changes. It is used from
// the bubbletea update loop (and startup/shutdown), so it deliberately avoids
// its own goroutines.
type toolRuntime struct {
	appData string
	root    string

	mcpPath string
	lspPath string

	mcpCfg     *mcp.Config
	mcpClients map[string]mcpClient
	mcpTools   map[string][]tools.AgentTool
	mcpDefs    map[string][]providers.ToolDef
	mcpErrors  map[string]string
	mcpErr     string

	lspCfg          *lsp.Config
	lspBridge       *tools.LSPBridge
	lspServerErrors map[string]string
	lspErr          string

	// newMCPClient is a test seam; production uses mcp.NewClient.
	newMCPClient func(mcp.ServerConfig) (mcpClient, error)

	closed bool
}

// newToolRuntime builds the runtime for one app run. appData is the directory
// holding .mcp.json/.lsp.json; root is the project root.
func newToolRuntime(appData, root string) *toolRuntime {
	if strings.TrimSpace(appData) == "" {
		appData = "."
	}
	return &toolRuntime{
		appData:         appData,
		root:            root,
		mcpPath:         filepath.Join(appData, mcpConfigFileName),
		lspPath:         filepath.Join(appData, lspConfigFileName),
		mcpCfg:          &mcp.Config{Servers: map[string]mcp.ServerConfig{}},
		mcpClients:      map[string]mcpClient{},
		mcpTools:        map[string][]tools.AgentTool{},
		mcpDefs:         map[string][]providers.ToolDef{},
		mcpErrors:       map[string]string{},
		lspCfg:          lsp.NewConfig(),
		lspServerErrors: map[string]string{},
	}
}

// Load reads both configuration files, starts every enabled MCP server, and
// creates the LSP bridge. It never returns an error: a missing/broken config or
// unavailable server is recorded for the Tools tab and the app keeps running.
func (r *toolRuntime) Load() {
	if r == nil {
		return
	}
	r.loadMCPConfig()
	r.loadLSPConfig()

	for _, server := range r.mcpCfg.EnabledServers() {
		_ = r.connectMCP(server)
	}
	r.restartLSPBridge()
}

func (r *toolRuntime) loadMCPConfig() {
	data, err := os.ReadFile(r.mcpPath)
	if err != nil {
		if !os.IsNotExist(err) {
			r.mcpErr = fmt.Sprintf("could not read %s: %v", r.mcpPath, err)
		}
		return
	}
	cfg, err := mcp.ParseConfig(data)
	if err != nil {
		r.mcpErr = err.Error()
		return
	}
	if cfg == nil {
		return
	}
	r.mcpCfg = cfg
}

func (r *toolRuntime) loadLSPConfig() {
	data, err := os.ReadFile(r.lspPath)
	if err != nil {
		if !os.IsNotExist(err) {
			r.lspErr = fmt.Sprintf("could not read %s: %v", r.lspPath, err)
		}
		return
	}
	cfg, err := lsp.ParseConfig(data)
	if err != nil {
		r.lspErr = err.Error()
		return
	}
	if cfg == nil {
		return
	}
	r.lspCfg = cfg
}

// connectMCP starts one server, discovers its tools, and stores the bridges.
// Failures are recorded per server and returned so the caller can surface them;
// the configured entry stays in place for a later retry.
func (r *toolRuntime) connectMCP(server mcp.ServerConfig) error {
	name := server.Name
	if strings.TrimSpace(name) == "" {
		name = server.Command
		if name == "" {
			name = server.URL
		}
	}
	r.closeMCPClient(name)

	newClient := r.newMCPClient
	if newClient == nil {
		newClient = func(cfg mcp.ServerConfig) (mcpClient, error) {
			return mcp.NewClient(cfg)
		}
	}
	client, err := newClient(server)
	if err != nil {
		r.mcpErrors[name] = err.Error()
		return err
	}

	startTimeout := time.Duration(server.StartupTimeout)
	if startTimeout <= 0 {
		startTimeout = mcp.DefaultStartupTimeout
	}
	if startTimeout > mcpConnectTimeout {
		startTimeout = mcpConnectTimeout
	}
	startCtx, cancel := context.WithTimeout(context.Background(), startTimeout)
	err = client.Start(startCtx)
	cancel()
	if err != nil {
		_ = client.Close()
		r.mcpErrors[name] = err.Error()
		return err
	}

	listTimeout := time.Duration(server.RequestTimeout)
	if listTimeout <= 0 {
		listTimeout = mcp.DefaultRequestTimeout
	}
	if listTimeout > mcpConnectTimeout {
		listTimeout = mcpConnectTimeout
	}
	listCtx, cancel := context.WithTimeout(context.Background(), listTimeout)
	definitions, err := client.ListTools(listCtx)
	cancel()
	if err != nil {
		_ = client.Close()
		r.mcpErrors[name] = err.Error()
		return err
	}

	agentTools, providerDefs, err := tools.MCPTools(name, client, definitions)
	if err != nil {
		_ = client.Close()
		r.mcpErrors[name] = err.Error()
		return err
	}

	r.mcpClients[name] = client
	r.mcpTools[name] = agentTools
	r.mcpDefs[name] = providerDefs
	delete(r.mcpErrors, name)
	return nil
}

// restartLSPBridge closes the old bridge (stopping every cached language
// server process) and creates a fresh one over the current configuration.
func (r *toolRuntime) restartLSPBridge() {
	if r.lspBridge != nil {
		_ = r.lspBridge.Close()
		r.lspBridge = nil
	}
	if r.lspCfg == nil {
		return
	}
	r.validateLSPServers()
	for _, server := range r.lspCfg.List() {
		if server.Enabled {
			r.lspBridge = tools.NewLSPBridge(r.lspCfg)
			return
		}
	}
}

// validateLSPServers records enabled servers whose executable is missing so
// the Tools tab can show the problem before the first tool call. Language
// server processes still start lazily.
func (r *toolRuntime) validateLSPServers() {
	if r == nil || r.lspCfg == nil {
		return
	}
	for _, server := range r.lspCfg.List() {
		if !server.Enabled {
			delete(r.lspServerErrors, server.Name)
			continue
		}
		if _, err := exec.LookPath(server.Command); err != nil {
			r.lspServerErrors[server.Name] = fmt.Sprintf("command %q not found on PATH", server.Command)
		} else {
			delete(r.lspServerErrors, server.Name)
		}
	}
}

// Tools returns every runtime-provided agent tool: MCP bridges in server-name
// order followed by the LSP tools when at least one language server is enabled.
func (r *toolRuntime) Tools() []tools.AgentTool {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.mcpTools))
	for name := range r.mcpTools {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []tools.AgentTool
	for _, name := range names {
		out = append(out, r.mcpTools[name]...)
	}
	if r.lspBridge != nil {
		out = append(out, tools.NewLSPTools(r.lspBridge)...)
	}
	return out
}

// ToolDefs returns provider definitions matching Tools(), in the same order.
func (r *toolRuntime) ToolDefs() []providers.ToolDef {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.mcpDefs))
	for name := range r.mcpDefs {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []providers.ToolDef
	for _, name := range names {
		out = append(out, r.mcpDefs[name]...)
	}
	if r.lspBridge != nil {
		for _, tool := range tools.NewLSPTools(r.lspBridge) {
			out = append(out, providers.ToolDef{
				Name:        tool.Name(),
				Description: tool.Description(),
				InputSchema: tool.InputSchema(),
			})
		}
	}
	return out
}

// MCPServers returns the Tools-tab snapshot of configured MCP servers.
func (r *toolRuntime) MCPServers() []toolstab.MCPServer {
	if r == nil || r.mcpCfg == nil {
		return nil
	}
	names := r.mcpCfg.Names()
	out := make([]toolstab.MCPServer, 0, len(names))
	for _, name := range names {
		server, _ := r.mcpCfg.Server(name)
		transport := string(server.TransportKind())
		if transport == "" {
			transport = "stdio"
		}
		if transport == string(mcp.TransportAuto) {
			transport = "http"
		}
		out = append(out, toolstab.MCPServer{
			Name:      name,
			Transport: transport,
			Enabled:   server.IsEnabled(),
			Connected: r.mcpClients[name] != nil,
			Tools:     len(r.mcpTools[name]),
			Error:     r.mcpErrors[name],
		})
	}
	return out
}

// LSPServers returns the Tools-tab snapshot of configured language servers.
func (r *toolRuntime) LSPServers() []toolstab.LSPServer {
	if r == nil || r.lspCfg == nil {
		return nil
	}
	servers := r.lspCfg.List()
	out := make([]toolstab.LSPServer, 0, len(servers))
	for _, server := range servers {
		out = append(out, toolstab.LSPServer{
			Name:      server.Name,
			Languages: append([]string(nil), server.LanguageIDs...),
			Enabled:   server.Enabled,
			Error:     r.lspServerErrors[server.Name],
		})
	}
	return out
}

// ConfigPaths returns the two persisted configuration locations.
func (r *toolRuntime) ConfigPaths() (mcpPath, lspPath string) {
	if r == nil {
		return "", ""
	}
	return r.mcpPath, r.lspPath
}

// Errors returns the last config-level error for each subsystem.
func (r *toolRuntime) Errors() (mcpErr, lspErr string) {
	if r == nil {
		return "", ""
	}
	return r.mcpErr, r.lspErr
}

// SetMCPEnabled enables/disables a server, persists the change, and starts or
// stops its client accordingly.
func (r *toolRuntime) SetMCPEnabled(name string, enabled bool) error {
	if r == nil || r.mcpCfg == nil {
		return errors.New("MCP configuration is unavailable")
	}
	server, ok := r.mcpCfg.Server(name)
	if !ok {
		return fmt.Errorf("%w: %q", mcp.ErrServerNotFound, name)
	}
	if err := r.mcpCfg.SetServerEnabled(name, enabled); err != nil {
		return err
	}
	if err := r.saveMCPConfig(); err != nil {
		r.mcpCfg.Servers[name] = server
		return err
	}
	if !enabled {
		r.closeMCPClient(name)
		delete(r.mcpErrors, name)
		return nil
	}
	server, _ = r.mcpCfg.Server(name)
	return r.connectMCP(server)
}

// AddMCP validates, persists, and starts a new MCP server.
func (r *toolRuntime) AddMCP(msg toolstab.AddMCPMsg) error {
	if r == nil || r.mcpCfg == nil {
		return errors.New("MCP configuration is unavailable")
	}
	server := mcp.ServerConfig{
		Name:    strings.TrimSpace(msg.Name),
		Command: strings.TrimSpace(msg.Command),
		Args:    append([]string(nil), msg.Args...),
		URL:     strings.TrimSpace(msg.URL),
	}
	if err := r.mcpCfg.AddServer(server); err != nil {
		r.mcpErr = err.Error()
		return err
	}
	if err := r.saveMCPConfig(); err != nil {
		_ = r.mcpCfg.RemoveServer(server.Name)
		return err
	}
	server, _ = r.mcpCfg.Server(server.Name)
	if err := r.connectMCP(server); err != nil {
		return fmt.Errorf("server %q was added but could not start: %w", server.Name, err)
	}
	return nil
}

// RemoveMCP deletes a server after stopping its client, and persists the
// change.
func (r *toolRuntime) RemoveMCP(name string) error {
	if r == nil || r.mcpCfg == nil {
		return fmt.Errorf("%w: %q", mcp.ErrServerNotFound, name)
	}
	server, ok := r.mcpCfg.Server(name)
	if !ok {
		return fmt.Errorf("%w: %q", mcp.ErrServerNotFound, name)
	}
	if err := r.mcpCfg.RemoveServer(name); err != nil {
		return err
	}
	if err := r.saveMCPConfig(); err != nil {
		r.mcpCfg.Servers[name] = server
		return err
	}
	r.closeMCPClient(name)
	delete(r.mcpErrors, name)
	return nil
}

// SetLSPEnabled persists an enabled toggle and refreshes the bridge. Existing
// language-server processes are stopped so a disabled server cannot keep
// running in the background.
func (r *toolRuntime) SetLSPEnabled(name string, enabled bool) error {
	if r == nil || r.lspCfg == nil {
		return errors.New("LSP configuration is unavailable")
	}
	if err := r.lspCfg.SetEnabled(name, enabled); err != nil {
		return err
	}
	if err := r.saveLSPConfig(); err != nil {
		_ = r.lspCfg.SetEnabled(name, !enabled)
		return err
	}
	delete(r.lspServerErrors, name)
	r.restartLSPBridge()
	return nil
}

// AddLSP validates, persists, and enables a new language server.
func (r *toolRuntime) AddLSP(msg toolstab.AddLSPMsg) error {
	if r == nil || r.lspCfg == nil {
		return errors.New("LSP configuration is unavailable")
	}
	server := lsp.ServerConfig{
		Name:        strings.TrimSpace(msg.Name),
		Command:     strings.TrimSpace(msg.Command),
		Args:        append([]string(nil), msg.Args...),
		LanguageIDs: append([]string(nil), msg.Languages...),
		Enabled:     true,
	}
	if err := r.lspCfg.Add(server); err != nil {
		r.lspServerErrors[server.Name] = err.Error()
		r.lspErr = err.Error()
		return err
	}
	if err := r.saveLSPConfig(); err != nil {
		_ = r.lspCfg.Remove(server.Name)
		return err
	}
	r.restartLSPBridge()
	return nil
}

// RemoveLSP deletes a language server and refreshes the bridge.
func (r *toolRuntime) RemoveLSP(name string) error {
	if r == nil || r.lspCfg == nil {
		return fmt.Errorf("%w: %q", lsp.ErrServerNotFound, name)
	}
	server, ok := r.lspCfg.Get(name)
	if !ok {
		return fmt.Errorf("%w: %q", lsp.ErrServerNotFound, name)
	}
	if err := r.lspCfg.Remove(name); err != nil {
		return err
	}
	if err := r.saveLSPConfig(); err != nil {
		_ = r.lspCfg.Add(server)
		return err
	}
	delete(r.lspServerErrors, name)
	r.restartLSPBridge()
	return nil
}

func (r *toolRuntime) saveMCPConfig() error {
	if r == nil || r.mcpCfg == nil {
		return errors.New("MCP configuration is unavailable")
	}
	if err := r.mcpCfg.Save(r.mcpPath); err != nil {
		r.mcpErr = err.Error()
		return err
	}
	r.mcpErr = ""
	return nil
}

func (r *toolRuntime) saveLSPConfig() error {
	if r == nil || r.lspCfg == nil {
		return errors.New("LSP configuration is unavailable")
	}
	servers := r.lspCfg.List()
	if servers == nil {
		servers = []lsp.ServerConfig{}
	}
	data, err := json.MarshalIndent(struct {
		Servers []lsp.ServerConfig `json:"servers"`
	}{Servers: servers}, "", "  ")
	if err != nil {
		r.lspErr = err.Error()
		return fmt.Errorf("lsp: encode config: %w", err)
	}
	data = append(data, '\n')
	if err := writeConfigFile(r.lspPath, data); err != nil {
		r.lspErr = err.Error()
		return err
	}
	r.lspErr = ""
	return nil
}

// writeConfigFile atomically replaces path with data, creating its directory
// with private permissions.
func writeConfigFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("config path must not be empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	keep := true
	defer func() {
		if keep {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	keep = false
	return nil
}

func (r *toolRuntime) closeMCPClient(name string) {
	if r == nil {
		return
	}
	if client := r.mcpClients[name]; client != nil {
		_ = client.Close()
	}
	delete(r.mcpClients, name)
	delete(r.mcpTools, name)
	delete(r.mcpDefs, name)
}

// Servers implements the app's LSPManager seam for /lsp.
func (r *toolRuntime) Servers() []LSPServer {
	if r == nil || r.lspCfg == nil {
		return nil
	}
	servers := r.lspCfg.List()
	out := make([]LSPServer, 0, len(servers))
	for _, server := range servers {
		status := "disabled"
		if server.Enabled {
			status = "enabled"
		}
		if detail := r.lspServerErrors[server.Name]; detail != "" {
			status += " · " + detail
		}
		out = append(out, LSPServer{
			Name:     server.Name,
			Language: strings.Join(server.LanguageIDs, ", "),
			Status:   status,
		})
	}
	return out
}

// Configure implements /lsp configure: it enables an already configured
// server, or adds a server detected on PATH.
func (r *toolRuntime) Configure(name string) error {
	if r == nil || r.lspCfg == nil {
		return errors.New("LSP configuration is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("server name is required")
	}
	if server, ok := r.lspCfg.Get(name); ok {
		if err := r.lspCfg.SetEnabled(server.Name, true); err != nil {
			return err
		}
		if err := r.saveLSPConfig(); err != nil {
			return err
		}
		r.restartLSPBridge()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, detected := range lsp.Detect(ctx) {
		if !strings.EqualFold(detected.Name, name) {
			continue
		}
		server := lsp.ServerConfig{
			Name:        detected.Name,
			Command:     detected.Path,
			LanguageIDs: append([]string(nil), detected.LanguageIDs...),
			Enabled:     true,
		}
		if err := r.lspCfg.Add(server); err != nil {
			return err
		}
		if err := r.saveLSPConfig(); err != nil {
			_ = r.lspCfg.Remove(server.Name)
			return err
		}
		r.restartLSPBridge()
		return nil
	}
	return fmt.Errorf("no configured language server %q and no installed server with that name was found", name)
}

// Close stops every MCP client and the LSP bridge. It is idempotent and safe
// to call from the quit filter.
func (r *toolRuntime) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	var errs []error
	for name, client := range r.mcpClients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("stop MCP server %q: %w", name, err))
		}
	}
	r.mcpClients = map[string]mcpClient{}
	r.mcpTools = map[string][]tools.AgentTool{}
	r.mcpDefs = map[string][]providers.ToolDef{}
	if r.lspBridge != nil {
		if err := r.lspBridge.Close(); err != nil {
			errs = append(errs, fmt.Errorf("stop language servers: %w", err))
		}
		r.lspBridge = nil
	}
	return errors.Join(errs...)
}
