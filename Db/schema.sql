-- GoWork schema, mirrored from Db/db.go's `schema` const.
-- Kept in sync manually: if db.go's schema changes, update this file too.
-- Used by `make init_db` to create Db/gowork.db standalone (without
-- needing to launch the TUI app first) before loading insert.sql.

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

CREATE TABLE IF NOT EXISTS session_skills (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL,
    loaded_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_skills ON session_skills(session_id);
