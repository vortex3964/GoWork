package mcp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeContent(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "image", MIMEType: "image/png", Data: "aGVsbG8="},
		{Type: "audio"},
		{Type: "resource", Resource: &ResourceContents{URI: "file:///a.txt", Text: "file body"}},
		{Type: "resource", Resource: &ResourceContents{URI: "file:///b.bin", Blob: "AA=="}},
		{Type: "resource_link", Name: "main.go", URI: "file:///main.go"},
		{Type: "resource_link", URI: "file:///other"},
		{Type: "unknown-kind"},
		{Type: ""},
	}

	got := normalizeContent(blocks)
	want := "hello\n" +
		"[image: image/png]\n" +
		"[audio]\n" +
		"file body\n" +
		"[resource: file:///b.bin]\n" +
		"[resource: main.go (file:///main.go)]\n" +
		"[resource: file:///other]\n" +
		"[unsupported content type: unknown-kind]"
	if got != want {
		t.Errorf("normalizeContent() =\n%q\nwant\n%q", got, want)
	}
}

func TestNormalizeContentEmpty(t *testing.T) {
	if got := normalizeContent(nil); got != "" {
		t.Errorf("normalizeContent(nil) = %q, want empty", got)
	}
}

func TestDurationJSON(t *testing.T) {
	data, err := json.Marshal(Duration(90 * time.Second))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != `"1m30s"` {
		t.Errorf("Marshal = %s, want \"1m30s\"", data)
	}

	var d Duration
	if err := json.Unmarshal([]byte(`"250ms"`), &d); err != nil {
		t.Fatalf("Unmarshal string: %v", err)
	}
	if d.Duration() != 250*time.Millisecond {
		t.Errorf("Unmarshal string = %s", d.Duration())
	}
	if err := json.Unmarshal([]byte(`0.25`), &d); err != nil {
		t.Fatalf("Unmarshal number: %v", err)
	}
	if d.Duration() != 250*time.Millisecond {
		t.Errorf("Unmarshal number = %s", d.Duration())
	}
	if err := json.Unmarshal([]byte(`null`), &d); err != nil {
		t.Fatalf("Unmarshal null: %v", err)
	}
	if d != 0 {
		t.Errorf("Unmarshal null = %s, want 0", d.Duration())
	}
}

func TestSupportedProtocolVersionsCopy(t *testing.T) {
	versions := SupportedProtocolVersions()
	if len(versions) == 0 || versions[0] != PreferredProtocolVersion {
		t.Fatalf("SupportedProtocolVersions = %v", versions)
	}
	versions[0] = "mutated"
	if SupportedProtocolVersions()[0] == "mutated" {
		t.Error("SupportedProtocolVersions returned the internal slice")
	}
}
