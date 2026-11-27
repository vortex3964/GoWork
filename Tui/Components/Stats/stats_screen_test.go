package stats

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"GoWork/Db"
)

// testModel builds a small but representative dashboard: six models so the
// list has rows to select, and a full 30-day window of daily buckets.
func testModel() Model {
	models := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	now := time.Now().Local()
	rows := make([]db.ModelStat, 0, len(models))
	for i, name := range models {
		rows = append(rows, db.ModelStat{
			Model:            name,
			Provider:         "test",
			PromptTokens:     1000 * (i + 1),
			CompletionTokens: 500 * (i + 1),
			TotalTokens:      1500 * (i + 1),
			Calls:            10 + i,
			ToolCalls:        i,
			FirstUsed:        now.AddDate(0, 0, -10),
			LastUsed:         now,
		})
	}

	daily := make([]db.DailyBucket, 0, windowDays)
	for i := windowDays - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		b := db.DailyBucket{
			Day:       day,
			Tokens:    map[string]int{},
			Tools:     map[string]int{},
			ToolCalls: i % 3,
		}
		for j, name := range models {
			n := (i + 1) * (j + 1) * 10
			b.Tokens[name] = n
			b.Total += n
			if (i+j)%3 == 0 {
				b.Tools[name] = 1
			}
		}
		daily = append(daily, b)
	}

	m := New()
	m.SetData(rows, daily)
	m.SetSize(140, 45)
	return m
}

func TestViewCacheReusesFrameUntilStateChanges(t *testing.T) {
	m := testModel()

	first := m.View()
	if m.cache.builds != 1 {
		t.Fatalf("first View built %d frames, want 1", m.cache.builds)
	}
	if got := m.View(); got != first {
		t.Fatal("cached View returned different output")
	}
	if m.cache.builds != 1 {
		t.Fatalf("unchanged View rebuilt (%d builds), want cached", m.cache.builds)
	}

	steps := []struct {
		name   string
		change func()
	}{
		{"selection", func() { m.cursor++ }},
		{"day selection", func() { m.dayCursor = (m.dayCursor + 1) % windowDays }},
		{"day window", func() {
			m.daily = append(m.daily, db.DailyBucket{
				Day:    time.Now().AddDate(0, 0, -windowDays-2).Format("2006-01-02"),
				Tokens: map[string]int{"alpha": 1}, Tools: map[string]int{}, Total: 1,
			})
			m.dataRev++ // data changed even if the window still fits
			m.dayOffset = 1
		}},
		{"size", func() { m.SetSize(120, 40) }},
		{"filter", func() {
			m.filtering = true
			m.filterInput.Focus()
			m.filterInput.SetValue("alpha")
			m.applyFilter()
		}},
		{"modal", func() {
			m.modal = &ModelDetail{model: m.items[0], index: 0, termW: m.width, termH: m.height}
		}},
		{"modal scroll", func() { m.modal.vScroll = 1 }},
	}

	for _, step := range steps {
		step.change()
		before := m.cache.builds
		_ = m.View()
		if m.cache.builds != before+1 {
			t.Fatalf("%s change rebuilt %d times, want 1", step.name, m.cache.builds-before)
		}
	}
}

// TestFilterOwnsArrowKeysWhileActive keeps the filter input usable: arrows
// must move the text cursor instead of navigating the list or the chart, and
// Esc is the only key that dismisses the filter.
func TestFilterOwnsArrowKeysWhileActive(t *testing.T) {
	m := testModel()
	m.filtering = true
	m.filterInput.Focus()
	m.filterInput.SetValue("alpha")
	m.applyFilter()

	cursor := m.cursor
	day := m.dayCursor
	offset := m.dayOffset

	pos := m.filterInput.Position()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated
	if got := m.filterInput.Position(); got != pos-1 {
		t.Fatalf("left while filtering moved the filter cursor to %d, want %d", got, pos-1)
	}
	if m.cursor != cursor || m.dayCursor != day || m.dayOffset != offset {
		t.Fatalf("left while filtering moved list/chart state: cursor %d->%d day %d->%d offset %d->%d",
			cursor, m.cursor, day, m.dayCursor, offset, m.dayOffset)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated
	if m.cursor != cursor {
		t.Fatalf("down while filtering moved the list cursor to %d, want %d", m.cursor, cursor)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated
	if got := m.filterInput.Position(); got != pos {
		t.Fatalf("right while filtering moved the filter cursor to %d, want %d", got, pos)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated
	if m.filtering {
		t.Fatal("esc did not dismiss the filter")
	}
	if m.filterInput.Value() != "" {
		t.Fatalf("esc left filter text %q, want cleared", m.filterInput.Value())
	}
	if len(m.items) != len(m.stats) {
		t.Fatalf("dismissing the filter left %d items, want %d", len(m.items), len(m.stats))
	}
}

func BenchmarkViewCached(b *testing.B) {
	m := testModel()
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkViewRebuild(b *testing.B) {
	m := testModel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Force a rebuild by bumping the data revision each iteration.
		m.dataRev++
		_ = m.View()
	}
}

// TestStatsViewSmoke renders a few states so the cached path and the
// underlying renderer agree on the basics.
func TestStatsViewSmoke(t *testing.T) {
	m := testModel()
	view := m.View()
	if view == "" {
		t.Fatal("empty stats view")
	}
	if !strings.Contains(view, "alpha") {
		t.Fatalf("view missing model name:\n%s", view)
	}
	m.SetData(nil, nil)
	empty := m.View()
	if !strings.Contains(empty, "No usage recorded") {
		t.Fatalf("empty view missing placeholder:\n%s", empty)
	}
}
