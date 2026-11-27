package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	gowork "GoWork/internal/gowork"
	"GoWork/tools"
)

func ctrlShiftPKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModShift}
}

func escKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEsc}
}

func TestPlanModeToggleAndStatusline(t *testing.T) {
	m := newModeTestModel()
	if m.PlanModeActive() {
		t.Fatal("plan mode should start off")
	}
	if strings.Contains(m.StatusLineText(), "PLAN") {
		t.Fatal("statusline shows PLAN while plan mode is off")
	}

	updated, _ := m.Update(ctrlShiftPKey())
	m = updated.(gowork.Model)
	if !m.PlanModeActive() {
		t.Fatal("ctrl+shift+p did not enable plan mode")
	}
	if !strings.Contains(m.StatusLineText(), "PLAN") {
		t.Fatalf("statusline missing PLAN indicator: %q", m.StatusLineText())
	}

	// The same keybind leaves plan mode again.
	updated, _ = m.Update(ctrlShiftPKey())
	m = updated.(gowork.Model)
	if m.PlanModeActive() {
		t.Fatal("ctrl+shift+p did not disable plan mode")
	}
	if strings.Contains(m.StatusLineText(), "PLAN") {
		t.Fatal("statusline still shows PLAN after leaving plan mode")
	}
}

func TestPlanModeEscapeFromIdle(t *testing.T) {
	m := newModeTestModel()
	m.Mode = gowork.ModeIdle

	updated, _ := m.Update(ctrlShiftPKey())
	m = updated.(gowork.Model)
	if !m.PlanModeActive() {
		t.Fatal("plan mode was not enabled")
	}

	updated, _ = m.Update(escKey())
	m = updated.(gowork.Model)
	if m.PlanModeActive() {
		t.Fatal("esc from the idle mode indicator did not leave plan mode")
	}
}

func TestPlanModeEscapeSequenceFromPrompt(t *testing.T) {
	m := newModeTestModel() // starts focused in the prompt

	updated, _ := m.Update(ctrlShiftPKey())
	m = updated.(gowork.Model)
	if !m.PlanModeActive() {
		t.Fatal("plan mode was not enabled")
	}

	// First esc keeps its normal prompt job: blur and drop to idle.
	updated, _ = m.Update(escKey())
	m = updated.(gowork.Model)
	if m.Mode != gowork.ModeIdle {
		t.Fatalf("mode after first esc = %v, want idle", m.Mode)
	}
	if !m.PlanModeActive() {
		t.Fatal("first esc should only blur the prompt, not leave plan mode")
	}

	// Second esc is now "esc from the mode indicator".
	updated, _ = m.Update(escKey())
	m = updated.(gowork.Model)
	if m.PlanModeActive() {
		t.Fatal("second esc from idle did not leave plan mode")
	}
}

type planStubTool struct {
	name string
}

func (s planStubTool) Name() string                 { return s.name }
func (s planStubTool) Description() string          { return "test stub" }
func (s planStubTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s planStubTool) Kind() tools.Kind             { return tools.KindAllowed }
func (s planStubTool) Run(context.Context, tools.DispatchArgs, json.RawMessage) (tools.ToolResult, error) {
	return tools.Ok("ran " + s.name), nil
}

func newPlanTestDispatcher(t *testing.T, names ...string) *tools.Dispatcher {
	t.Helper()
	agentTools := make([]tools.AgentTool, 0, len(names))
	for _, name := range names {
		agentTools = append(agentTools, planStubTool{name: name})
	}
	d, err := tools.InitDispacher(t.TempDir(), nil, nil, agentTools...)
	if err != nil {
		t.Fatalf("InitDispacher: %v", err)
	}
	return d
}

func TestDispatcherPlanModeGating(t *testing.T) {
	d := newPlanTestDispatcher(t,
		"read_file", "grep_search", "file_search", "list_directory", "skill",
		"web_search", "todo_list",
		"create_file_tool", "write_file", "edit_file", "move_file", "delete_file",
		"bash", "web_fetch", "questions_tool",
	)
	ctx := context.Background()
	call := func(name, input string) tools.ToolResult {
		return d.Dispach(ctx, tools.ToolUse{Name: name, Input: json.RawMessage(input)})
	}

	// Normal mode still runs mutating tools (permissions live above the
	// dispatcher and are unchanged).
	if got := call("write_file", `{}`); got.IsError {
		t.Fatalf("write_file blocked outside plan mode: %s", got.Content)
	}

	d.SetPlanMode(true)
	if !d.PlanMode() {
		t.Fatal("PlanMode() = false after SetPlanMode(true)")
	}

	allowed := []struct {
		name  string
		input string
	}{
		{"read_file", `{"path":"main.go"}`},
		{"grep_search", `{"pattern":"plan"}`},
		{"file_search", `{"pattern":"*.go"}`},
		{"list_directory", `{"path":"."}`},
		{"skill", `{"name":"test"}`},
		{"web_search", `{"query":"golang"}`},
		{"todo_list", `{"action":"list"}`},
	}
	for _, tc := range allowed {
		got := call(tc.name, tc.input)
		if got.IsError {
			t.Errorf("%s should be allowed in plan mode, got error: %s", tc.name, got.Content)
			continue
		}
		if got.Content != "ran "+tc.name {
			t.Errorf("%s result = %q, want it to run", tc.name, got.Content)
		}
	}

	blocked := []struct {
		name  string
		input string
	}{
		{"create_file_tool", `{"path":"a.go","content":"x"}`},
		{"write_file", `{"path":"a.go","content":"x"}`},
		{"edit_file", `{"path":"a.go","old":"a","new":"b"}`},
		{"move_file", `{"from":"a.go","to":"b.go"}`},
		{"delete_file", `{"path":"a.go"}`},
		{"bash", `{"command":"touch x"}`},
		{"web_fetch", `{"url":"https://example.com"}`},
		{"questions_tool", `{"questions":[{"question":"which?"}]}`},
		{"todo_list", `{"action":"push","tasks":["change code"]}`},
		{"todo_list", `{"action":"baseline","tasks":["change code"]}`},
		{"todo_list", `{"action":"clear"}`},
		{"todo_list", `not-json`},
	}
	for _, tc := range blocked {
		got := call(tc.name, tc.input)
		if !got.IsError {
			t.Errorf("%s was not blocked in plan mode (result %q)", tc.name, got.Content)
			continue
		}
		if !strings.Contains(got.Content, tc.name) {
			t.Errorf("%s refusal does not name the tool: %q", tc.name, got.Content)
		}
		if !strings.Contains(strings.ToUpper(got.Content), "PLAN MODE") {
			t.Errorf("%s refusal is not labelled plan mode: %q", tc.name, got.Content)
		}
	}

	// Unknown tools still report themselves as unknown instead of leaking a
	// plan-mode message for a tool that does not exist.
	if got := call("not_a_tool", `{}`); !got.IsError || !strings.Contains(got.Content, "unknown tool") {
		t.Errorf("unknown tool result = %+v, want unknown-tool error", got)
	}

	// Leaving plan mode restores the mutating tools immediately.
	d.SetPlanMode(false)
	if got := call("write_file", `{}`); got.IsError {
		t.Fatalf("write_file still blocked after leaving plan mode: %s", got.Content)
	}
}

func TestPlanModeAllows(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"read_file", `{}`, true},
		{"write_file", `{}`, false},
		{"bash", `{}`, false},
		{"web_fetch", `{}`, false},
		{"todo_list", `{"action":"list"}`, true},
		{"todo_list", `{"action":"mark"}`, false},
		{"", `{}`, false},
		{"future_mutating_tool", `{}`, false},
	}
	for _, tc := range cases {
		if got := tools.PlanModeAllows(tc.name, json.RawMessage(tc.input)); got != tc.want {
			t.Errorf("PlanModeAllows(%q, %s) = %v, want %v", tc.name, tc.input, got, tc.want)
		}
	}
}
