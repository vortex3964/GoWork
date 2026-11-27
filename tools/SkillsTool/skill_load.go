package skillstool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"GoWork/tools"
)

// LoadToolName is the model-facing identifier of the session load tool.
const LoadToolName = "skill_load"

// LoadTool lists, loads and unloads installed skills in the current session.
// It only changes session state; it never reads from the network.
type LoadTool struct{}

// NewLoadTool returns the skill_load tool.
func NewLoadTool() tools.AgentTool { return &LoadTool{} }

func (t *LoadTool) Name() string { return LoadToolName }

func (t *LoadTool) Kind() tools.Kind { return tools.KindAllowed }

func (t *LoadTool) Description() string {
	return `Lists, loads or unloads skills for this session without downloading or writing files. Use action "list" to see every installed skill and whether it is loaded. Loading adds a skill to the skill tool's available_skills block for the next model turn; unloading removes it from this session only. Use this after skill_install when load was not requested, or to load a persisted skill in a new session.`
}

func (t *LoadTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "load", "unload"},
				"description": "Session action. Defaults to list.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name for load and unload.",
			},
		},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *LoadTool) Run(_ context.Context, _ tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var in struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(rawInput, &in); err != nil {
		return tools.ToolResult{}, fmt.Errorf("skill_load: invalid input: %w", err)
	}

	m := GetManager()
	if m == nil {
		return tools.Errf("skill_load: skills are not available in this session"), nil
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		action = "list"
	}
	name := strings.TrimSpace(in.Name)

	switch action {
	case "list":
		entries := m.Discover()
		if len(entries) == 0 {
			return tools.Ok("no skills are installed"), nil
		}
		loaded := make(map[string]bool)
		for _, loadedName := range m.LoadedNames() {
			loaded[loadedName] = true
		}
		var b strings.Builder
		b.WriteString("installed skills:\n")
		for _, entry := range entries {
			state := "available"
			if loaded[entry.Name] {
				state = "loaded"
			}
			b.WriteString(fmt.Sprintf("- %s (%s): %s\n", entry.Name, state, entry.Description))
		}
		return tools.Ok(strings.TrimRight(b.String(), "\n")), nil

	case "load":
		if name == "" {
			return tools.Errf("skill_load: action \"load\" requires a name"), nil
		}
		if !m.Load(name) {
			return tools.Errf("skill_load: no installed skill named %q\nnext call: skill_load {\"action\":\"list\"} to see installed names, or skill_install to install it first", name), nil
		}
		rebuildSkillToolDef()
		return tools.Ok(fmt.Sprintf("loaded skill %q for this session", name)), nil

	case "unload":
		if name == "" {
			return tools.Errf("skill_load: action \"unload\" requires a name"), nil
		}
		if !m.IsLoaded(name) {
			return tools.Errf("skill_load: skill %q is not loaded in this session", name), nil
		}
		m.Unload(name)
		rebuildSkillToolDef()
		return tools.Ok(fmt.Sprintf("unloaded skill %q from this session", name)), nil

	default:
		return tools.Errf("skill_load: unknown action %q; use list, load or unload", in.Action), nil
	}
}
