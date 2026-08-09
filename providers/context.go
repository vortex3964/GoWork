// DESC: cheap local token estimation + context-window trimming helpers.

package providers

import "strings"

// EstimateOverheadTokensForModel estimates the fixed token cost every
// Generate request carries on top of the message list: the system prompt plus
// the registered tool schemas (both are re-sent each turn). Unknown models or
// unavailable tokenizers degrade to a chars/4 estimate.
func EstimateOverheadTokensForModel(model string) int {
	tokens := countTokensForModel(model, system_prompt)
	for _, td := range toolDefs() {
		tokens += countTokensForModel(model, td.Name)
		tokens += countTokensForModel(model, td.Description)
		tokens += countTokensForModel(model, string(td.InputSchema))
	}
	return tokens + 512
}

// EstimateMessageTokensForModel is a local token estimate for one message,
// including the JSON overhead of any tool calls it carries. It deliberately
// avoids the provider's EstimateTokens-style endpoints, which cost a network
// round-trip per turn. Unknown models or unavailable tokenizers degrade to a
// chars/4 estimate.
func EstimateMessageTokensForModel(m Message, model string) int {
	tokens := countTokensForModel(model, m.Content)
	tokens += countTokensForModel(model, m.ToolCallID)
	for _, tc := range m.ToolCalls {
		tokens += countTokensForModel(model, tc.Tool_name)
		tokens += countTokensForModel(model, string(tc.Input))
	}
	return tokens + 16
}

// EstimateContextTokens estimates what a full request for `messages` will
// cost: the fixed system+tools overhead plus every message. Used to resync
// the status line after a compaction.
func EstimateContextTokens(messages []Message, model string) int {
	total := EstimateOverheadTokensForModel(model)
	for _, m := range messages {
		total += EstimateMessageTokensForModel(m, model)
	}
	return total
}

// TrimContext returns a copy of messages bounded to fit within a window of
// `window` tokens, using the local char/4 estimator for the count.
// The first message (the user's original request) is always kept when the
// history is too big, whole assistant-tool-call exchanges are dropped from
// the middle, keeping the most recent exchanges. Dropping whole groups (an
// assistant tool_calls message glued to its tool results) keeps the remaining
// sequence valid for OpenAI-compatible / ollama chat APIs.
// The input slice is never mutated.
//
// window <= 0 means "unknown", in which case messages are returned unchanged.
func TrimContext(messages []Message, window int, model string) []Message {
	if window <= 0 || len(messages) <= 1 {
		return messages
	}

	budget := window - EstimateOverheadTokensForModel(model)
	if budget <= 0 {
		// Tiny window (or giant overhead): still try to keep the task plus
		// a handful of the most recent messages.
		budget = window / 4
	}
	if budget < 64 {
		budget = 64
	}

	first := messages[0]
	groups := groupMessages(messages[1:])

	sizes := make([]int, len(groups))
	used := EstimateMessageTokensForModel(first, model)
	for gi, g := range groups {
		s := 0
		for _, msg := range g {
			s += EstimateMessageTokensForModel(msg, model)
		}
		sizes[gi] = s
		used += s
	}

	if used <= budget {
		return messages
	}

	drop := 0
	for used > budget && drop < len(groups) {
		used -= sizes[drop]
		drop++
	}

	out := make([]Message, 0, len(messages)-drop)
	out = append(out, first)
	for _, g := range groups[drop:] {
		out = append(out, g...)
	}
	return out
}

// groupMessages splits a message list into groups where an assistant message
// with tool calls is kept glued to the tool-result messages that follow it.
// Everything else forms its own group. This is what lets trimming cut old
// tool exchanges out of the middle without breaking the call/result pairing.
func groupMessages(messages []Message) [][]Message {
	var groups [][]Message
	for i := 0; i < len(messages); {
		if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 {
			g := []Message{messages[i]}
			i++
			for i < len(messages) && messages[i].Role == "tool" {
				g = append(g, messages[i])
				i++
			}
			groups = append(groups, g)
			continue
		}
		groups = append(groups, []Message{messages[i]})
		i++
	}
	return groups
}

// the compaction prompt
var compaction_prompt string

// set the prompt
func InitCompactionPrompt(s string) {
	compaction_prompt = s
}

// Compaction tuning
const (
	// compactionBuffer is the default token reserve kept free above the
	// compaction threshold.
	compactionBuffer = 20000
	// compactionMinReserve stops small-window models from compacting too
	// late the threshold never sits closer than this to the window top.
	compactionMinReserve = 4096
	// defaultCompactionKeepTurns is how many recent user turns are kept
	// verbatim after a compaction.
	defaultCompactionKeepTurns = 2
	// compactToolOutputMax bounds each tool result sent into a compaction
	// call, so one huge output can't eat the window by itself.
	compactToolOutputMax = 4096
)

// ShouldCompact reports whether a request using promptTokens tokens
// has crossed into the compaction zone , unknown windows never compact
func ShouldCompact(window, promptTokens int) bool {
	if window <= 0 || promptTokens <= 0 {
		return false
	}
	reserved := compactionBuffer
	if r := window / 4; r < reserved {
		reserved = r
	}
	if reserved < compactionMinReserve {
		reserved = compactionMinReserve
	}
	return promptTokens >= window-reserved
}

// CompactionMessages builds the message list for a compaction call: the
// current history (bounded to the window) with oversized tool results
// truncated, followed by a final user message carrying the compaction prompt.
// todoState is an optional snapshot of the current todo list; it is appended
// to the compaction prompt so the summary's work-state reflects live tasks.
func CompactionMessages(messages []Message, window int, todoState ...string) []Message {
	history := TrimContext(messages, window, "")
	msgs := make([]Message, 0, len(history)+1)
	for _, msg := range history {
		m := msg
		if m.Role == "tool" && len(m.Content) > compactToolOutputMax {
			m.Content = m.Content[:compactToolOutputMax] + "\n…[tool output truncated]"
		}
		msgs = append(msgs, m)
	}
	prompt := compaction_prompt
	if prompt == "" {
		prompt = "Condense the conversation above into a summary for a coding agent. Keep it accurate and complete."
	}
	if len(todoState) > 0 && strings.TrimSpace(todoState[0]) != "" {
		prompt += "\n\n## Current todo list\n" + todoState[0] + "\nBase the Work State section on this list."
	}
	msgs = append(msgs, Message{Role: "user", Content: prompt})
	return msgs
}

// CompactContext replaces the message history with a summary of the earlier
// conversation a leading user message the input slice is never mutated. If the summary
// is empty, or there is nothing old enough to replace, the input is returned unchanged.
func CompactContext(messages []Message, summary string, keepTurns int) []Message {
	if strings.TrimSpace(summary) == "" {
		return messages
	}
	if keepTurns <= 0 {
		keepTurns = defaultCompactionKeepTurns
	}

	groups := groupMessages(messages)
	tailStart := 0
	seen := 0
	for i := len(groups) - 1; i >= 0 && seen < keepTurns; i-- {
		if groupHasUserTurn(groups[i]) {
			seen++
			tailStart = i
		}
	}
	if tailStart == 0 {
		// The whole history counts as "recent" - compaction can't help.
		return messages
	}

	out := make([]Message, 0, len(groups)-tailStart+1)
	out = append(out, Message{
		Role:    "user",
		Content: "The following is a summary of the earlier conversation. Read it as your starting context.\n\n" + summary,
	})
	for _, g := range groups[tailStart:] {
		out = append(out, g...)
	}
	return out
}

// groupHasUserTurn reports whether a message group contains a user-role
// message (i.e. whether it starts a conversational turn).
func groupHasUserTurn(g []Message) bool {
	for _, msg := range g {
		if msg.Role == "user" {
			return true
		}
	}
	return false
}
