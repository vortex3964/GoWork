// Package db is GoWork's local statistics + session store. It's a plain
// SQLite database (pure-Go driver, no CGO) kept inside the Db/ folder. It
// records how much each model was used (per-generation token usage) and the
// message history of every app run so a past session can be restored with
// `GoWork -S <sessionID> [path]`.
//
// Nothing is written to disk during a run: session start is the only eager
// write, usage rows and messages accumulate in memory and all get flushed
// once on exit (FinalizeSession) before the terminal tears down.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DefaultPath is where the database lives relative to the project root.
const DefaultPath = "Db/gowork.db"

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at    TEXT NOT NULL,
    ended_at      TEXT,
    model         TEXT,
    provider      TEXT,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    message_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS usage (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    model             TEXT NOT NULL,
    provider          TEXT,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    tool_calls        INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_usage_time   ON usage(created_at);
CREATE INDEX IF NOT EXISTS idx_usage_model  ON usage(model);

CREATE TABLE IF NOT EXISTS messages (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id     INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role           TEXT NOT NULL,
    content        TEXT,
    tool_call_id   TEXT,
    tool_calls_json TEXT,
    created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);

-- which skills the session had loaded, so a restored session can re-load
-- them automatically.
CREATE TABLE IF NOT EXISTS session_skills (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL,
    loaded_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_skills ON session_skills(session_id);
`

// Store wraps the SQLite handle plus the in-memory buffer that gets flushed
// on exit.
type Store struct {
	hnd *sql.DB
	// path is where the database lives, kept so a session can be reopened.
	path string

	// the session currently being recorded (one app run = one session)
	sessionID   int64
	startedAt   string
	currentModl string
	currentProv string

	// pendingUsage holds every recorded generation between flushes so the
	// stats screen can reflect the live session without a table read on
	// every token count.
	pendingUsage []UsageRecord

	// sessionSkills is the session's loaded-skill set, buffered the same way
	// and written as session_skills rows on finalize so a restored session
	// comes back with its skills already loaded.
	sessionSkills map[string]struct{}
}

// Open opens (creating if needed) the database at path and guarantees the
// schema exists. The returned Store is safe to use, but should only be
// touched from one goroutine (the bubbletea update loop's).
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("db: create dir %s: %w", dir, err)
		}
	}

	// modernc.org/sqlite: use _pragma params on the DSN to enable foreign
	// keys (they're off by default in sqlite) and a busy timeout so a
	// leftover lock from a crashed run can't wedge the app open.
	dsn := "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)"
	h, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	if err := h.Ping(); err != nil {
		h.Close()
		return nil, fmt.Errorf("db: ping %s: %w", path, err)
	}
	if _, err := h.Exec(schema); err != nil {
		h.Close()
		return nil, fmt.Errorf("db: schema: %w", err)
	}

	return &Store{hnd: h, path: path, sessionSkills: make(map[string]struct{})}, nil
}

// Close frees the handle. Call it after FinalizeSession.
func (s *Store) Close() error {
	if s == nil || s.hnd == nil {
		return nil
	}
	return s.hnd.Close()
}