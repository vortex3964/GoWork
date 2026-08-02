//DESC: the interface every tool must follow
//plus things multiple tools would like to have access to

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"os"
	"path/filepath"
	"sync"
	ignore "github.com/sabhiram/go-gitignore"
	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/providers"
)

//NOTE: old ReadState/Cache code is removed (Cache is wrong).
// it needs a rewrite after more of the app is made is you wanna see old logic.

//NOTE: Kind categorizes what a tool does, independent of its name or logic.
// This isn't used by the LLM it's for the dispatcher/TUI to make (like how to render the results).
type Kind int

const (
	KindRead Kind = iota
	KindWrite
	KindDelete
	KindSearch
	KindWebSearch
	KindExec
)

func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	case KindDelete:
		return "delete"
	case KindExec:
		return "exec"
	case KindWebSearch:
		return "web"
	case KindSearch:
		return "search"
	default:
		return "unknown"
	}
}

//IMPORTANT: ToolResult is what every tool returns on a completed run, whether the
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

//DESC: Errf builds a failed ToolResult with a formatted message. 
//NOTE:Use this for expected failures the model should see and can act on.
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
	p providers.Provider
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

//DESC: DispatchArgs holds run-wide context that's the same for every tool call.
// Built once by the dispatcher at startup and passed into every Run call.
type DispatchArgs struct {
	Root *os.Root
	RootPath string
	WatchList *cl.WatchList
	Provider ProviderGetter
}

func InitDispatchArgs(projectRoot string , wl *cl.WatchList , getProvider ProviderGetter) (DispatchArgs , error) {
	
	if projectRoot == "" {
		return DispatchArgs{} , fmt.Errorf("projectRoot cant be empty")
	}

	abs , err := filepath.Abs(projectRoot)

	if err != nil {
		return DispatchArgs{} , fmt.Errorf("resolving project root:%w",err)
	}

	root , err := os.OpenRoot(abs)

	if err != nil {
		return DispatchArgs{} , fmt.Errorf("opening project root failed:%w" , err)
	}

	return DispatchArgs{Root: root , RootPath: abs , WatchList: wl , Provider: getProvider} , nil
}

//DESC: AgentTool is the contract every tool must satisfy. A tool is anything
// that can describe itself to the LLM  and execute a call from it (Run). 
// Nothing outside this interface should be required to add a new tool.
// each tool will have a tool struct even if its empty just to implement the interface
// or the dispatcher wont call it
type AgentTool interface {
	Name() string //Name is the identifier the LLM uses to call this tool (unique across every tool)
	Description() string //Description explains what the tool does and when to use it.
	InputSchema() json.RawMessage // InputSchema is the JSON schema describing this tool's expected input
	Kind() Kind // Kind categorizes this tool for dispatcher/UI purposes.

	//NOTE: Run executes the tool with the given raw JSON input.
	// ctx carries cancellation long-running tools should check
	// The returned error is for failures for the code
	Run(ctx context.Context, args DispatchArgs , input json.RawMessage) (ToolResult, error)
}

//code for the dispatcher
type Dispatcher struct {
	tools map[string]AgentTool
	args DispatchArgs
}

type ToolUse struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func InitDispacher(projectRoot string , wl *cl.WatchList , getProvider ProviderGetter ,tools ... AgentTool) ( *Dispatcher , error ) {
	args , err := InitDispatchArgs(projectRoot,wl,getProvider)

	if err != nil {
		return nil , err
	}

	tool_map := make(map[string]AgentTool , len(tools))

	for _ , tool := range tools {
		//set the tool objs to the map so they can be called
		tool_map[tool.Name()] = tool
	}


	return  &Dispatcher{tools: tool_map , args: args } , nil
}

//NOTE:it is the providers class job to parse the json
//from the llm and actually call dispatch (plus init the tool use struct)
func (d *Dispatcher) Dispach(ctx context.Context , tu ToolUse )  ToolResult {
	tool , ok := d.tools[tu.Name]

	if !ok {
		return Errf("unknown tool:%q",tu.Name)
	}

	res , err := tool.Run(ctx , d.args , tu.Input)

	if err != nil {
		return Errf("tool %v failed:%s" , tu.Name , err)
	}

	return res
}

// LoadIgnores loads .gitignore and .agentignore from root, if present.
// Shared across any tool that needs to skip ignored paths
func LoadIgnores(root string) []*ignore.GitIgnore {
	var ignores []*ignore.GitIgnore

	if gi, err := ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore")); err == nil {
		ignores = append(ignores, gi)
	}
	if ai, err := ignore.CompileIgnoreFile(filepath.Join(root, ".agentignore")); err == nil {
		ignores = append(ignores, ai)
	}

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
