package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gowork "GoWork/internal/gowork"
)

// TestSystemPromptIsConciseBrief locks in the intended shape of the system
// prompt: a short, professional brief with accurate tool names, the required
// injection placeholders, and an explicitly labelled project snapshot.
func TestSystemPromptIsConciseBrief(t *testing.T) {
	path := filepath.Join("..", gowork.SystemPromptPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	prompt := string(data)

	if got := len(data); got > 4500 {
		t.Errorf("system prompt is %d bytes, want at most 4500; keep it a concise brief", got)
	}

	for _, placeholder := range []string{"${PROJECT_ROOT}", "${IS_GIT_REPO}", "${PLATFORM}", "${PROJECT_TREE}"} {
		if !strings.Contains(prompt, placeholder) {
			t.Errorf("missing injection placeholder %s", placeholder)
		}
	}

	if !strings.Contains(prompt, "snapshot") || !strings.Contains(prompt, "session started") {
		t.Error("project tree must be framed as a snapshot taken at session start")
	}

	for _, stale := range []string{
		"MANDATORY",
		"You may NOT",
		"NEVER invent",
		"worst thing you can do",
		"authoritative listing",
	} {
		if strings.Contains(prompt, stale) {
			t.Errorf("defensive scaffolding still present: %q", stale)
		}
	}

	// These are the names the dispatcher actually registers. create_file_tool
	// is the one the old prompt got wrong.
	for _, tool := range []string{
		"read_file", "grep_search", "file_search", "list_directory",
		"edit_file", "write_file", "create_file_tool", "move_file",
		"delete_file", "bash", "skill", "web_fetch", "web_search",
		"todo_list", "questions_tool",
	} {
		if !strings.Contains(prompt, tool) {
			t.Errorf("prompt does not mention registered tool %q", tool)
		}
	}
	if strings.Contains(prompt, "`create_file`") {
		t.Error("prompt uses `create_file`, but the registered tool name is create_file_tool")
	}

	// Approval claims must match the dispatcher: edit_file is gated like the
	// other write tools, and bash is approved per call but not sandboxed.
	editBullet := toolBullet(prompt, "edit_file")
	if !strings.Contains(strings.ToLower(editBullet), "requires approval") {
		t.Errorf("edit_file bullet must say it requires approval: %q", editBullet)
	}
	bashBullet := toolBullet(prompt, "bash")
	if !strings.Contains(bashBullet, "per-call approval") {
		t.Errorf("bash bullet must say approval is per call: %q", bashBullet)
	}
	if !strings.Contains(bashBullet, "not sandboxed") {
		t.Errorf("bash bullet must say the command itself is not sandboxed: %q", bashBullet)
	}
}

// toolBullet returns the prompt line that introduces the named tool.
func toolBullet(prompt, name string) string {
	prefix := "- `" + name + "`"
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
