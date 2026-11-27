package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "GoWork/Db"
	"GoWork/Tui/Components/MessageArea"
	"GoWork/Tui/Components/SkillsTab"
	gowork "GoWork/internal/gowork"
	"GoWork/providers"
	skillstool "GoWork/tools/SkillsTool"
)

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func TestResumeSessionRestoresSkills(t *testing.T) {
	// 1) A previous session finishes with skill "hello" loaded.
	store := newTestStore(t)
	if err := store.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("start: %v", err)
	}
	store.SetSessionSkills([]string{"hello"})
	msgs := []providers.Message{
		{Role: "user", Content: "use the hello skill"},
		{Role: "assistant", Content: "sure"},
		{Role: "tool", Content: "result", ToolCallID: "c1"},
	}
	if err := store.FinalizeSession(msgs, 100); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	// 2) A real skills folder with the skill on disk.
	root := t.TempDir()
	skillDir := filepath.Join(root, ".GoWork", "skills", "hello")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: hello\ndescription: hello world\n---\n\nhi"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillstool.SetManager(skillstool.NewManager(root))

	// 3) New app run: resume session 1 exactly like `GoWork -S 1` does.
	if err := store.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	m := gowork.Model{
		MessageArea: messagearea.New(),
		Skills:      skills.New(),
		Db:          store,
	}
	m.ResumeSession(1)

	mgr := skillstool.GetManager()
	if !mgr.IsLoaded("hello") {
		t.Fatalf("expected session skill hello loaded, got %v", mgr.LoadedNames())
	}
	if len(m.Context) != 3 {
		t.Fatalf("expected 3 messages in context, got %d", len(m.Context))
	}
	if m.MessageArea.Size() < 3 {
		t.Fatalf("expected messages replayed in the area, got size %d", m.MessageArea.Size())
	}
	// The restore should also re-register the tool def so the model sees it.
	desc := skillstool.ToolDescription()
	if !strings.Contains(desc, "<name>hello</name>") {
		t.Fatalf("tool description lacks restored skill: %q", desc)
	}
}
