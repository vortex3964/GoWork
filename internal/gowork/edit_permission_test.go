package gowork

import (
	"testing"

	"GoWork/providers"
	"GoWork/tools"
	editfiletool "GoWork/tools/EditFileTool"
	readfiletool "GoWork/tools/ReadFileTool"
)

// TestEditFileRequiresPermissionPrompt makes sure the approval gate sees
// edit_file as a mutating tool. Without this, edits would run as soon as the
// model requested them while writes and deletes still asked first.
func TestEditFileRequiresPermissionPrompt(t *testing.T) {
	dispatcher, err := tools.InitDispacher(t.TempDir(), nil, nil,
		editfiletool.New(), readfiletool.New())
	if err != nil {
		t.Fatalf("InitDispacher: %v", err)
	}

	m := model{
		tool_dispatcher:   dispatcher,
		pendingTools:      []pendingTool{{call: providers.ToolCall{Tool_name: "edit_file"}}},
		pendingResults:    make([]*tools.ToolResult, 1),
		permissionDecided: make([]bool, 1),
	}

	if got := m.nextPendingPermissionSlice(); got != 0 {
		t.Fatalf("edit_file permission slot = %d, want 0", got)
	}

	m.pendingTools[0].call.Tool_name = "read_file"
	if got := m.nextPendingPermissionSlice(); got != -1 {
		t.Fatalf("read_file permission slot = %d, want -1 (auto-approved)", got)
	}
}
