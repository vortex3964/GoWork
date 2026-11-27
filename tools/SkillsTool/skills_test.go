package skillstool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"GoWork/tools"
)

func writeSkillFile(t *testing.T, root, dirName, name, desc, body string) string {
	t.Helper()
	dir := filepath.Join(root, SkillsDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	path := filepath.Join(dir, SkillFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	return path
}

// TestDiscoverStoresMetadataOnly proves discovery populates name/description/
// path for the tab without loading or storing the skill body.
func TestDiscoverStoresMetadataOnly(t *testing.T) {
	root := t.TempDir()
	longBody := "STEP ONE\n" + strings.Repeat("very long body line that the tab never renders\n", 500)
	alphaPath := writeSkillFile(t, root, "alpha", "alpha", "Alpha description.", longBody)
	writeSkillFile(t, root, "beta", "beta", "Beta description.", "beta body")
	// Name must match the directory name; this one is filtered out.
	writeSkillFile(t, root, "mismatch", "other-name", "Mismatched.", "body")

	mgr := NewManager(root)
	entries := mgr.Discover()
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid skills, got %d: %+v", len(entries), entries)
	}

	byName := make(map[string]Skill, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
		if e.Body != "" {
			t.Errorf("discovery stored a %d-byte body for %q", len(e.Body), e.Name)
		}
		if e.Path == "" {
			t.Errorf("discovery entry %q has no path", e.Name)
		}
	}

	alpha, ok := byName["alpha"]
	if !ok {
		t.Fatalf("alpha missing from discovery snapshot: %+v", entries)
	}
	if alpha.Description != "Alpha description." {
		t.Errorf("alpha description = %q", alpha.Description)
	}
	if alpha.Path != alphaPath {
		t.Errorf("alpha path = %q, want %q", alpha.Path, alphaPath)
	}
	if _, ok := byName["other-name"]; ok {
		t.Errorf("name/dir mismatch was not filtered out: %+v", entries)
	}
}

// TestLoadedStateAndToolDefSurviveDiscover checks that rediscovery is
// metadata-only but still drives the loaded set and the tool definition.
func TestLoadedStateAndToolDefSurviveDiscover(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "alpha", "alpha", "Alpha description.", "alpha body")

	mgr := NewManager(root)
	mgr.Discover()
	if !mgr.Load("alpha") {
		t.Fatal("Load(alpha) returned false for an existing skill")
	}

	// Refreshing the tab snapshot must not drop session state.
	mgr.Discover()
	if !mgr.IsLoaded("alpha") {
		t.Fatal("Discover dropped the loaded state")
	}
	available := mgr.Available()
	if len(available) != 1 || available[0].Name != "alpha" || available[0].Description != "Alpha description." {
		t.Fatalf("Available() = %+v", available)
	}

	prev := GetManager()
	SetManager(mgr)
	t.Cleanup(func() { SetManager(prev) })

	desc := ToolDef().Description
	if !strings.Contains(desc, "<name>alpha</name>") || !strings.Contains(desc, "<description>Alpha description.</description>") {
		t.Errorf("tool definition missing loaded skill metadata:\n%s", desc)
	}
}

// TestSkillToolReturnsFullContentOnDemand proves discovery leaves bodies on
// disk and the skill tool reads the complete file when the model asks for it.
func TestSkillToolReturnsFullContentOnDemand(t *testing.T) {
	root := t.TempDir()
	body := "Follow these steps:\n1. do the thing\n2. verify it"
	writeSkillFile(t, root, "alpha", "alpha", "Alpha description.", body)

	mgr := NewManager(root)
	entries := mgr.Discover()
	if len(entries) != 1 || entries[0].Body != "" {
		t.Fatalf("discovery did not stay metadata-only: %+v", entries)
	}
	if !mgr.Load("alpha") {
		t.Fatal("Load(alpha) returned false")
	}

	prev := GetManager()
	SetManager(mgr)
	t.Cleanup(func() { SetManager(prev) })

	tool := New()
	raw, err := json.Marshal(map[string]string{"name": "alpha"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	res, err := tool.Run(context.Background(), tools.DispatchArgs{}, raw)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	for _, want := range []string{"name: alpha", "Alpha description.", "1. do the thing", "2. verify it"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("skill tool output missing %q:\n%s", want, res.Content)
		}
	}

	mgr.Unload("alpha")
	res, err = tool.Run(context.Background(), tools.DispatchArgs{}, raw)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not loaded") {
		t.Errorf("expected not-loaded error after Unload, got: %+v", res)
	}
	if strings.Contains(ToolDescription(), "<name>alpha</name>") {
		t.Errorf("tool description still advertises unloaded skill:\n%s", ToolDescription())
	}
}

func TestCreateStillReturnsMetadata(t *testing.T) {
	mgr := NewManager(t.TempDir())
	created, err := mgr.Create("new-skill", "A new skill.", "body text")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "new-skill" || created.Description != "A new skill." {
		t.Errorf("Create returned %+v", created)
	}
	if created.Body != "" {
		t.Errorf("Create loaded the full body (%d bytes) instead of metadata only", len(created.Body))
	}

	entries := mgr.Discover()
	if len(entries) != 1 || entries[0].Name != "new-skill" || entries[0].Description != "A new skill." {
		t.Fatalf("Discover after Create = %+v", entries)
	}
}
