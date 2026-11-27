package mentionmenu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testItems() []Item {
	return []Item{
		{Kind: KindSkill, Name: "alpha", Description: "first skill"},
		{Kind: KindSkill, Name: "alphabet", Description: "second skill"},
		{Kind: KindMCP, Name: "github", Description: "stdio · connected"},
		{Kind: KindLSP, Name: "gopls", Description: "go · enabled"},
		{Kind: KindFile, Name: "main.go", Description: "cmd/main.go", Insert: "@cmd/main.go"},
	}
}

func itemNames(items []Item) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Name
	}
	return out
}

func TestSyncFiltersAndScores(t *testing.T) {
	m := New(testItems()...)
	m.SetWidth(100)

	if m.Visible() {
		t.Fatal("menu starts visible")
	}

	m.Sync("@", 1)
	if !m.Visible() {
		t.Fatal("bare @ did not show every source")
	}
	if got := len(m.Matches()); got != len(testItems()) {
		t.Fatalf("bare @ matched %d items, want %d", got, len(testItems()))
	}

	// A shared prefix is ranked by fuzzy score; the shorter exact-prefix
	// match wins over the longer one.
	m.Sync("@alp", 4)
	if got := itemNames(m.Matches()); len(got) < 2 || got[0] != "alpha" || got[1] != "alphabet" {
		t.Fatalf("@alp matches = %v, want alpha before alphabet", got)
	}

	// Matching is case-insensitive.
	m.Sync("@ALP", 4)
	if got := itemNames(m.Matches()); len(got) == 0 || got[0] != "alpha" {
		t.Fatalf("@ALP matches = %v, want alpha", got)
	}

	// Source-qualified queries narrow to one kind.
	m.Sync("@mcp:", 5)
	if got := itemNames(m.Matches()); len(got) != 1 || got[0] != "github" {
		t.Fatalf("@mcp: matches = %v, want [github]", got)
	}
	m.Sync("@lsp:", 5)
	if got := itemNames(m.Matches()); len(got) != 1 || got[0] != "gopls" {
		t.Fatalf("@lsp: matches = %v, want [gopls]", got)
	}

	// A path fragment matches the relative path description.
	m.Sync("@cmd", 4)
	if got := itemNames(m.Matches()); len(got) != 1 || got[0] != "main.go" {
		t.Fatalf("@cmd matches = %v, want [main.go]", got)
	}

	m.Sync("@zzz", 4)
	if m.Visible() || m.Height() != 0 {
		t.Fatalf("no-match query stayed visible (height=%d)", m.Height())
	}
}

func TestNavigationWrapsAndKeepsSelectionVisible(t *testing.T) {
	items := make([]Item, MaxRows+3)
	for i := range items {
		items[i] = Item{
			Kind:        KindFile,
			Name:        "file" + string(rune('a'+i)) + ".go",
			Description: "pkg/file.go",
		}
	}
	m := New(items...)
	m.SetWidth(80)
	m.Sync("@", 1)

	if got := m.Height(); got != MaxRows {
		t.Fatalf("height with %d items = %d, want %d", len(items), got, MaxRows)
	}
	if sel, _ := m.Selected(); sel.Name != items[0].Name {
		t.Fatalf("initial selection = %s, want %s", sel.Name, items[0].Name)
	}

	m.Move(-1)
	if sel, _ := m.Selected(); sel.Name != items[len(items)-1].Name {
		t.Fatalf("wrapped-up selection = %s, want %s", sel.Name, items[len(items)-1].Name)
	}
	view := m.View()
	if !strings.Contains(view, items[len(items)-1].Name) {
		t.Fatalf("scrolled view does not show the selected item:\n%s", view)
	}
	if strings.Contains(view, items[0].Name) {
		t.Fatalf("scrolled view still shows the first item:\n%s", view)
	}

	m.Move(1)
	if sel, _ := m.Selected(); sel.Name != items[0].Name {
		t.Fatalf("wrapped-down selection = %s, want %s", sel.Name, items[0].Name)
	}
}

func TestCloseStaysClosedUntilEdit(t *testing.T) {
	m := New(testItems()...)
	m.Sync("@alp", 4)
	if !m.Visible() {
		t.Fatal("menu not visible before close")
	}
	m.Close()
	if m.Visible() {
		t.Fatal("Close did not hide the menu")
	}
	m.Sync("@alp", 4)
	if m.Visible() {
		t.Fatal("dismissed menu reopened on the same text")
	}
	m.Sync("@alph", 5)
	if !m.Visible() {
		t.Fatal("edited query did not reopen the menu")
	}
}

func TestSelectionReturnsTokenOffsets(t *testing.T) {
	m := New(testItems()...)
	input := "look at @alp then stop"
	cursor := len([]rune("look at @alp"))
	m.Sync(input, cursor)

	item, start, end, ok := m.Selection()
	if !ok {
		t.Fatal("no selection for an active @ token")
	}
	if item.Name != "alpha" {
		t.Fatalf("selected %q, want alpha", item.Name)
	}
	if start != len([]rune("look at ")) || end != cursor {
		t.Fatalf("selection offsets = [%d,%d), want [%d,%d)", start, end, len([]rune("look at ")), cursor)
	}

	// An @ in the middle of a word (an email address) is not a mention token.
	m.Sync("mail me@example.com", len("mail me@example.com"))
	if m.Visible() {
		t.Fatal("email-like @ opened the mention menu")
	}
}

func TestViewShowsKindNameAndDescription(t *testing.T) {
	m := New(testItems()...)
	m.SetWidth(120)
	m.Sync("@mcp:", 5)
	view := m.View()
	for _, want := range []string{"mcp", "github", "stdio · connected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if got := strings.Count(view, "\n") + 1; got != m.Height() {
		t.Fatalf("view has %d lines, Height() = %d", got, m.Height())
	}
}

func TestMentionForms(t *testing.T) {
	cases := []struct {
		item Item
		want string
	}{
		{Item{Kind: KindSkill, Name: "go-testing"}, "@go-testing"},
		{Item{Kind: KindMCP, Name: "github"}, "@mcp:github"},
		{Item{Kind: KindLSP, Name: "gopls"}, "@lsp:gopls"},
		{Item{Kind: KindFolder, Name: "Tui"}, "@Tui"},
		{Item{Kind: KindFile, Name: "main.go", Insert: "@cmd/main.go"}, "@cmd/main.go"},
	}
	for _, tc := range cases {
		if got := tc.item.Mention(); got != tc.want {
			t.Fatalf("%s mention = %q, want %q", tc.item.Kind, got, tc.want)
		}
	}
}

func TestProjectItemsAreRootRelativeAndSkipVCS(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Tui", "Components"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tui", "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	items := ProjectItems(root)
	byMention := make(map[string]Item, len(items))
	for _, item := range items {
		byMention[item.Mention()] = item
		if strings.HasPrefix(item.Description, ".git") {
			t.Fatalf(".git entry was not skipped: %+v", item)
		}
	}

	folder, ok := byMention["@Tui"]
	if !ok || folder.Kind != KindFolder || folder.Description != "Tui/" {
		t.Fatalf("folder item = %+v (ok=%v), want kind folder at Tui/", folder, ok)
	}
	file, ok := byMention["@Tui/main.go"]
	if !ok || file.Kind != KindFile || file.Description != "Tui/main.go" {
		t.Fatalf("file item = %+v (ok=%v), want kind file at Tui/main.go", file, ok)
	}
	readme, ok := byMention["@README.md"]
	if !ok || readme.Kind != KindFile {
		t.Fatalf("root file item = %+v (ok=%v)", readme, ok)
	}
	if _, ok := byMention["@.git/config"]; ok {
		t.Fatal(".git file was enumerated")
	}
}

func TestProjectItemsEmptyRoot(t *testing.T) {
	if got := ProjectItems(""); got != nil {
		t.Fatalf("ProjectItems(\"\") = %v, want nil", got)
	}
}
