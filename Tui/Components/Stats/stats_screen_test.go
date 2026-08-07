package stats

import (
	"testing"
	"time"

	"GoWork/Db"
)

func testStats() []db.ModelStat {
	now := time.Now()
	prov := map[string]string{
		"gemini-2.5-pro":    "google",
		"claude-3-7-sonnet": "anthropic",
		"gpt-4o":            "openai",
		"qwen2.5-coder:14b": "ollama",
		"llama3.1:8b":       "ollama",
		"mistral-large":     "ollama",
	}
	models := []string{"gemini-2.5-pro", "claude-3-7-sonnet", "gpt-4o", "qwen2.5-coder:14b", "llama3.1:8b", "mistral-large"}
	rows := make([]db.ModelStat, 0, len(models)+1)
	for i, name := range models {
		rows = append(rows, db.ModelStat{
			Model:            name,
			Provider:         prov[name],
			PromptTokens:     1000 * (i + 3),
			CompletionTokens: 500 * (i + 1),
			TotalTokens:      1500 * (i + 2),
			Calls:            i + 1,
			ToolCalls:        i,
			FirstUsed:        now.Add(-time.Duration(i) * 24 * time.Hour),
			LastUsed:         now.Add(-time.Duration(i) * time.Hour),
		})
	}
	// one more so "others" groups kick in
	rows = append(rows, db.ModelStat{
		Model: "deepseek-r1", Provider: "ollama",
		PromptTokens: 120, CompletionTokens: 80, TotalTokens: 200,
		Calls: 2, ToolCalls: 0,
		FirstUsed: now.Add(-48 * time.Hour), LastUsed: now,
	})
	return rows
}

func TestViewRenders(t *testing.T) {
	for _, size := range [][2]int{{392, 655}, {40, 10}, {120, 30}, {80, 24}} {
		m := New()
		m.SetData(testStats())
		m.SetSize(size[0], size[1])
		m.cursor = 2
		out := m.View()
		if out == "" {
			t.Fatalf("empty view for %v", size)
		}
	}
}

func TestViewEmpty(t *testing.T) {
	m := New()
	m.SetData(nil)
	m.SetSize(100, 30)
	if m.View() == "" {
		t.Fatal("empty view for no-data")
	}
}

func TestFilterAndMagnify(t *testing.T) {
	m := New()
	m.SetData(testStats())
	m.SetSize(120, 40)

	m.filtering = true
	m.filterInput.Focus()
	m.filterInput.SetValue("gpt")
	m.applyFilter()
	if len(m.items) != 1 || m.items[0].Model != "gpt-4o" {
		t.Fatalf("filter should hit only gpt-4o: %+v", m.items)
	}
	m.magnifySelected()
	if m.modal == nil {
		t.Fatal("magnify didn't open a modal")
	}
	if m.modal.model.Model != "gpt-4o" {
		t.Fatalf("modal shows wrong model: %s", m.modal.model.Model)
	}
	out := m.View()
	if out == "" {
		t.Fatal("modal view empty")
	}
}

func TestScrollAndPan(t *testing.T) {
	m := New()
	m.SetData(testStats())
	m.SetSize(80, 40)
	for i := 0; i < len(m.items)+10; i++ {
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	}
	if m.cursor >= len(m.items) {
		t.Fatal("cursor overflowed the list")
	}
	if m.cursor > 0 {
		m.cursor--
	}
	for i := 0; i < 5; i++ {
		m.hScroll += 4
		m.clampScroll()
	}
	if m.hScroll > m.hScrollMax() {
		t.Fatal("hScroll escaped its clamp")
	}
}