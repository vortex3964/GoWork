package gowork

import (
	"fmt"
	"log"
	"strings"

	"GoWork/Tui/Components/MessageArea"

	"GoWork/providers"
	"GoWork/tools/SkillsTool"
)

// interruptedToolResult is the synthetic tool result written when a context
// repair has to close a tool call whose real result never arrived: the user
// quit while a permission prompt, questionnaire or running batch was still
// open, or an older session was saved with an unfinished batch. OpenAI,
// Anthropic and Gemini all reject a request whose assistant tool call has no
// matching result, so a synthetic result is the least destructive repair: it
// preserves the assistant's prose and the visible transcript.
const interruptedToolResult = "Tool call interrupted: the turn ended before this tool produced a result, so it did not run."

// repairContext makes a history safe to send to a provider. It pairs every
// assistant tool call with exactly one tool result in call order:
//   - a tool call whose result is missing gets a synthetic interrupted result
//     inserted directly after the call (so trailing calls on quit/max-steps
//     cannot leave an invalid conversation);
//   - a tool result with no preceding call to answer (or a duplicate result)
//     is dropped, because providers reject orphan tool messages too.
//
// Valid histories are returned unchanged (same slice) and report changed
// false. The input slice is never mutated.
func repairContext(messages []providers.Message) ([]providers.Message, bool) {
	out := make([]providers.Message, 0, len(messages)+2)
	pending := make([]string, 0, 2)
	changed := false

	flushPending := func() {
		for _, id := range pending {
			out = append(out, providers.Message{
				Role:       "tool",
				ToolCallID: id,
				Content:    interruptedToolResult,
			})
			changed = true
		}
		pending = pending[:0]
	}

	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			// Any call still pending here was never answered before the
			// conversation moved on; close it before this message.
			flushPending()
			out = append(out, msg)
			for _, tc := range msg.ToolCalls {
				pending = append(pending, tc.Tool_call_id)
			}
		case "tool":
			match := -1
			for i, id := range pending {
				if id == msg.ToolCallID {
					match = i
					break
				}
			}
			if match < 0 {
				// Orphan or duplicate result: no call is waiting for it.
				changed = true
				continue
			}
			pending = append(pending[:match], pending[match+1:]...)
			out = append(out, msg)
		default:
			flushPending()
			out = append(out, msg)
		}
	}
	flushPending()

	if !changed {
		return messages, false
	}
	return out, true
}

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

	// A session saved by an older build (or interrupted mid-batch) can carry
	// an assistant tool call with no result. Repair it before replaying so
	// both the transcript and the first request of this run are valid.
	messages, repaired := repairContext(ls.Messages)
	ls.Messages = messages

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
	if repaired {
		m.MessageArea.AppendMessage(
			">**INFO** Repaired an interrupted tool batch in the restored session so it can continue safely.",
			false,
		)
	}
	m.MessageArea.AppendMessage(
		fmt.Sprintf(">**INFO** Restored %d messages and %d skill(s) from session #%d.", len(ls.Messages), len(ls.Skills), ls.ID),
		false,
	)
}

// flushSession is the save-on-exit path: it finalizes the current session
// (writes usage + messages), closes the database, and stops every MCP client
// and language-server process the runtime owns. Fired from the program's quit
// filter when a QuitMsg arrives.
func (m model) flushSession() {
	m.closeToolClients()
	if m.Db == nil {
		return
	}
	// Quitting mid-batch (a running tool, an open permission prompt or a
	// questionnaire) leaves the last assistant tool call without results.
	// Save the repaired history so resuming this session can't send an
	// invalid conversation to the provider.
	messages, _ := repairContext(m.Context)
	if err := m.Db.FinalizeSession(messages, m.status.sessionTokens); err != nil {
		log.Printf("failed to save session: %v", err)
	}
	if err := m.Db.Close(); err != nil {
		log.Printf("failed to close database: %v", err)
	}
}
