package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	ignore "github.com/sabhiram/go-gitignore"
)

//NOTE: Schema represents a JSON schema describing a tool's input, sent to the LLM so it knows what arguments to provide and in what shape.
type Schema map[string]any

//NOTE: Kind categorizes what a tool does, independent of its name or logic.
// This isn't used by the LLM — it's for the dispatcher/TUI to make (like how to render the results).
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
// means the LLM always receives something it can read — a failed delete
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

//DESC: AgentTool is the contract every tool must satisfy. A tool is anything
// that can describe itself to the LLM  and execute a call from it (Run). 
// Nothing outside this interface should be required to add a new tool.
type AgentTool interface {
	Name() string //Name is the identifier the LLM uses to call this tool (unique across every tool)
	Description() string //Description explains what the tool does and when to use it.
	InputSchema() Schema // InputSchema is the JSON schema describing this tool's expected input
	Kind() Kind // Kind categorizes this tool for dispatcher/UI purposes.

	//NOTE: Run executes the tool with the given raw JSON input.
	// ctx carries cancellation — long-running tools should check
	// The returned error is for failures for the code
	Run(ctx context.Context, input json.RawMessage) (ToolResult, error)
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

