// Package skillstool's skill tool: the model-facing entry point to the
// session's loaded skills. The tool's description carries an
// <available_skills> block listing only the skills the user loaded in the
// session; calling it returns the full SKILL.md content of the requested
// skill.
package skillstool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"GoWork/providers"
	"GoWork/tools"
)

// ToolName is the skill tool's identifier in the wire protocol.
const ToolName = "skill"

// Tool is a stateless agent tool; the manager singleton holds the state.
type Tool struct{}

// New returns the skill tool as an AgentTool.
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

func (t *Tool) InputSchema() json.RawMessage { return ToolSchema() }

// Description reflects the session's loaded skills at call time; the
// description that goes in the wire protocol is rebuilt (ToolDef) whenever
// the loaded set changes.
func (t *Tool) Description() string {
	return ToolDescription()
}

// ToolDescription builds the full skill-tool description: the usage guide
// plus the <available_skills> block of session-loaded skills.
func ToolDescription() string {
	var b strings.Builder
	b.WriteString(`Loads the content of a skill. Skills are reusable instruction sets the user has loaded into this session. When a task would benefit from a skill's knowledge, call this tool with its name to get its full content and follow it.

Available skills you may load (the user's current session selection):
`)
	m := GetManager()
	block := ""
	if m != nil {
		entries := m.Available()
		if len(entries) > 0 {
			var s strings.Builder
			s.WriteString("<available_skills>\n")
			for _, e := range entries {
				s.WriteString("  <skill>\n")
				s.WriteString("    <name>" + e.Name + "</name>\n")
				s.WriteString("    <description>" + e.Description + "</description>\n")
				s.WriteString("  </skill>\n")
			}
			s.WriteString("</available_skills>\n")
			block = s.String()
		}
	}
	if block == "" {
		b.WriteString("<available_skills></available_skills>\n")
		b.WriteString("(no skills are loaded for this session)")
	} else {
		b.WriteString(block)
	}
	return b.String()
}

// ToolDef returns the ToolDef registered with the providers, built from the
// current description. Rebuilding it after every load/unload keeps the
// available-skills list the model sees in sync with the session.
func ToolDef() providers.ToolDef {
	return providers.ToolDef{
		Name:        ToolName,
		Description: ToolDescription(),
		InputSchema: ToolSchema(),
	}
}

// ToolSchema is the static input schema shared by the registered defs.
func ToolSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of a skill from the available_skills list.",
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

// Run reads the requested skill's file content. Only skills loaded in the
// session can be loaded; anything else is an error result the model can
// understand.
func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rawInput, &in); err != nil {
		return tools.ToolResult{}, fmt.Errorf("skill: invalid input: %w", err)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return tools.Errf("skill: missing required field \"name\""), nil
	}

	m := GetManager()
	if m == nil {
		return tools.Errf("skill: skills are not available in this session"), nil
	}
	if !m.IsLoaded(name) {
		loaded := strings.Join(m.LoadedNames(), ", ")
		if loaded == "" {
			loaded = "none"
		}
		return tools.Errf("skill %q is not loaded in this session; loaded skills: %s", name, loaded), nil
	}
	content := m.Content(name)
	if content == "" {
		return tools.Errf("skill %q is loaded but its file could not be read; it may have been removed from disk", name), nil
	}
	return tools.Ok(content), nil
}
