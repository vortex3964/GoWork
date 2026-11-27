package gowork

import (
	"fmt"
	"log"
	"strings"

	"GoWork/Tui/Components/MessageArea"

	"GoWork/tools/SkillsTool"
)

// resumeSession restores a previous session into this app run: its message
// history is replayed into the message area and the model context, and every
// skill the session had loaded is loaded again so the available-skills list
// matches. Called at startup when GoWork is passed `-S <sessionID>`.
func (m *model) resumeSession(id int64) {
	if m.Db == nil {
		m.MessageArea.AppendMessage(">**ERROR** Database unavailable - cannot restore a previous session.", false)
		return
	}
	ls, err := m.Db.LoadSession(id)
	if err != nil {
		m.MessageArea.AppendMessage(">**ERROR** Could not restore session: "+err.Error(), false)
		return
	}
	if ls == nil {
		m.MessageArea.AppendMessage(fmt.Sprintf(">**ERROR** No stored session with id %d.", id), false)
		return
	}

	// Replay the stored history into the message area. Assistant tool calls
	// render as running rows; the matching "tool" role messages flick each
	// one to done with its stored result, in order.
	pendingTools := make([]int, 0, 4)
	for _, msg := range ls.Messages {
		switch msg.Role {
		case "user":
			m.MessageArea.AppendMessage(msg.Content, true)
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if strings.TrimSpace(msg.Content) != "" {
					m.MessageArea.AppendMessage(msg.Content, false)
				}
				for _, tc := range msg.ToolCalls {
					idx := m.MessageArea.AppendTool(tc.Tool_name, messagearea.SummarizeToolInput(tc.Input))
					pendingTools = append(pendingTools, idx)
				}
			} else {
				m.MessageArea.AppendMessage(msg.Content, false)
			}
		case "tool":
			if len(pendingTools) > 0 {
				idx := pendingTools[0]
				pendingTools = pendingTools[1:]
				m.MessageArea.UpdateToolMessage(idx, messagearea.ToolDone, msg.Content)
			}
		}
	}

	// Restore the session's loaded skills so they are visible to the model
	// again. Load() skips names that no longer exist on disk.
	mgr := skillstool.GetManager()
	if mgr != nil {
		for _, name := range ls.Skills {
			if !mgr.Load(name) {
				m.MessageArea.AppendMessage(fmt.Sprintf(">**INFO** Session's skill %q no longer exists on disk; skipped.", name), false)
			}
		}
		m.syncSkillsState()
	}

	m.Context = ls.Messages
	m.status.sessionTokens = ls.TotalTokens
	m.MessageArea.AppendMessage(
		fmt.Sprintf(">**INFO** Restored %d messages and %d skill(s) from session #%d.", len(ls.Messages), len(ls.Skills), ls.ID),
		false,
	)
}

// flushSession is the save-on-exit path: it finalizes the current session
// (writes usage + messages), then closes the database. Fired from the
// program's quit filter when a QuitMsg arrives.
func (m model) flushSession() {
	if m.Db == nil {
		return
	}
	if err := m.Db.FinalizeSession(m.Context, m.status.sessionTokens); err != nil {
		log.Printf("failed to save session: %v", err)
	}
	if err := m.Db.Close(); err != nil {
		log.Printf("failed to close database: %v", err)
	}
}
