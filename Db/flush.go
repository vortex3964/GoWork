package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"GoWork/providers"
)

// FlushSession persists a checkpoint of the live session without ending it:
// the session row keeps its open ended_at, its messages and skills are
// replaced with the current snapshot, and usage buffered since the last
// checkpoint is inserted. /save uses this so saving does not silently fork the
// session or reset the token counters.
//
// The snapshot is idempotent: later FlushSession calls and the final
// FinalizeSession replace the same rows instead of appending a second copy of
// the history.
func (s *Store) FlushSession(messages []providers.Message, totalTokens int) error {
	if s == nil || s.hnd == nil || s.sessionID == 0 {
		return nil
	}

	tx, err := s.hnd.Begin()
	if err != nil {
		return fmt.Errorf("db: flush begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE sessions SET total_tokens = ?, message_count = ?
		WHERE id = ?`,
		totalTokens, len(messages), s.sessionID); err != nil {
		return fmt.Errorf("db: flush session row: %w", err)
	}
	if err := replaceSessionSnapshot(tx, s.sessionID, s.sessionSkills, messages, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}
	if err := insertPendingUsage(tx, s.sessionID, s.pendingUsage); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: flush commit: %w", err)
	}

	// The usage rows are now on disk; clearing the buffer keeps the next
	// flush/finalize from inserting them again. Stats folds buffered and
	// stored rows together, so the dashboard stays continuous.
	s.pendingUsage = s.pendingUsage[:0]
	return nil
}

// replaceSessionSnapshot rewrites the session's stored messages and loaded
// skills as a complete snapshot. Both FlushSession and FinalizeSession use it
// so a checkpoint and the later exit save describe the same session instead
// of appending a duplicate history.
func replaceSessionSnapshot(tx *sql.Tx, sessionID int64, skills []string, messages []providers.Message, now string) error {
	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("db: clear messages: %w", err)
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
			sessionID, msg.Role, msg.Content, msg.ToolCallID, tcJSON, now); err != nil {
			return fmt.Errorf("db: insert message: %w", err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM session_skills WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("db: clear session skills: %w", err)
	}
	for _, name := range skills {
		if _, err := tx.Exec(`
			INSERT INTO session_skills (session_id, skill_name, loaded_at)
			VALUES (?, ?, ?)`,
			sessionID, name, now); err != nil {
			return fmt.Errorf("db: insert session skill: %w", err)
		}
	}
	return nil
}

// insertPendingUsage flushes the usage records buffered since the last
// checkpoint/finalize. The caller clears the buffer after committing.
func insertPendingUsage(tx *sql.Tx, sessionID int64, pending []UsageRecord) error {
	for _, u := range pending {
		ts := u.CreatedAt.Format(time.RFC3339)
		if _, err := tx.Exec(`
			INSERT INTO usage (session_id, model, provider, prompt_tokens,
			                   completion_tokens, total_tokens, tool_calls, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID, u.Model, u.Provider, u.PromptTokens,
			u.CompletionTokens, u.TotalTokens, u.ToolCalls, ts); err != nil {
			return fmt.Errorf("db: insert usage: %w", err)
		}
	}
	return nil
}
