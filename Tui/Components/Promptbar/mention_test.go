package promptbar_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"GoWork/Tui/Components/MentionMenu"
	"GoWork/Tui/Components/Promptbar"
)

func mentionItems() []mentionmenu.Item {
	return []mentionmenu.Item{
		{Kind: mentionmenu.KindSkill, Name: "alpha", Description: "first skill"},
		{Kind: mentionmenu.KindSkill, Name: "alphabet", Description: "second skill"},
		{Kind: mentionmenu.KindMCP, Name: "github", Description: "stdio · connected"},
		{Kind: mentionmenu.KindLSP, Name: "gopls", Description: "go · enabled"},
		{Kind: mentionmenu.KindFile, Name: "main.go", Description: "cmd/main.go", Insert: "@cmd/main.go"},
	}
}

func typeMentionText(t *testing.T, m promptbar.Model, text string) promptbar.Model {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated
	}
	return m
}

func TestMentionOpensOnAtAndFilters(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetMentionItems(mentionItems())
	m.Focus()

	baseHeight := m.Height()
	m = typeMentionText(t, m, "@")
	if got := m.Value(); got != "@" {
		t.Fatalf("prompt after typing @ = %q, want @", got)
	}
	if !m.MentionVisible() {
		t.Fatal("typing @ did not open the mention menu")
	}
	if m.Height() <= baseHeight {
		t.Fatalf("mention menu did not add height: base=%d now=%d", baseHeight, m.Height())
	}
	if sel, ok := m.MentionSelected(); !ok || sel.Name != "alpha" {
		t.Fatalf("initial mention selection = %v (ok=%v), want alpha", sel, ok)
	}
	if m.PaletteVisible() {
		t.Fatal("mention menu also opened the slash palette")
	}

	m = typeMentionText(t, m, "mcp:")
	if got := m.Value(); got != "@mcp:" {
		t.Fatalf("prompt after filter = %q, want @mcp:", got)
	}
	if sel, ok := m.MentionSelected(); !ok || sel.Name != "github" {
		t.Fatalf("filtered mention selection = %v (ok=%v), want github", sel, ok)
	}
	view := m.View()
	if !strings.Contains(view, "github") || !strings.Contains(view, "stdio · connected") {
		t.Fatalf("mention view missing name/description:\n%s", view)
	}
}

func TestMentionNavigationAndCompletion(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetMentionItems(mentionItems())
	m.Focus()
	m.SetValue("@alp")
	if !m.MentionVisible() {
		t.Fatal("mention menu did not open for @alp")
	}

	m.MoveMention(1)
	if sel, _ := m.MentionSelected(); sel.Name != "alphabet" {
		t.Fatalf("selection after down = %s, want alphabet", sel.Name)
	}
	m.MoveMention(-1)
	if sel, _ := m.MentionSelected(); sel.Name != "alpha" {
		t.Fatalf("selection after up = %s, want alpha", sel.Name)
	}

	if !m.AcceptMention() {
		t.Fatal("AcceptMention returned false")
	}
	if got := m.Value(); got != "@alpha " {
		t.Fatalf("prompt after completion = %q, want %q", got, "@alpha ")
	}
	if m.MentionVisible() {
		t.Fatal("mention menu stayed open after completion")
	}
	if !m.Focused() {
		t.Fatal("completion moved focus away from the prompt")
	}
}

func TestMentionCompletionPreservesLeadingText(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetMentionItems(mentionItems())
	m.Focus()
	m.SetValue("look at @al")
	if !m.AcceptMention() {
		t.Fatal("AcceptMention returned false")
	}
	if got := m.Value(); got != "look at @alpha " {
		t.Fatalf("prompt after completion = %q, want %q", got, "look at @alpha ")
	}
}

func TestMentionEscDismissesWithoutClearingPrompt(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetMentionItems(mentionItems())
	m.Focus()
	m.SetValue("@alp")
	if !m.MentionVisible() {
		t.Fatal("mention menu did not open")
	}
	before := m.Height()

	m.CloseMentions()
	if m.MentionVisible() {
		t.Fatal("CloseMentions did not hide the menu")
	}
	if got := m.Value(); got != "@alp" {
		t.Fatalf("prompt after esc = %q, want @alp", got)
	}
	if m.Height() >= before {
		t.Fatalf("menu height not released: before=%d after=%d", before, m.Height())
	}

	// Same text stays dismissed; editing reopens.
	m.SetValue("@alp")
	if m.MentionVisible() {
		t.Fatal("dismissed menu reopened on the same text")
	}
	m.SetValue("@alph")
	if !m.MentionVisible() {
		t.Fatal("editing the query did not reopen the menu")
	}
}

func TestMentionOnlyForWholeWordToken(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetMentionItems(mentionItems())
	m.Focus()
	m.SetValue("me@example")
	if m.MentionVisible() {
		t.Fatal("@ inside an email address opened the mention menu")
	}
}

func TestMentionsDisabledOutsideCodeTab(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetMentionItems(mentionItems())
	m.Focus()
	m.SetMentionsEnabled(false)
	m.SetValue("@alp")
	if m.MentionVisible() {
		t.Fatal("mention menu opened while disabled")
	}

	m.SetMentionsEnabled(true)
	m.SetValue("@alph")
	if !m.MentionVisible() {
		t.Fatal("editing after re-enable did not reopen the menu")
	}
}

func TestSlashPaletteStillWorksAlongsideMentions(t *testing.T) {
	m := promptbar.New()
	m.SetWidth(100)
	m.SetCommands(testCommands())
	m.SetMentionItems(mentionItems())
	m.Focus()

	m = typeMentionText(t, m, "/st")
	if !m.PaletteVisible() {
		t.Fatal("slash palette did not open")
	}
	if m.MentionVisible() {
		t.Fatal("mention menu opened for a slash command")
	}
	completed, ok := m.AcceptPalette()
	if !ok || completed != "/stats " {
		t.Fatalf("AcceptPalette = %q (ok=%v), want %q", completed, ok, "/stats ")
	}
}
