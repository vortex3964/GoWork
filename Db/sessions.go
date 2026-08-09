package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"GoWork/providers"
)

// StartSession begins a new session row (one app run = one session). Called
// once at boot; everything else is buffered until FinalizeSession.
func (s *Store) StartSession(model, provider string) error {
	now := time.Now().Format(time.RFC3339)
	res, err := s.hnd.Exec(`
		INSERT INTO sessions (started_at, model, provider)
		VALUES (?, ?, ?)`, now, model, provider)
	if err != nil {
		return fmt.Errorf("db: start session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("db: session id: %w", err)
	}
	s.sessionID = id
	s.startedAt = now
	s.currentModl = model
	s.currentProv = provider
	return nil
}

// SetSessionSkills replaces the session's buffered loaded-skill set. Called
// whenever the user loads/unloads a skill so finalize writes the exact set.
func (s *Store) SetSessionSkills(names []string) {
	if s.sessionSkills == nil {
		s.sessionSkills = make(map[string]struct{}, len(names))
	} else {
		clear(s.sessionSkills)
	}
	for _, name := range names {
		if name != "" {
			s.sessionSkills[name] = struct{}{}
		}
	}
}

// FinalizeSession is the single save-on-exit point: it stamps the session's
// end, flushes every buffered usage row, the session's loaded skills and its
// message history, then prunes to the retention budgets (last 5 sessions,
// 30 days of usage).
func (s *Store) FinalizeSession(messages []providers.Message, totalTokens int) error {
	if s == nil || s.hnd == nil {
		return nil
	}
	if s.sessionID == 0 {
		// No session was ever started; nothing to finalize.
		return nil
	}
	tx, err := s.hnd.Begin()
	if err != nil {
		return fmt.Errorf("db: finalize begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().Format(time.RFC3339)
	if _, err := tx.Exec(`
		UPDATE sessions SET ended_at = ?, total_tokens = ?, message_count = ?
		WHERE id = ?`,
		now, totalTokens, len(messages), s.sessionID); err != nil {
		return fmt.Errorf("db: finalize session row: %w", err)
	}

	for _, u := range s.pendingUsage {
		ts := u.CreatedAt.Format(time.RFC3339)
		if _, err := tx.Exec(`
			INSERT INTO usage (session_id, model, provider, prompt_tokens,
			                   completion_tokens, total_tokens, tool_calls, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.sessionID, u.Model, u.Provider, u.PromptTokens,
			u.CompletionTokens, u.TotalTokens, u.ToolCalls, ts); err != nil {
			return fmt.Errorf("db: insert usage: %w", err)
		}
	}

	for _, msg := range messages {
		tcJSON := ""
		if len(msg.ToolCalls) > 0 {
			b, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("db: marshal tool calls: %w", err)
			}
			tcJSON = string(b)
		}
		if _, err := tx.Exec(`
			INSERT INTO messages (session_id, role, content, tool_call_id, tool_calls_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			s.sessionID, msg.Role, msg.Content, msg.ToolCallID, tcJSON, now); err != nil {
			return fmt.Errorf("db: insert message: %w", err)
		}
	}

	// The loaded skill names follow their session's cascade.
	for name := range s.sessionSkills {
		if _, err := tx.Exec(`
			INSERT INTO session_skills (session_id, skill_name, loaded_at)
			VALUES (?, ?, ?)`,
			s.sessionID, name, now); err != nil {
			return fmt.Errorf("db: insert session skill: %w", err)
		}
	}

	if err := s.pruneLocked(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: finalize commit: %w", err)
	}

	// The session is sealed; clear the live buffer so a second
	// FinalizeSession (defensive double-call) is a no-op.
	s.sessionID = 0
	s.pendingUsage = s.pendingUsage[:0]
	return nil
}

// pruneLocked enforces the sliding window within the finalize transaction:
//   - keep only the 5 most recent sessions, dropping their usage/messages via
//     the ON DELETE CASCADE foreign key;
//   - drop any usage rows older than the 30-day retention window.
func (s *Store) pruneLocked(tx *sql.Tx) error {
	if _, err := tx.Exec(`
		DELETE FROM sessions
		WHERE id NOT IN (
			SELECT id FROM sessions ORDER BY id DESC LIMIT ?
		)`, 5); err != nil {
		return fmt.Errorf("db: prune sessions: %w", err)
	}
	cutoff := time.Now().Add(-retentionWindow).Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM usage WHERE created_at < ?`, cutoff); err != nil {
		return fmt.Errorf("db: prune usage: %w", err)
	}
	return nil
}

// LoadedSession is what `GoWork -S <id> path` restores: the session's
// metadata plus its full message history back in order.
type LoadedSession struct {
	ID           int64
	StartedAt    time.Time
	EndedAt      time.Time
	Model        string
	Provider     string
	TotalTokens  int
	MessageCount int
	Messages     []providers.Message
	// Skills are the session's loaded skill names, in load order.
	Skills []string
}

// LoadSession reads a stored session and its message history by id. Returns
// (nil, nil) when no such session exists, and closes over any unparseable
// message gracefully (skipped) instead of failing the whole restore.
func (s *Store) LoadSession(id int64) (*LoadedSession, error) {
	var (
		startS, endS string
		model, prov  string
		total, count int
	)
	err := s.hnd.QueryRow(`
		SELECT started_at, COALESCE(ended_at, ''), model, provider,
		       total_tokens, message_count
		FROM sessions WHERE id = ?`, id).
		Scan(&startS, &endS, &model, &prov, &total, &count)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: load session %d: %w", id, err)
	}

	ls := &LoadedSession{
		ID:           id,
		Model:        model,
		Provider:     prov,
		TotalTokens:  total,
		MessageCount: count,
		Messages:     []providers.Message{},
	}
	ls.StartedAt, _ = time.Parse(time.RFC3339, startS)
	ls.EndedAt, _ = time.Parse(time.RFC3339, endS)

	// The session's loaded skills come back with it so a restore can
	// re-load them automatically.
	skillRows, err := s.hnd.Query(`
		SELECT skill_name FROM session_skills
		WHERE session_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("db: load session skills: %w", err)
	}
	defer skillRows.Close()
	for skillRows.Next() {
		var name string
		if err := skillRows.Scan(&name); err != nil {
			return nil, fmt.Errorf("db: scan session skill: %w", err)
		}
		ls.Skills = append(ls.Skills, name)
	}

	rows, err := s.hnd.Query(`
		SELECT role, COALESCE(content, ''), COALESCE(tool_call_id, ''),
		       COALESCE(tool_calls_json, '')
		FROM messages WHERE session_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("db: load messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var role, content, tcID, tcJSON string
		if err := rows.Scan(&role, &content, &tcID, &tcJSON); err != nil {
			return nil, fmt.Errorf("db: scan message: %w", err)
		}
		msg := providers.Message{Role: role, Content: content, ToolCallID: tcID}
		if tcJSON != "" {
			if err := json.Unmarshal([]byte(tcJSON), &msg.ToolCalls); err != nil {
				// Unparseable tool calls just drop; the content still
				// restores.
				msg.ToolCalls = nil
			}
		}
		ls.Messages = append(ls.Messages, msg)
	}
	return ls, rows.Err()
}