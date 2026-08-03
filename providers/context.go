// DESC: cheap local token estimation + context-window trimming helpers.
//
// Every request to a provider re-sends the system prompt, the full tool
// definition set, and the whole message history. Local models (and small
// context windows) drop the *head* of an overflowing prompt - which is the
// user's original request - so the agent "forgets what it was asked". These
// helpers let main bound what gets sent while always keeping the first user
// message.

package providers

// EstimateOverheadTokensForModel estimates the fixed token cost every
// Generate request carries on top of the message list: the system prompt plus
// the registered tool schemas (both are re-sent each turn). Counted with the
// model's BPE tokenizer, plus a small constant for JSON structure.
func EstimateOverheadTokensForModel(model string) int {
	tokens := countTokensForModel(model, system_prompt)
	for _, td := range tools_def {
		tokens += countTokensForModel(model, td.Name)
		tokens += countTokensForModel(model, td.Description)
		tokens += countTokensForModel(model, string(td.InputSchema))
	}
	return tokens + 512
}

// EstimateMessageTokensForModel is a local token estimate for one message
// using the model's BPE tokenizer, including the JSON overhead of any tool
// calls it carries. It deliberately avoids the provider's EstimateTokens
// endpoint, which costs a network round-trip per turn.
func EstimateMessageTokensForModel(m Message, model string) int {
	tokens := countTokensForModel(model, m.Content)
	tokens += countTokensForModel(model, m.ToolCallID)
	for _, tc := range m.ToolCalls {
		tokens += countTokensForModel(model, tc.Tool_name)
		tokens += countTokensForModel(model, string(tc.Input))
	}
	return tokens + 16
}

// TrimContext returns a copy of messages bounded to fit within a window of
// `window` tokens, using the BPE tokenizer matching `model` for the count.
// The first message (the user's original request) is always kept; when the
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
