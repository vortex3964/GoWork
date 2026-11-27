//DESC: the interface every tool must follow
//plus things multiple tools would like to have access to

package tools

import (
	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/providers"
	"context"
	"encoding/json"
	"fmt"
	ignore "github.com/sabhiram/go-gitignore"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//NOTE: old ReadState/Cache code is removed (Cache is wrong).
// it needs a rewrite after more of the app is made is you wanna see old logic.

// NOTE: Kind categorizes what a tool does, independent of its name or logic.
// This isn't used by the LLM it's for the dispatcher/TUI to make (like how to render the results).
type Kind int

const (
	KindNotAllowed Kind = iota
	KindAllowed
)

// IMPORTANT: ToolResult is what every tool returns on a completed run, whether the
// underlying operation succeeded or failed. Using one type for both cases
// means the LLM always receives something it can read a failed delete
// and a successful delete look the same shape, just with IsError flipped.
type ToolResult struct {
	Content string
	IsError bool
}

// Ok builds a successful ToolResult.
func Ok(content string) ToolResult {
	return ToolResult{Content: content}
}

// DESC: Errf builds a failed ToolResult with a formatted message.
// NOTE:Use this for expected failures the model should see and can act on.
func Errf(format string, args ...any) ToolResult {
	return ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// ProviderGetter returns whichever Provider is currently used
// used for tools that directly need to talk to the llm (specificaly web fetch tool)
type ProviderGetter func() providers.Provider

// ProviderHolder is the single, concurrency-safe source of truth for "the
// currently selected provider."
type ProviderHolder struct {
	mu sync.RWMutex
	p  providers.Provider
}

func (h *ProviderHolder) Get() providers.Provider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.p
}

func (h *ProviderHolder) Set(p providers.Provider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.p = p
}

// DESC: DispatchArgs holds run-wide context that's the same for every tool call.
// Built once by the dispatcher at startup and passed into every Run call.
type DispatchArgs struct {
	Root *os.Root
	// RootPath points at the dispatcher's root string: the single source of
	// truth for the absolute project root. Everything that needs the root
	// shares this pointer, so changing the root means changing one string.
	RootPath  *string
	WatchList *cl.WatchList
	Provider  ProviderGetter
	// ReadState tracks the last modification time the agent saw for each
	// file it has read or written. Write tools use it to refuse editing a
	// file that changed on disk since the agent last saw it, so the model
	// can't blind-clobber newer external edits.
	ReadState *ReadState
}

// ReadState is a small, concurrency-safe map of clean relative path -> the
// file's mod time when the agent last read or wrote it. Write tools consult
// it before overwriting so we fail (and tell the model to re-read) when the
// file changed underneath us since we last saw it.
type ReadState struct {
	mu    sync.Mutex
	times map[string]time.Time
}

func NewReadState() *ReadState {
	return &ReadState{times: make(map[string]time.Time)}
}

// Record updates the recorded mod time for path.
func (r *ReadState) Record(path string, t time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.times[path] = t
}

// Forget drops a path's record (used when a file is moved or deleted).
func (r *ReadState) Forget(path string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.times, path)
}

// SeenAt returns the mod time recorded for path, or the zero time if it was
// never seen.
func (r *ReadState) SeenAt(path string) (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.times[path]
	return t, ok
}

func InitDispatchArgs(projectRoot string, wl *cl.WatchList, getProvider ProviderGetter) (DispatchArgs, error) {
	if projectRoot == "" {
		return DispatchArgs{}, fmt.Errorf("projectRoot cant be empty")
	}

	abs, err := filepath.Abs(projectRoot)

	if err != nil {
		return DispatchArgs{}, fmt.Errorf("resolving project root:%w", err)
	}

	root, err := os.OpenRoot(abs)

	if err != nil {
		return DispatchArgs{}, fmt.Errorf("opening project root failed:%w", err)
	}

	// rootPath is the dispatcher's root string. Its address is shared by
	// DispatchArgs.RootPath and the watch list, so it can be updated in one
	// place. It escapes to the heap so the pointer stays valid.
	rootPath := abs

	if wl != nil {
		wl.SetRoot(&rootPath)
	}

	return DispatchArgs{Root: root, RootPath: &rootPath, WatchList: wl, Provider: getProvider, ReadState: NewReadState()}, nil
}

// DESC: AgentTool is the contract every tool must satisfy. A tool is anything
// that can describe itself to the LLM  and execute a call from it (Run).
// Nothing outside this interface should be required to add a new tool.
// each tool will have a tool struct even if its empty just to implement the interface
// or the dispatcher wont call it
type AgentTool interface {
	Name() string                 //Name is the identifier the LLM uses to call this tool (unique across every tool)
	Description() string          //Description explains what the tool does and when to use it.
	InputSchema() json.RawMessage // InputSchema is the JSON schema describing this tool's expected input
	Kind() Kind                   // Kind categorizes this tool for dispatcher/UI purposes.

	//NOTE: Run executes the tool with the given raw JSON input.
	// ctx carries cancellation long-running tools should check
	// The returned error is for failures for the code
	Run(ctx context.Context, args DispatchArgs, input json.RawMessage) (ToolResult, error)
}

// code for the dispatcher
type Dispatcher struct {
	// mu guards tools. MCP and LSP runtimes register and unregister tools while
	// the agent loop dispatches calls, so every access goes through the lock.
	mu    sync.RWMutex
	tools map[string]AgentTool
	args  DispatchArgs
	// planMode turns the dispatcher read-only: mutating calls are refused
	// with a model-readable result while reads keep working. Atomic because
	// tool batches run on worker goroutines while the TUI can flip the mode.
	planMode atomic.Bool
}

type ToolUse struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// planModeReadOnlyTools is the positive allowlist for plan (read-only) mode.
// The gate is default-deny, so a newly added tool stays blocked until it is
// deliberately classified here.
var planModeReadOnlyTools = map[string]struct{}{
	"read_file":      {},
	"grep_search":    {},
	"file_search":    {},
	"list_directory": {},
	"skill":          {},
	// skill_load manages the session's loaded-skill set (list/load/unload)
	// without touching the workspace, so it stays available for planning.
	"skill_load": {},
	// web_search only queries the web; it does not touch the workspace.
	"web_search": {},
}

// PlanModeAllows reports whether a tool call is safe to run while plan mode is
// active. Read-only tools are allowed, todo_list is allowed only for its
// non-mutating "list" action, and every unknown name is treated as mutating
// (default deny). web_fetch is blocked because long pages are persisted under
// the project root, bash is blocked because it can change anything, and
// questions_tool is blocked because it mutates the session's questionnaire
// state and parks the turn.
func PlanModeAllows(name string, input json.RawMessage) bool {
	name = strings.ToLower(strings.TrimSpace(name))

	if name == "todo_list" {
		var in struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(in.Action), "list")
	}

	_, ok := planModeReadOnlyTools[name]
	return ok
}

// SetPlanMode switches the dispatcher between normal operation and read-only
// plan mode. Safe to call while tool batches are running.
func (d *Dispatcher) SetPlanMode(on bool) {
	if d == nil {
		return
	}
	d.planMode.Store(on)
}

// PlanMode reports whether read-only plan mode is enabled.
func (d *Dispatcher) PlanMode() bool {
	if d == nil {
		return false
	}
	return d.planMode.Load()
}

func InitDispacher(projectRoot string, wl *cl.WatchList, getProvider ProviderGetter, tools ...AgentTool) (*Dispatcher, error) {
	args, err := InitDispatchArgs(projectRoot, wl, getProvider)

	if err != nil {
		return nil, err
	}

	tool_map := make(map[string]AgentTool, len(tools))

	for _, tool := range tools {
		//set the tool objs to the map so they can be called
		tool_map[tool.Name()] = tool
	}

	return &Dispatcher{tools: tool_map, args: args}, nil
}

// Register adds a tool to the live registry, replacing a tool with the same
// name. It is safe to call while other goroutines dispatch or unregister.
func (d *Dispatcher) Register(tool AgentTool) error {
	if d == nil {
		return fmt.Errorf("dispatcher is nil")
	}
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := strings.TrimSpace(tool.Name())
	if name == "" {
		return fmt.Errorf("tool name must not be empty")
	}
	d.mu.Lock()
	if d.tools == nil {
		d.tools = make(map[string]AgentTool)
	}
	d.tools[name] = tool
	d.mu.Unlock()
	return nil
}

// Unregister removes a tool by name. It reports whether the tool existed.
func (d *Dispatcher) Unregister(name string) bool {
	if d == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	d.mu.Lock()
	_, existed := d.tools[name]
	delete(d.tools, name)
	d.mu.Unlock()
	return existed
}

// Tools returns a snapshot of the registered tools sorted by name.
func (d *Dispatcher) Tools() []AgentTool {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	out := make([]AgentTool, 0, len(d.tools))
	for _, tool := range d.tools {
		out = append(out, tool)
	}
	d.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// NOTE:it is the providers class job to parse the json
// from the llm and actually call dispatch (plus init the tool use struct)
func (d *Dispatcher) Dispach(ctx context.Context, tu ToolUse) ToolResult {
	d.mu.RLock()
	tool, ok := d.tools[tu.Name]
	d.mu.RUnlock()

	if !ok {
		return Errf("unknown tool:%q", tu.Name)
	}

	// Plan mode is enforced here, at the single choke point every tool call
	// passes through, rather than in individual tools. The model gets a
	// normal tool result explaining the refusal so it can pivot to read-only
	// investigation instead of silently losing the call.
	if d.planMode.Load() && !PlanModeAllows(tu.Name, tu.Input) {
		return Errf(">**PLAN MODE** Tool `%s` is blocked because plan mode is read-only and this tool can modify the workspace. No changes were made. Use read_file, grep_search, file_search or list_directory to inspect the code and present a plan, or ask the user to leave plan mode (ctrl+shift+p) before applying changes.", tu.Name)
	}

	res, err := tool.Run(ctx, d.args, tu.Input)

	if err != nil {
		return Errf("tool %v failed:%s", tu.Name, err)
	}

	return res
}

func (d *Dispatcher) Lookup(name string) (AgentTool, bool) {
	if d == nil {
		return nil, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	tool, ok := d.tools[name]
	return tool, ok
}

// ignoreFileState is the cheap fingerprint used to decide whether a compiled
// ignore file is still current. Missing files get the zero value.
type ignoreFileState struct {
	modTime time.Time
	size    int64
	present bool
}

func ignoreFileStateAt(path string) ignoreFileState {
	info, err := os.Stat(path)
	if err != nil {
		return ignoreFileState{}
	}
	return ignoreFileState{modTime: info.ModTime(), size: info.Size(), present: true}
}

type ignoreCacheEntry struct {
	gitignore   ignoreFileState
	agentignore ignoreFileState
	ignores     []*ignore.GitIgnore
}

// ignoreCache keeps compiled .gitignore/.agentignore matchers per cleaned
// root. Compiling the patterns is the expensive part of listing a directory,
// and the files rarely change, so we only recompile when their mtime or size
// changes (or when one appears/disappears).
var ignoreCache = struct {
	sync.Mutex
	entries map[string]ignoreCacheEntry
}{entries: make(map[string]ignoreCacheEntry)}

// LoadIgnores loads .gitignore and .agentignore from root, if present.
// Shared across any tool that needs to skip ignored paths.
// The compiled matchers are cached per root and refreshed automatically when
// either ignore file changes.
func LoadIgnores(root string) []*ignore.GitIgnore {
	root = filepath.Clean(root)
	gitignorePath := filepath.Join(root, ".gitignore")
	agentignorePath := filepath.Join(root, ".agentignore")
	gitignoreState := ignoreFileStateAt(gitignorePath)
	agentignoreState := ignoreFileStateAt(agentignorePath)

	ignoreCache.Lock()
	entry, ok := ignoreCache.entries[root]
	if ok && entry.gitignore == gitignoreState && entry.agentignore == agentignoreState {
		ignoreCache.Unlock()
		return entry.ignores
	}
	ignoreCache.Unlock()

	var ignores []*ignore.GitIgnore
	if gitignoreState.present {
		if gi, err := ignore.CompileIgnoreFile(gitignorePath); err == nil {
			ignores = append(ignores, gi)
		}
	}
	if agentignoreState.present {
		if ai, err := ignore.CompileIgnoreFile(agentignorePath); err == nil {
			ignores = append(ignores, ai)
		}
	}

	ignoreCache.Lock()
	ignoreCache.entries[root] = ignoreCacheEntry{
		gitignore:   gitignoreState,
		agentignore: agentignoreState,
		ignores:     ignores,
	}
	ignoreCache.Unlock()

	return ignores
}

// IsIgnored reports whether rel (a path relative to the same root passed to
// LoadIgnores) matches any loaded ignore pattern.
func IsIgnored(ignores []*ignore.GitIgnore, rel string) bool {
	for _, ig := range ignores {
		if ig.MatchesPath(rel) {
			return true
		}
	}
	return false
}

// PathKey normalizes a tool-provided relative path into the key used for the
// ReadState tracker, so read and write tools agree even when one receives
// "./src/foo.go" and the other "src/foo.go".
func (d DispatchArgs) PathKey(path string) string {
	return filepath.Clean(path)
}

// MkdirAllInRoot creates dir and all necessary parent directories within
// root, sandboxed the same way every other operation through root is
// symlink escapes and ".." traversal are rejected by the OS itself.
func MkdirAllInRoot(root *os.Root, dir string) error {
	if filepath.IsAbs(dir) {
		return fmt.Errorf("absolute paths are not allowed: %s", dir)
	}

	dir = filepath.Clean(dir)
	if dir == "." || dir == "" {
		return nil
	}

	var cur string
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if err := root.Mkdir(cur, 0755); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}
