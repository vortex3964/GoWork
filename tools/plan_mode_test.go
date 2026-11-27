package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanModeAllowsSkillLoadButBlocksMutations(t *testing.T) {
	if !PlanModeAllows("skill_load", json.RawMessage(`{"action":"list"}`)) {
		t.Fatal("PlanModeAllows(skill_load) = false, want true")
	}
	if PlanModeAllows("skill_install", json.RawMessage(`{}`)) {
		t.Fatal("PlanModeAllows(skill_install) = true, want false")
	}
	if !PlanModeAllows("mcp_list", json.RawMessage(`{}`)) {
		t.Fatal("PlanModeAllows(mcp_list) = false, want true")
	}

	// Every workspace/boundary-mutating tool remains default-denied.
	for _, name := range []string{
		"bash", "create_file", "write_file", "edit_file", "delete_file",
		"move_file", "web_fetch", "mcp_install", "mcp_remove", "skill_install",
		"questions_tool",
	} {
		if PlanModeAllows(name, json.RawMessage(`{}`)) {
			t.Errorf("PlanModeAllows(%s) = true, want false", name)
		}
	}
	if PlanModeAllows("todo_list", json.RawMessage(`{"action":"add"}`)) {
		t.Fatal("PlanModeAllows(todo_list add) = true, want false")
	}
	if !PlanModeAllows("todo_list", json.RawMessage(`{"action":"list"}`)) {
		t.Fatal("PlanModeAllows(todo_list list) = false, want true")
	}
}

func TestDispatcherPlanModeSkillLoadGate(t *testing.T) {
	dispatcher, err := InitDispacher(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(&registryTestTool{name: "skill_load"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Register(&registryTestTool{name: "skill_install"}); err != nil {
		t.Fatal(err)
	}
	dispatcher.SetPlanMode(true)

	allowed := dispatcher.Dispach(context.Background(), ToolUse{Name: "skill_load", Input: json.RawMessage(`{}`)})
	if allowed.IsError {
		t.Fatalf("skill_load was blocked in plan mode: %+v", allowed)
	}
	blocked := dispatcher.Dispach(context.Background(), ToolUse{Name: "skill_install", Input: json.RawMessage(`{}`)})
	if !blocked.IsError || !strings.Contains(blocked.Content, "PLAN MODE") {
		t.Fatalf("skill_install was not blocked in plan mode: %+v", blocked)
	}
}
