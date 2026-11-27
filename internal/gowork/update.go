package gowork

import (
	"context"
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	changeslist "GoWork/Tui/Components/ChangesList"
	dialog "GoWork/Tui/Components/Dialog"
	"GoWork/Tui/Components/MessageArea"
	permisionbox "GoWork/Tui/Components/PermisionBox"
	popup "GoWork/Tui/Components/PopUp"
	"GoWork/Tui/Components/Promptbar"
	providerselect "GoWork/Tui/Components/ProviderSelect"
	"GoWork/Tui/Components/Questionnaire"
	"GoWork/Tui/Components/SkillForm"
	"GoWork/Tui/Components/SkillsTab"
	todo "GoWork/Tui/Components/ToDo"

	"GoWork/providers"
	"GoWork/tools"
	"GoWork/tools/QuestionnaireTool"
	"GoWork/tools/SkillsTool"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case promptbar.HeightChangedMsg:
		// The prompt bar grew/shrunk (typing past the 2-line resting size,
		// the command palette opening/closing, or back down on reset):
		// shrink or free the band above it so the message area and the
		// skills pane keep fitting.
		m.promptHeight = m.Prompt.Height()
		m.applyPanelSize()
		return m, nil

	case tea.WindowSizeMsg:
		m.winWidth = msg.Width
		m.winHeight = msg.Height
		m.Tabs.SetSize(m.winWidth)
		m.Prompt.SetWidth(m.winWidth)
		m.syncPromptHeight()

		// message area fills everything between the tabs and the prompt
		// bar, minus the reserved spinner row and the statusline row.
		msgAreaHeight := m.winHeight - topBarHeight - spinnerHeight - statusLineHeight - m.bottomBarHeight()
		if msgAreaHeight < 0 {
			msgAreaHeight = 0
		}
		m.MessageArea.SetSize(m.msgAreaWidth(), msgAreaHeight)

		// The skills pane uses the very same band as the message area: the
		// prompt bar and the statusline stay pinned at the bottom, so the
		// pane can never push them off-screen.
		m.Skills.SetSize(m.msgAreaWidth(), msgAreaHeight)

		// Stats keeps its full-height dashboard: it renders its own
		// statusline on its last row.
		contentHeight := m.winHeight - topBarHeight
		if contentHeight < 0 {
			contentHeight = 0
		}
		m.Stats.SetSize(m.winWidth, contentHeight)
		m.skillForm.SetSize(m.winWidth, m.winHeight)
		m.ChangesList.SetSize(m.winWidth, msgAreaHeight)
		m.popUp.SetSize(m.winWidth, m.winHeight)
		m.modal.SetSize(m.winWidth, m.winHeight)
		m.Coffee.SetSize(m.winWidth, m.winHeight)
		m.questionnaire.SetWidth(m.winWidth)
		m.permissionBox.SetSize(m.winWidth, m.winHeight)
		if m.Mode == modeProviderSelect {
			m.providerSelect.SetSize(m.winWidth, m.winHeight)
		}
		return m, nil

	case providerselect.SelectedMsg:
		return m.handleProviderSelected(msg)

	case providerselect.CancelledMsg:
		m.restoreModeForActiveTab()
		return m, nil

	case skills.SkillToggleMsg:
		mgr := skillstool.GetManager()
		if mgr == nil {
			return m, nil
		}
		if msg.Unload {
			mgr.Unload(msg.Name)
		} else if !mgr.Load(msg.Name) {
			m.MessageArea.AppendMessage(">**ERROR** No such skill on disk: "+msg.Name, false)
			return m, nil
		}
		m.syncSkillsState()
		return m, nil

	case skillform.SubmitMsg:
		mgr := skillstool.GetManager()
		if mgr == nil {
			return m, nil
		}
		name := skillstool.Slugify(msg.Name)
		body := m.Prompt.Value()
		created, err := mgr.Create(name, msg.Description, body)
		if err != nil {
			m.MessageArea.AppendMessage(">**ERROR** "+err.Error(), false)
			return m, nil
		}
		// A freshly created skill is the user's current intent, so it joins
		// the session's loaded set right away.
		_ = mgr.Load(name)
		m.Prompt.Reset()
		m.syncPromptHeight()
		m.historyIdx = 0
		m.syncSkillsState()
		m.MessageArea.AppendMessage(
			fmt.Sprintf(">**INFO** Skill %q created and loaded for this session.", created.Name),
			false,
		)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyPressMsg(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClickMsg(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheelMsg(msg)
	case tea.MouseMotionMsg:
		return m.handleMouseMotionMsg(msg)
	case tea.MouseReleaseMsg:
		return m.handleMouseReleaseMsg(msg)
	case spinner.TickMsg:
		// Only keep re-ticking while we're actually waiting on a response,
		// otherwise the spinner would spin forever in the background.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if *m.AiThink {
			return m, cmd
		}
		return m, nil
	case changeslist.WatcherEventMsg:
		if m.ChangesList.Open() && !*m.AiThink {
			if m.ChangesList.HandleWatcherEvent(msg.FilePath) {
				m.ChangesList.RebuildRows()
			}
		}
		return m, m.ChangesList.WatchCmd()
	case changeslist.ChangeEventMsg:
		// Accept/reject summaries surface in the message area, rendered as
		// markdown (blockquote + bold + code) so they stand out from the
		// surrounding conversation.
		m.MessageArea.AppendMessage(changeEventMarkdown(msg), false)
		return m, nil
	case streamDeltaMsg:
		// A chunk of streamed assistant text: append it to the live message
		// and re-arm the reader for the next chunk.
		if m.liveActive {
			m.liveContent += msg.delta
			m.MessageArea.LiveAssistantUpdate(m.liveMsgIdx, m.liveContent)
			return m, streamReadCmd(m.streamCh)
		}
		return m, nil
	case streamEndMsg:
		m.liveActive = false
		if m.ChangesList.Open() {
			m.ChangesList.RefreshDiffs()
		}

		// Token accounting. Exact usage arrives in the stream's final chunk;
		// an interrupted stream (ctrl+i) never receives it, and a few older
		// servers don't report usage at all. In both cases fall back to a
		// local estimate so the session counter and window-fill % stay
		// continuous instead of zeroing out.
		usage := msg.result.Usage
		if msg.err == nil || isCancelError(msg.err) {
			if usage.TotalTokens == 0 {
				prompt := providers.EstimateContextTokens(m.Context, m.status.modelID)
				completion := len(m.liveContent)/4 + 1
				usage = providers.Usage{
					PromptTokens:     prompt,
					CompletionTokens: completion,
					TotalTokens:      prompt + completion,
				}
			}
			m.status.sessionTokens += usage.TotalTokens
			m.status.lastPromptTokens = usage.PromptTokens
		}

		// Record the generation in the local database (buffered, flushed on
		// exit) so the stats screen can aggregate model usage.
		if m.Db != nil {
			m.Db.RecordUsage(m.status.modelID, m.status.providerName,
				usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
				len(msg.result.ToolCalls), time.Now())
		}

		// The user pressed ctrl+i. Even if the provider ignored the cancel
		// and returned happily, we honour the interrupt and stop here rather
		// than launching another model turn or more tools.
		if m.stopRequested {
			if m.liveMsgIdx >= 0 {
				m.MessageArea.LiveAssistantFinish(m.liveMsgIdx, m.liveContent)
			}
			m.endTurn()
			m.MessageArea.AppendMessage(">**INFO** Generation stopped.", false)
			return m, nil
		}
		if msg.err != nil {
			if m.liveMsgIdx >= 0 {
				m.MessageArea.LiveAssistantFinish(m.liveMsgIdx, m.liveContent)
			}
			m.endTurn()
			var perr *providers.ProviderError
			if errors.As(msg.err, &perr) {
				switch perr.Kind {
				case providers.ErrRateLimited:
					m.MessageArea.AppendMessage("Rate limited - try again in a moment.", false)
				case providers.ErrAuthFailed:
					m.MessageArea.AppendMessage("Auth failed - "+authFailureDetail(perr), false)
				case providers.ErrContextExceeded:
					m.MessageArea.AppendMessage("Context window exceeded - try trimming history.", false)
				case providers.ErrCanceled:
					m.MessageArea.AppendMessage(">**INFO** Generation stopped.", false)
				default:
					m.MessageArea.AppendMessage("Error: "+perr.Error(), false)
				}
			} else {
				m.MessageArea.AppendMessage("Error: "+msg.err.Error(), false)
			}
			if m.ChangesList.Open() {
				return m, m.ChangesList.WatchCmd()
			}
			return m, nil
		}

		// The stream is over: the live message still holds the plain-text
		// streaming form. Render the canonical final content through glamour
		// once here, after any text-embedded tool call has been stripped.
		content := msg.result.Content

		// Some models (often small local ones) print a tool call as a JSON
		// blob in their text instead of emitting a native tool_calls block.
		// If the model reported no tool calls but its text parses into a
		// known tool call, treat it as one anyway.
		toolCalls := msg.result.ToolCalls
		if len(toolCalls) == 0 && content != "" {
			if parsed, clean := providers.ParseTextToolCalls(content); len(parsed) > 0 {
				toolCalls = parsed
				content = clean
			}
		}

		if m.liveMsgIdx >= 0 {
			m.MessageArea.LiveAssistantFinish(m.liveMsgIdx, content)
		} else if content != "" {
			m.MessageArea.AppendMessage(content, false)
		}

		if len(toolCalls) > 0 {
			// The assistant asked for tools: run them, feed the results
			// back into context, then call the model again. aiThink stays
			// true so the spinner keeps running and submitPrompt won't
			// launch a concurrent generation; the prompt bar itself stays
			// focused and usable.

			// Cap the batch. A runaway model that keeps re-emitting the same
			// calls must not spin forever: only the first maxToolsPerTurn
			// calls of this turn are executed, the rest are dropped. The
			// assistant message below carries exactly the calls that will
			// get results, so the conversation stays well-formed.
			if len(toolCalls) > maxToolsPerTurn {
				m.MessageArea.AppendMessage(
					fmt.Sprintf(">**INFO** Model requested %d tool calls; running the first %d this turn.", len(toolCalls), maxToolsPerTurn),
					false,
				)
				toolCalls = toolCalls[:maxToolsPerTurn]
			}

			m.Context = append(m.Context, providers.Message{
				Role: "assistant", Content: content, ToolCalls: toolCalls,
			})

			// Max-steps guardrail so a model stuck re-calling tools can't loop forever.
			m.stepCount++
			if m.stepCount > m.maxSteps {
				m.endTurn()
				m.MessageArea.AppendMessage(
					">**ERROR** Reached the maximum number of agent turns. If you want, ask me to continue with the remaining steps.",
					false,
				)
				return m, nil
			}

			// Render each requested call as a "running" row, then execute
			// the whole batch in parallel - the update loop collects
			// results as they arrive and only returns to the model once
			// every slot is filled. The batch gets its own cancellable
			// context so ctrl+i can abort in-flight tools.
			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			m.stopRequested = false
			m.batchCtx = ctx
			m.pendingTools = m.pendingTools[:0]
			m.pendingResults = make([]*tools.ToolResult, len(toolCalls))
			m.pendingNotes = make([]string, len(toolCalls))
			m.permissionDecided = make([]bool, len(toolCalls))
			m.permissionSlot = -1
			for _, tc := range toolCalls {
				idx := m.MessageArea.AppendTool(tc.Tool_name, messagearea.SummarizeToolInput(tc.Input))
				m.pendingTools = append(m.pendingTools, pendingTool{call: tc, messageIdx: idx})
			}

			// Pause the batch and open the permisionbox if any call needs permission
			// (and the user hasn't auto-approved everything).
			if slot := m.nextPendingPermissionSlice(); slot >= 0 {
				return m, m.openPermissionBox(slot)
			}
			return m, m.dispatchPendingBatch()
		}

		// Final answer - no more tool calls to run.
		if content != "" {
			m.Context = append(m.Context, providers.Message{Role: "assistant", Content: content})
		}

		// Context window nearly full: condense the conversation before the
		// user can ask anything else, instead of old exchanges silently
		// dropping out of the window. aiThink stays true while the
		// compaction runs, so the spinner keeps going and no new prompt can
		// batch up behind it.
		if m.shouldCompact() {
			m.MessageArea.AppendMessage(">**INFO** Context window nearly full - condensing the conversation...", false)
			var p providers.Provider
			if m.model != nil {
				p = m.model.Get()
			}
			if p == nil {
				m.endTurn()
				return m, nil
			}
			return m, compactCmd(p, m.Context, m.status.contextWindow, todo.GetTodoList().String())
		}

		m.endTurn()
		if m.ChangesList.Open() {
			return m, m.ChangesList.WatchCmd()
		}
		return m, nil
	case compactResultMsg:
		if msg.err != nil {
			m.endTurn()
			m.MessageArea.AppendMessage(">**ERROR** Context compaction failed: "+msg.err.Error(), false)
			return m, nil
		}
		m.status.sessionTokens += msg.usage.TotalTokens
		if m.Db != nil {
			m.Db.RecordUsage(m.status.modelID, m.status.providerName,
				msg.usage.PromptTokens, msg.usage.CompletionTokens,
				msg.usage.TotalTokens, 0, time.Now())
		}
		m.Context = providers.CompactContext(m.Context, msg.summary, 2)
		m.status.lastPromptTokens = providers.EstimateContextTokens(m.Context, m.status.modelID)
		m.MessageArea.AppendMessage(">**INFO** Conversation condensed to fit the context window - continue when ready.", false)
		m.endTurn()
		if m.ChangesList.Open() {
			return m, m.ChangesList.WatchCmd()
		}
		return m, nil
	case toolResultMsg:
		if msg.index < 0 || msg.index >= len(m.pendingTools) {
			return m, nil
		}
		pt := m.pendingTools[msg.index]

		// The questions_tool parks the ai here: hold its batch slot open
		// and render the questionnaire in place of the prompt bar until
		// the user answers. aiThink stays true so the model can't continue.
		if pt.call.Tool_name == questionnairetool.ToolName &&
			questionnaire.Active() != nil && m.questionnaireSlot == -1 && !m.stopRequested {
			m.questionnaireSlot = msg.index
			m.Mode = modeQuestionnaire
			m.questionnaire = questionnaire.New(questionnaire.Active())
			m.questionnaire.SetWidth(m.winWidth)
			m.applyPanelSize()
			return m, nil
		}

		if msg.result.IsError {
			m.MessageArea.UpdateToolMessage(pt.messageIdx, messagearea.ToolError, msg.result.Content)
		} else {
			m.MessageArea.UpdateToolMessage(pt.messageIdx, messagearea.ToolDone, msg.result.Content)
		}

		// A note the user typed while granting rides along with the tool's output
		res := msg.result
		if msg.index < len(m.pendingNotes) && m.pendingNotes[msg.index] != "" {
			res.Content = ">**Allowed with a note from the user:**\n" + m.pendingNotes[msg.index] + "\n\n" + res.Content
			if res.IsError {
				m.MessageArea.UpdateToolMessage(pt.messageIdx, messagearea.ToolError, res.Content)
			} else {
				m.MessageArea.UpdateToolMessage(pt.messageIdx, messagearea.ToolDone, res.Content)
			}
			m.pendingNotes[msg.index] = ""
		}

		// Stash the result. Results are appended to context in call order
		// once the whole batch reports back, keeping the assistant's
		// tool_calls paired with their answers.
		if msg.index < len(m.pendingResults) {
			m.pendingResults[msg.index] = &res
		}
		if m.ChangesList.Open() {
			m.ChangesList.RefreshDiffs()
		}

		// Still waiting on other calls in the batch.
		for _, r := range m.pendingResults {
			if r == nil {
				return m, nil
			}
		}

		return m, m.loopAfterBatch()
	case permisionbox.DecisionMsg:
		if m.Mode == modePermission {
			return m, m.resolvePermission(msg)
		}
		return m, nil
	case questionnaire.DoneMsg:
		if msg.Cancelled {
			m.answerQuestionnaire(questionnairetool.FormatCancelled())
		} else {
			m.answerQuestionnaire(questionnairetool.FormatAnswers(m.questionnaire.Questions(), msg.Answers))
		}
		for _, r := range m.pendingResults {
			if r == nil {
				return m, nil
			}
		}
		return m, m.loopAfterBatch()
	case modelInfoMsg:
		if msg.err == nil {
			m.status.contextWindow = msg.info.ContextWindow
		}
		return m, nil

	case popup.ShowMsg:
		var cmd tea.Cmd
		m.popUp, cmd = m.popUp.Update(msg)
		cmds = append(cmds, cmd)

	case dialog.ShowMsg:
		var cmd tea.Cmd
		m.modal, cmd = m.modal.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m.forwardToComponents(msg)
}

// forwardToComponents forwards a message that Update didn't consume to
// the tab strip, prompt bar, message area, popup and (on the stats tab)
// the stats screen, batching their commands.
func (m model) forwardToComponents(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.Mode == modeProviderSelect {
		var cmd tea.Cmd
		m.providerSelect, cmd = m.providerSelect.Update(msg)
		return m, cmd
	}

	var tabsCmd tea.Cmd
	m.Tabs, tabsCmd = m.Tabs.Update(msg)
	// The prompt bar's cursor blink runs on its own message loop
	// (cursor.BlinkMsg) that doesn't match any case above, so it has to
	// be forwarded here too or the cursor blinks once and then freezes.
	// The message area's viewport has the same needs (e.g. its own
	// internal key/mouse handling), so it gets forwarded the same way.
	var promptCmd tea.Cmd
	m.Prompt, promptCmd = m.Prompt.Update(msg)
	var msgAreaCmd tea.Cmd
	m.MessageArea, msgAreaCmd = m.MessageArea.Update(msg)
	var popUpCmd tea.Cmd
	m.popUp, popUpCmd = m.popUp.Update(msg)
	// The stats screen's filter cursor blinks on the same loop; forward the
	// leftover messages while the stats tab is live so it stays responsive.
	var statsCmd tea.Cmd
	if m.Tabs.Active().Name == "stats" {
		m.Stats, statsCmd = m.Stats.Update(msg)
	}
	// Coffee's animation tick is an opaque message; forward everything left
	// over so the overlay advances even when other modes ignore it.
	var coffeeCmd tea.Cmd
	m.Coffee, coffeeCmd = m.Coffee.Update(msg)
	return m, tea.Batch(tabsCmd, promptCmd, msgAreaCmd, popUpCmd, statsCmd, coffeeCmd)
}
