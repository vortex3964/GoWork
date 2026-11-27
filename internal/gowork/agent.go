package gowork

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"GoWork/Tui/Components/MessageArea"
	permisionbox "GoWork/Tui/Components/PermisionBox"
	"GoWork/Tui/Components/Questionnaire"
	todo "GoWork/Tui/Components/ToDo"

	"GoWork/providers"
	"GoWork/tools"
)

// streamDeltaMsg carries a new chunk of assistant text while a generation is
// streaming; it's what makes the message area update token-by-token.
type streamDeltaMsg struct {
	delta string
}

// streamEndMsg is sent once (and only once) when a streamed generation
// finishes - success or error - carrying the fully assembled result.
type streamEndMsg struct {
	result providers.GenerateResult
	err    error
}

// toolResultMsg reports a single finished tool call back to the update loop.
// index points at the pending tool it belongs to (into model.pendingTools).
type toolResultMsg struct {
	index  int
	result tools.ToolResult
}

type modelInfoMsg struct {
	info providers.ModelInfo
	err  error
}

// compactResultMsg reports the outcome of a context-compaction call: the
// model's summary of the earlier conversation, plus the tokens the summary
// itself cost.
type compactResultMsg struct {
	summary string
	usage   providers.Usage
	err     error
}

// pendingTool is one assistant-requested tool call waiting to run, paired
// with the message-area index its status line is rendered at.
type pendingTool struct {
	call       providers.ToolCall
	messageIdx int
}

// streamFlushInterval is how often accumulated assistant text is handed to
// the update loop while a generation streams.
const streamFlushInterval = 80 * time.Millisecond

// startStreaming spawns a goroutine that runs a streaming generation on ctx
// (which the caller owns so ctrl+i can cancel it) and forwards chunks into ch.
// Deltas are coalesced on a real time interval (streamFlushInterval) rather
// than a byte count, so a fast model can't flood bubbletea with one message
// per token while a slow one still feels live. Whatever is still buffered,
// including the final short chunk, is flushed once when the stream ends; then
// a single streamEndMsg is sent and ch is closed.
func startStreaming(ctx context.Context, p providers.Provider, messages []providers.Message, ch chan<- tea.Msg) {
	go func() {
		var buf strings.Builder
		lastFlush := time.Now()
		flush := func() {
			if buf.Len() == 0 {
				return
			}
			ch <- streamDeltaMsg{delta: buf.String()}
			buf.Reset()
			lastFlush = time.Now()
		}

		res, err := p.GenerateStream(ctx, messages, func(delta string) {
			buf.WriteString(delta)
			if time.Since(lastFlush) >= streamFlushInterval {
				flush()
			}
		})
		flush() // any trailing partial chunk
		ch <- streamEndMsg{result: res, err: err}
		close(ch)
	}()
}

// streamReadCmd returns the next message from the streaming channel. A nil
// msg means the channel closed (stream finished).
func streamReadCmd(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// runToolCmd executes a single tool call off the update loop. Panics inside a
// tool are recovered and surfaced as an error result instead of killing the
// whole app . Running the calls of a batch in parallel is what the caller
// arranges by issuing several of these together via tea.Batch
func runToolCmd(ctx context.Context, d *tools.Dispatcher, call providers.ToolCall, index int) tea.Cmd {
	return func() tea.Msg {
		var res tools.ToolResult
		func() {
			defer func() {
				if r := recover(); r != nil {
					res = tools.Errf("tool %s panicked: %v", call.Tool_name, r)
				}
			}()
			res = d.Dispach(ctx, tools.ToolUse{Name: call.Tool_name, Input: call.Input})
		}()
		return toolResultMsg{index: index, result: res}
	}
}

// nextPendingPermissionSlice returns the first batch slot that still needs a
// user decision, or -1 when every call can run without asking.
func (m *model) nextPendingPermissionSlice() int {
	for i := range m.pendingTools {
		if i < len(m.pendingResults) && m.pendingResults[i] != nil {
			continue
		}
		if i < len(m.permissionDecided) && m.permissionDecided[i] {
			continue
		}
		if m.AutoAcceptTools {
			continue
		}
		if m.planMode && !tools.PlanModeAllows(m.pendingTools[i].call.Tool_name, m.pendingTools[i].call.Input) {
			// The dispatcher will refuse this call in plan mode. Don't ask
			// the user to approve an action that cannot run: let it fall
			// through to the dispatcher's model-readable refusal instead.
			continue
		}
		tool, ok := m.tool_dispatcher.Lookup(m.pendingTools[i].call.Tool_name)
		if !ok || tool.Kind() == tools.KindAllowed {
			continue
		}
		return i
	}
	return -1
}

// openPermissionBox swaps into modePermission and shows the permisionbox for
// the given slot. The batch stays frozen until the user answers.
func (m *model) openPermissionBox(slot int) tea.Cmd {
	pt := m.pendingTools[slot]
	name := pt.call.Tool_name
	action := messagearea.SummarizeToolInput(pt.call.Input)
	desc := "no description provided"
	if tool, ok := m.tool_dispatcher.Lookup(name); ok {
		desc = tool.Description()
	}
	m.permissionSlot = slot
	m.Mode = modePermission
	m.permissionBox = permisionbox.New(name, desc, action, explainToolImpact(name, pt.call.Input, m.root))
	m.permissionBox.SetSize(m.winWidth, m.winHeight)
	return nil
}

// resolvePermission applies the user's decision, then asks about the next
// pending call or dispatches the whole batch.
func (m *model) resolvePermission(dec permisionbox.DecisionMsg) tea.Cmd {
	slot := m.permissionSlot
	if slot < 0 || slot >= len(m.pendingTools) {
		m.Mode = modePrompt
		m.permissionSlot = -1
		m.Prompt.Focus()
		return nil
	}

	pt := m.pendingTools[slot]

	// A rejected tool never runs, its result is the denial the model reads.
	if dec.Type == permisionbox.ActionReject {
		reason := strings.TrimSpace(dec.Reason)
		if reason == "" {
			reason = "no reason given"
		}
		res := tools.ToolResult{
			Content: fmt.Sprintf(
				">**PERMISSION DENIED** Tool `%s` was rejected by the user and did not run.\nUser reason: %s",
				pt.call.Tool_name, reason,
			),
			IsError: true,
		}
		m.pendingResults[slot] = &res
		m.MessageArea.UpdateToolMessage(pt.messageIdx, messagearea.ToolError, res.Content)
	}

	if dec.Type == permisionbox.ActionAcceptAll {
		m.AutoAcceptTools = true
	}

	if dec.Reason != "" && slot < len(m.pendingNotes) {
		m.pendingNotes[slot] = dec.Reason
	}

	m.permissionDecided[slot] = true

	// Any more calls waiting on a decision?
	if next := m.nextPendingPermissionSlice(); next >= 0 {
		m.openPermissionBox(next)
		return nil
	}

	m.permissionSlot = -1
	m.permissionBox.Hide()
	m.Mode = modePrompt
	m.Prompt.Focus()
	if cmd := m.dispatchPendingBatch(); cmd != nil {
		return cmd
	}
	// Everything in the batch was resolved without a runnable tool, so finalize.
	return m.loopAfterBatch()
}

// dispatchPendingBatch launches every pending tool that has no result yet.
// Rejected calls never run. Uses the batch's own context so the turn stays
// cancellable.
func (m *model) dispatchPendingBatch() tea.Cmd {
	var cmds []tea.Cmd
	for i, pt := range m.pendingTools {
		if m.pendingResults[i] != nil {
			continue
		}
		cmds = append(cmds, runToolCmd(m.batchCtx, m.tool_dispatcher, pt.call, i))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// contextForModel returns the message history we actually send to the
// provider on this turn. Full history is kept in m.Context for future turns;
// this bounds it to the model's context window (when known) so the original
// user request is never the thing that gets dropped when the window fills.
func (m *model) contextForModel() []providers.Message {
	return providers.TrimContext(m.Context, m.status.contextWindow, m.status.modelID)
}

// shouldCompact reports whether the provider-reported prompt usage from the
// last request has crossed into the compaction zone for this model's context
// window.
func (m *model) shouldCompact() bool {
	return providers.ShouldCompact(m.status.contextWindow, m.status.lastPromptTokens)
}

func isCancelError(err error) bool {
	var perr *providers.ProviderError
	return errors.As(err, &perr) && perr.Kind == providers.ErrCanceled
}

// compactCmd runs a context-compaction call off the update loop: the model
// condenses the conversation into a summary that then replaces the old
// history (see compactResultMsg). It reads m's state once up front and never
// touches the model from this goroutine. todoState snapshots the live todo
// list so the summary's Work State can reflect outstanding tasks.
func compactCmd(p providers.Provider, messages []providers.Message, window int, todoState ...string) tea.Cmd {
	return func() tea.Msg {
		// The compaction call only needs history + the compaction prompt:
		// WithoutSystem makes providers skip the system prompt and tool
		// schemas, which are useless for summarising and would waste tokens.
		ctx, cancel := context.WithCancel(providers.WithoutSystem(context.Background()))
		defer cancel()
		res, err := p.Generate(ctx, providers.CompactionMessages(messages, window, todoState...))
		if err != nil {
			return compactResultMsg{err: err}
		}
		return compactResultMsg{summary: res.Content, usage: res.Usage}
	}
}

// endTurn resets all agentic-loop state and returns the app to idle, focusing
// the prompt. Used when a turn finishes, errors, or is interrupted. It also
// cancels any still-live context (harmless if the work already wound down).
func (m *model) endTurn() {
	// A turn ending while the questionnaire is still up must drop it so the
	// mode can't stay stuck answering a turn that's already gone.
	if m.Mode == modeQuestionnaire {
		m.Mode = modePrompt
		m.questionnaire = questionnaire.New(nil)
		m.questionnaireSlot = -1
		questionnaire.Clear()
		m.applyPanelSize()
	}
	*m.AiThink = false
	m.stepCount = 0
	m.pendingTools = m.pendingTools[:0]
	m.pendingResults = m.pendingResults[:0]
	m.liveActive = false
	m.stopRequested = false
	// "Accept All" applies to the batch it was chosen for, not the rest of
	// the session: every new turn starts with permission prompts active.
	m.AutoAcceptTools = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.Prompt.Focus()
}

// isTurnInterrupt reports whether a keypress should stop the in-flight turn.
// A terminal sends ctrl+i as tab (0x09), so while a turn is running both
// spellings interrupt; when idle, tab must keep switching tabs.
func isTurnInterrupt(key string, turnRunning bool) bool {
	return turnRunning && (key == "ctrl+i" || key == "tab")
}

// answerQuestionnaire delivers the user's answers into the held batch slot,
// clears the questionnaire, and returns the app to the prompt layout.
func (m *model) answerQuestionnaire(content string) {
	idx := m.questionnaireSlot
	if idx < 0 || idx >= len(m.pendingTools) || idx >= len(m.pendingResults) {
		return
	}
	res := tools.Ok(content)
	m.pendingResults[idx] = &res
	pt := m.pendingTools[idx]
	m.MessageArea.UpdateToolMessage(pt.messageIdx, messagearea.ToolDone, content)
	m.questionnaireSlot = -1
	m.questionnaire = questionnaire.New(nil)
	questionnaire.Clear()
	m.Mode = modePrompt
	m.applyPanelSize()
}

// loopAfterBatch is the shared tail of the tool-batch path: every call in
// the batch has answered, so append its results to context and call the
// model again with the full history.
func (m *model) loopAfterBatch() tea.Cmd {
	for i, pt := range m.pendingTools {
		m.Context = append(m.Context, providers.Message{
			Role: "tool", ToolCallID: pt.call.Tool_call_id, Content: toolOutputForContext(m.pendingResults[i].Content),
		})
	}
	m.pendingTools = m.pendingTools[:0]
	m.pendingResults = m.pendingResults[:0]
	m.cancel = nil

	if m.stopRequested {
		m.endTurn()
		m.MessageArea.AppendMessage(">**INFO** Turn stopped.", false)
		return nil
	}

	var p providers.Provider
	if m.model != nil {
		p = m.model.Get()
	}
	if p == nil {
		m.endTurn()
		m.MessageArea.AppendMessage(">**ERROR** No provider selected press ctrl+p to pick one.", false)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.stopRequested = false
	m.streamCh = make(chan tea.Msg, 16)
	m.liveActive = true
	m.liveContent = ""
	m.liveMsgIdx = m.MessageArea.LiveAssistantStart()
	startStreaming(ctx, p, m.contextForModel(), m.streamCh)
	return streamReadCmd(m.streamCh)
}

// toolOutputForContext bounds how much of a tool's output gets written back
// into context so a single huge result can't blow the window
func toolOutputForContext(content string) string {
	const maxChars = 24 * 1024
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "\n…[tool output truncated - too long to keep in context]"
}

// submitPrompt sends whatever's currently in the prompt bar as a user
// message (if non-empty), resets the prompt and history state, and kicks
// off the AI generation.
func (m *model) submitPrompt() []tea.Cmd {
	var cmds []tea.Cmd
	val := m.Prompt.Value()
	if val == "" {
		return cmds
	}
	// Block submitting a second request while one is still running (the
	// agentic loop can hold aiThink true across many tool turns, so this
	// guards against launching concurrent generations).
	if *m.AiThink {
		m.MessageArea.AppendMessage(">**INFO** Still working - wait for the current turn to finish.", false)
		return cmds
	}
	var p providers.Provider
	if m.model != nil {
		p = m.model.Get()
	}
	if p == nil {
		m.MessageArea.AppendMessage(">**ERROR** No provider selected press ctrl+p to pick one.", false)
		m.Prompt.Reset()
		m.syncPromptHeight()
		m.historyIdx = 0
		m.Mode = modeIdle
		m.Prompt.Blur()
		return cmds
	}
	*m.AiThink = true
	if m.ChangesList.Open() {
		m.ChangesList.PauseWatching()
	}
	// Each new task starts with a fresh todo list. The model lays down its
	// own baseline with the todo_list tool from the system prompt.
	todo.GetTodoList().Clear()
	m.MessageArea.AppendMessage(val, true)
	m.Context = append(m.Context, providers.Message{Role: "user", Content: val})
	m.Prompt.Reset()
	m.syncPromptHeight()
	m.historyIdx = 0
	m.stepCount = 0
	m.pendingTools = m.pendingTools[:0]
	m.pendingResults = m.pendingResults[:0]

	// Start a streamed generation. An empty assistant message is created now
	// so deltas can be written straight into it. The context is owned here so
	// ctrl+i can cancel the turn from the update loop.
	cmds = append(cmds, m.beginStream(p)...)
	return cmds
}

// beginStream starts a streamed generation with the current context and
// returns the commands that pump it.
func (m *model) beginStream(p providers.Provider) []tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.stopRequested = false
	m.streamCh = make(chan tea.Msg, 16)
	m.liveActive = true
	m.liveContent = ""
	m.liveMsgIdx = m.MessageArea.LiveAssistantStart()
	startStreaming(ctx, p, m.contextForModel(), m.streamCh)
	return []tea.Cmd{streamReadCmd(m.streamCh), m.spinner.Tick}
}
