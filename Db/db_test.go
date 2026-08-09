package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GoWork/providers"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func TestStartAndRecord(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := s.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("start: %v", err)
	}
	s.RecordUsage("gpt-4o", "openai", 100, 50, 150, 2, time.Now())
	s.RecordUsage("gpt-4o", "openai", 10, 20, 30, 0, time.Now())
	s.RecordUsage("gpt-4o", "openai", 0, 0, 0, 0, time.Now()) // ignored: zero usage

	rows, err := s.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model, got %d", len(rows))
	}
	st := rows[0]
	if st.Model != "gpt-4o" || st.TotalTokens != 180 || st.Calls != 2 || st.ToolCalls != 2 {
		t.Fatalf("bad aggregate: %+v", st)
	}

	// Finalize writes messages + prunes usage window + session cap.
	msgs := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello", ToolCalls: []providers.ToolCall{{Tool_call_id: "c1", Tool_name: "read_file"}}},
		{Role: "tool", Content: "ok", ToolCallID: "c1"},
	}
	s.SetSessionSkills([]string{"hello", "refactor"})
	if err := s.FinalizeSession(msgs, 180); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	s.Close()

	// Reopen the same file and load the stored session back.
	s2, err := Open(s.path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if err := s2.StartSession("gpt-4o", "openai"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	ls, err := s2.LoadSession(1)
	if err != nil || ls == nil {
		t.Fatalf("load session 1: %v %v", ls, err)
	}
	if len(ls.Messages) != 3 || ls.Messages[1].ToolCalls[0].Tool_name != "read_file" {
		t.Fatalf("messages not restored correctly: %+v", ls.Messages)
	}
	if ls.TotalTokens != 180 {
		t.Fatalf("session total mismatch: %d", ls.TotalTokens)
	}
	if len(ls.Skills) != 2 || ls.Skills[0] != "hello" || ls.Skills[1] != "refactor" {
		t.Fatalf("session skills not restored correctly: %+v", ls.Skills)
	}
}

func TestSessionCapPrune(t *testing.T) {
	s := newTestStore(t)
	for i := int64(1); i <= 7; i++ {
		if err := s.StartSession("m", "p"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if err := s.FinalizeSession(nil, int(i)); err != nil {
			t.Fatalf("finalize %d: %v", i, err)
		}
	}
	// After 7 sessions only the last 5 survive.
	var count int
	if err := s.hnd.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("expected 5 sessions kept, got %d", count)
	}
}

func TestOldUsagePruned(t *testing.T) {
	s := newTestStore(t)
	if err := s.StartSession("m", "p"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	s.RecordUsage("m", "p", 10, 10, 20, 0, old)
	s.RecordUsage("m", "p", 5, 5, 10, 0, time.Now())
	if err := s.FinalizeSession(nil, 30); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.hnd.QueryRow(`SELECT COUNT(*) FROM usage`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected old usage pruned, got %d rows", count)
	}
}

func TestSchemaPlusInsertSQL(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	data, err := os.ReadFile("insert.sql")
	if err != nil {
		t.Fatalf("read insert.sql: %v", err)
	}
	// Drop full-line SQL comments first (- their trailing semicolons would
	// otherwise trip the ';' splitter), then execute each statement.
	var clean []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		clean = append(clean, line)
	}
	for _, stmt := range strings.Split(strings.Join(clean, "\n"), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.hnd.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt[:min(40, len(stmt))], err)
		}
	}

	rows, err := s.Stats()
	if err != nil {
		t.Fatalf("stats after insert.sql: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("insert.sql produced no aggregates")
	}
	if rows[0].TotalTokens <= 0 {
		t.Fatalf("bad top aggregate: %+v", rows[0])
	}

	ls, err := s.LoadSession(1)
	if err != nil || ls == nil {
		t.Fatalf("load seeded session 1: %v %v", ls, err)
	}
	if len(ls.Messages) == 0 {
		t.Fatal("seeded session has no messages")
	}

	// A session beyond the seed range must be missing.
	if missing, _ := s.LoadSession(99999); missing != nil {
		t.Fatal("expected no session with id 99999")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}