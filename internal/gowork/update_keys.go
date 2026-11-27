package gowork

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	popup "GoWork/Tui/Components/PopUp"

	"GoWork/tools/QuestionnaireTool"
)

func (m model) handleKeyPressMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	msg_str := msg.String()

	// The coffee animation is an interruptible overlay: it consumes every
	// key while visible so esc/ctrl+c close it instead of quitting the app
	// or leaking the key into the prompt.
	if m.Coffee.IsVisible() {
		var cmd tea.Cmd
		m.Coffee, cmd = m.Coffee.Update(msg)
		return m, cmd
	}

	if msg_str == "ctrl+c" {
		if m.Mode == modeProviderSelect {
			var cmd tea.Cmd
			m.providerSelect, cmd = m.providerSelect.Update(msg)
			return m, cmd
		}
		return m, tea.Quit
	}

	// While a permission request is up, every key goes to the permisionbox
	// (except ctrl+c above, which still quits).
	if m.Mode == modePermission {
		var cmd tea.Cmd
		m.permissionBox, cmd = m.permissionBox.Update(msg)
		return m, cmd
	}

	if msg_str == "ctrl+p" {
		if m.Mode == modeProviderSelect {
			return m, nil
		}
		// Don't let the picker open (or the provider swap while it's
		// open) mid-agentic-loop: the in-flight turn re-reads the
		// provider when it loops back to the model, so switching here
		// would send the rest of the run to a different provider with
		// context shaped for another one.
		if *m.AiThink {
			m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before switching providers.", false)
			return m, nil
		}
		return m, m.openProviderSelect()
	}

	// ctrl+i interrupts the current turn - the streamed generation and/or
	// the tool batch. A terminal delivers ctrl+i as the same byte as tab
	// (0x09), so while a turn is running both spellings interrupt. When
	// idle, plain tab stays free for tab switching and ctrl+i is a no-op.
	// While the slash-command palette is open, tab belongs to it as the
	// completion key even during a running turn; only ctrl+i interrupts
	// then.
	if msg_str == "ctrl+i" || msg_str == "tab" {
		turnRunning := *m.AiThink && m.cancel != nil
		paletteTab := msg_str == "tab" && m.slashPaletteVisible()
		if isTurnInterrupt(msg_str, turnRunning) && !paletteTab {
			m.stopRequested = true
			m.cancel()
			if m.Mode == modeQuestionnaire {
				// Abort the open questionnaire so the batch can wind down.
				m.answerQuestionnaire(questionnairetool.FormatCancelled())
				for _, r := range m.pendingResults {
					if r == nil {
						return m, nil
					}
				}
				return m, m.loopAfterBatch()
			}
			m.MessageArea.AppendMessage(">**INFO** Stopping…", false)
			return m, nil
		}
		if msg_str == "ctrl+i" {
			return m, nil
		}
	}

	// A visible modal swallows all keys so the user has to dismiss it
	// (Enter confirms its focused button, Escape cancels).
	if m.modal.IsVisible() {
		var cmd tea.Cmd
		m.modal, cmd = m.modal.Update(msg)
		return m, cmd
	}

	if m.Mode == modeProviderSelect {
		var cmd tea.Cmd
		m.providerSelect, cmd = m.providerSelect.Update(msg)
		return m, cmd
	}

	// Message copy mode owns the keyboard while it is active: arrows move
	// the selection, shift+arrows extend it, enter/ctrl+y copies the
	// selected range, esc cancels. ctrl+y enters the mode from the code
	// tab. This runs before prompt handling so those keys don't type into
	// (or move the cursor of) the textarea.
	if m.Tabs.Active().Name == "code" && (m.Mode == modePrompt || m.Mode == modeIdle) {
		if m.MessageArea.InCopyMode() {
			switch msg_str {
			case "esc":
				m.MessageArea.EndCopy()
				return m, nil
			case "enter", "ctrl+y":
				text, ok := m.MessageArea.SelectedText()
				m.MessageArea.EndCopy()
				return m, copyTextCmd(text, ok)
			case "up", "k":
				m.MessageArea.MoveSelection(-1, false)
				return m, nil
			case "down", "j":
				m.MessageArea.MoveSelection(1, false)
				return m, nil
			case "shift+up", "K":
				m.MessageArea.MoveSelection(-1, true)
				return m, nil
			case "shift+down", "J":
				m.MessageArea.MoveSelection(1, true)
				return m, nil
			case "a":
				m.MessageArea.SelectAll()
				return m, nil
			default:
				// Swallow the key so typing can't change the prompt while
				// a selection is being made.
				return m, nil
			}
		}
		if msg_str == "ctrl+y" {
			if m.MessageArea.BeginCopy() {
				return m, showPopupCmd(copyModeHint)
			}
			return m, showPopupCmd("Nothing to copy yet")
		}
	}

	// Slash-command palette keys. It sits above the prompt and owns its
	// navigation while visible, before the prompt's own up/enter/esc
	// handling and before global tab switching.
	if m.slashPaletteVisible() {
		switch msg_str {
		case "up":
			m.Prompt.MovePalette(-1)
			return m, nil
		case "down":
			m.Prompt.MovePalette(1)
			return m, nil
		case "tab":
			if completed, ok := m.Prompt.AcceptPalette(); ok {
				m.Prompt.SetValue(completed)
				m.syncPromptHeight()
			}
			return m, nil
		case "enter":
			if cmd, ok := m.Prompt.PaletteSelected(); ok {
				m.Prompt.Reset()
				m.syncPromptHeight()
				return m, m.runSlashCommand("/" + cmd.Name)
			}
			return m, nil
		case "esc":
			m.Prompt.ClosePalette()
			// Closing the palette shrinks the bottom bar; re-layout now so
			// the message band immediately reclaims the freed rows.
			m.syncPromptHeight()
			return m, nil
		}
	}

	if msg_str == "ctrl+l" {
		if m.Mode == modeChangeHandling {
			m.ChangesList.Close()
			return m, m.restoreModeForActiveTab()
		}
		if !canOpenChangesList(m.Mode, m.Tabs.Active().Name) {
			return m, nil
		}
		m.Prompt.Blur()
		m.Mode = modeChangeHandling
		return m, tea.Batch(m.ChangesList.Toggle(), m.ChangesList.WatchCmd())
	}

	// A pending questionnaire owns every key until the user answers
	if m.Mode == modeQuestionnaire {
		var cmd tea.Cmd
		m.questionnaire, cmd = m.questionnaire.Update(msg)
		return m, cmd
	}

	// An open skill form owns its keys before tab navigation kicks in,
	// so tab/shift+tab move between its fields instead of the top tabs.
	if m.Mode == modeSkills && m.skillForm.Visible() {
		var cmd tea.Cmd
		m.skillForm, cmd = m.skillForm.Update(msg)
		return m, cmd
	}

	// The Tools tab's add form does the same: while it is open every key
	// belongs to its fields.
	if m.Mode == modeTools && m.Tools.FormVisible() {
		var cmd tea.Cmd
		m.Tools, cmd = m.Tools.Update(msg)
		return m, cmd
	}

	// A skill-creation modal opened from the Tools tab owns the keyboard too.
	if m.Mode == modeTools && m.skillForm.Visible() {
		var cmd tea.Cmd
		m.skillForm, cmd = m.skillForm.Update(msg)
		return m, cmd
	}

	// ctrl+t jumps to the Tools tab from any layout, and toggles back to the
	// code tab when it is already active.
	if msg_str == "ctrl+t" {
		m.toggleToolsTab()
		return m, nil
	}

	// Plan (read-only) mode is session-wide, so its toggle works from every
	// layout that doesn't already own the keyboard: prompt, idle, changes,
	// recall, stats and skills. Modal forms, the provider picker, the
	// permission box and the questionnaire handled above keep their keys.
	if msg_str == "ctrl+shift+p" {
		m.togglePlanMode()
		return m, nil
	}
	// Esc from the idle layout (the statusline's mode indicator is visible
	// there) leaves plan mode. While the prompt is focused, the first esc
	// keeps its usual job of blurring it, and a second esc then exits plan
	// mode.
	if msg_str == "esc" && m.planMode && m.Mode == modeIdle {
		m.setPlanMode(false)
		return m, nil
	}

	// Tab navigation works in every mode: ctrl+tab / tab = next,
	// ctrl+shift+tab / shift+tab = previous.
	switch msg_str {
	case "ctrl+tab", "tab":
		m.Tabs.Next()
		m.syncModeToTab()
		return m, nil
	case "ctrl+shift+tab", "shift+tab":
		m.Tabs.Prev()
		m.syncModeToTab()
		return m, nil
	}

	// In stats mode every remaining key is the stats screen's to handle:
	// up/down navigate the list, left/right pan, enter magnifies, / or
	// any printable char filters, and esc dismisses whatever's open
	// (filter/detail) or leaves the tab altogether.
	if m.Mode == modeStats {
		switch msg_str {
		case "esc":
			if m.Stats.CanExitStatsMode() {
				m.Tabs.ActiveIdx = 0
				m.syncModeToTab()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.Stats, cmd = m.Stats.Update(msg)
		return m, cmd
	}

	// Skills mode mirrors stats: the prompt bar is hidden, and the
	// open skill-form modal or an active search swallows keys first.
	if m.Mode == modeSkills {
		if m.skillForm.Visible() {
			var cmd tea.Cmd
			m.skillForm, cmd = m.skillForm.Update(msg)
			return m, cmd
		}
		// ctrl+n is the explicit new-skill keybind: it opens the same form
		// as Enter, but works whether or not the prompt bar is focused.
		if msg_str == "ctrl+n" {
			m.skillForm.Open()
			return m, nil
		}
		if m.Prompt.Focused() {
			switch msg_str {
			case "esc":
				m.Prompt.Blur()
				return m, nil
			case "shift+enter", "ctrl+j":
				m.Prompt.InsertNewline()
				m.syncPromptHeight()
				return m, nil
			case "enter":
				// A slash command is a command, not skill body text: route
				// it through the runner before the form can claim Enter.
				if val := m.Prompt.Value(); strings.HasPrefix(strings.TrimSpace(val), "/") {
					m.Prompt.Reset()
					m.syncPromptHeight()
					m.historyIdx = 0
					return m, m.runSlashCommand(val)
				}
				m.skillForm.Open()
				return m, nil
			}
			var cmd tea.Cmd
			m.Prompt, cmd = m.Prompt.Update(msg)
			return m, cmd
		}
		switch msg_str {
		case "enter":
			// A committed search keeps its Enter; otherwise Enter
			// focuses the prompt bar (mirroring the code tab).
			if m.Skills.Filtering() {
				var cmd tea.Cmd
				m.Skills, cmd = m.Skills.Update(msg)
				return m, cmd
			}
			return m, m.Prompt.Focus()
		case "i":
			m.Prompt.Focus()
			return m, nil
		case "esc":
			if m.Skills.CanExitSkillsMode() {
				m.Tabs.ActiveIdx = 0
				m.syncModeToTab()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.Skills, cmd = m.Skills.Update(msg)
		return m, cmd
	}

	// Tools mode mirrors skills: the pane owns navigation and actions, while
	// its prompt bar is only used to write a new skill body. Enter on a
	// non-empty prompt opens the skill-name form; Esc leaves the tab.
	if m.Mode == modeTools {
		if m.Prompt.Focused() {
			switch msg_str {
			case "esc":
				m.Prompt.Blur()
				return m, nil
			case "shift+enter", "ctrl+j":
				m.Prompt.InsertNewline()
				m.syncPromptHeight()
				return m, nil
			case "enter":
				if strings.TrimSpace(m.Prompt.Value()) == "" {
					m.MessageArea.AppendMessage(">**INFO** Type the skill body first, then press enter to name it.", false)
					return m, nil
				}
				m.skillForm.Open()
				return m, nil
			}
			var cmd tea.Cmd
			m.Prompt, cmd = m.Prompt.Update(msg)
			return m, cmd
		}
		switch msg_str {
		case "i":
			return m, m.Prompt.Focus()
		case "esc":
			if m.Tools.CanExitToolsMode() {
				m.Tabs.ActiveIdx = 0
				m.syncModeToTab()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.Tools, cmd = m.Tools.Update(msg)
		return m, cmd
	}

	if m.Mode == modeChangeHandling {
		switch msg_str {
		case "esc", "tab", "enter":
			m.ChangesList.Close()
			m.Mode = modePrompt
			return m, m.Prompt.Focus()
		case "up":
			m.ChangesList.CursorUp()
			return m, nil
		case "down":
			m.ChangesList.CursorDown()
			return m, nil
		case "?":
			m.ChangesList.ToggleHelp()
			return m, nil
		case "ctrl+a":
			return m, m.ChangesList.AcceptSelected()
		case "ctrl+r":
			return m, m.ChangesList.RejectSelected()
		case "ctrl+f":
			return m, m.ChangesList.AcceptAll()
		case "ctrl+d":
			return m, m.ChangesList.RejectAll()
		default:
			var cmd tea.Cmd
			m.ChangesList, cmd = m.ChangesList.Update(msg)
			return m, cmd
		}
	}

	if m.Mode == modeRecall {
		switch msg_str {
		case "esc":
			m.Prompt.Blur()
			m.Mode = modeIdle
			m.historyIdx = 0
			return m, nil
		case "up":
			if val := m.MessageArea.LastUserMessage(m.historyIdx + 1); val != "" {
				m.historyIdx++
				m.Prompt.SetValue(val)
				m.syncPromptHeight()
			}
			return m, nil
		case "down":
			if m.historyIdx > 1 {
				m.historyIdx--
				m.Prompt.SetValue(m.MessageArea.LastUserMessage(m.historyIdx))
				m.syncPromptHeight()
			} else {
				m.historyIdx = 0
				m.Prompt.Reset()
				m.syncPromptHeight()
				m.Mode = modePrompt
			}
			return m, nil
		case "left", "right":
			var cmd tea.Cmd
			m.Prompt, cmd = m.Prompt.Update(msg)
			return m, cmd
		case "enter":
			m.Mode = modePrompt
			return m, tea.Batch(m.submitPrompt()...)
		default:
			m.Mode = modePrompt
			m.historyIdx = 0
			var cmd tea.Cmd
			m.Prompt, cmd = m.Prompt.Update(msg)
			return m, cmd
		}
	}

	if m.Mode == modePrompt {
		switch msg_str {
		case "esc":
			m.Prompt.Blur()
			m.Mode = modeIdle
			m.historyIdx = 0
			return m, nil
		case "shift+enter", "ctrl+j":
			m.Prompt.InsertNewline()
			m.syncPromptHeight()
			return m, nil
		case "up":
			// Only recall history when the prompt is empty; otherwise
			// let the textarea handle cursor movement as usual.
			if m.Prompt.IsEmpty() {
				if val := m.MessageArea.LastUserMessage(1); val != "" {
					m.historyIdx = 1
					m.Prompt.SetValue(val)
					m.syncPromptHeight()
					m.Mode = modeRecall
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.Prompt, cmd = m.Prompt.Update(msg)
			return m, cmd
		case "ctrl+a":
			val := m.Prompt.Value()
			if val != "" {
				return m, func() tea.Msg {
					if err := clipboard.WriteAll(val); err != nil {
						return popup.ShowMsg{Message: "Copy failed: " + err.Error()}
					}
					return popup.ShowMsg{Message: popupCopyMessage(val)}
				}
			}
			return m, nil
		case "ctrl+u":
			m.Prompt.Reset()
			m.syncPromptHeight()
			m.historyIdx = 0
			return m, nil
		case "enter":
			return m, tea.Batch(m.submitPrompt()...)
		}
		var cmd tea.Cmd
		m.Prompt, cmd = m.Prompt.Update(msg)
		return m, cmd
	} else {
		switch msg_str {
		case "enter":
			m.Mode = modePrompt
			return m, m.Prompt.Focus()
		}
	}
	return m.forwardToComponents(msg)
}

// slashPaletteVisible reports whether the command palette can own keys right
// now: it only renders over the code tab's prompt bar.
func (m model) slashPaletteVisible() bool {
	return m.Tabs.Active().Name == "code" &&
		(m.Mode == modePrompt || m.Mode == modeIdle) &&
		m.Prompt.PaletteVisible()
}

func popupCopyMessage(val string) string {
	oneLine := strings.ReplaceAll(strings.TrimSpace(val), "\n", " ")
	const maxLen = 40
	if len(oneLine) > maxLen {
		oneLine = oneLine[:maxLen] + "…"
	}
	if oneLine == "" {
		return "Copied to clipboard"
	}
	return "Copied: " + oneLine
}

// copyModeHint is shown when copy mode opens so the selection keys and the
// prompt bar's existing copy shortcut are discoverable.
const copyModeHint = "Copy: ↑↓ select · shift+↑↓ range · a all · enter copy · esc cancel"

func showPopupCmd(message string) tea.Cmd {
	return func() tea.Msg { return popup.ShowMsg{Message: message} }
}

// copyTextCmd writes text to the system clipboard and reports the result as
// the existing popup toast.
func copyTextCmd(text string, ok bool) tea.Cmd {
	if !ok || strings.TrimSpace(text) == "" {
		return showPopupCmd("Nothing selected to copy")
	}
	return func() tea.Msg {
		if err := clipboard.WriteAll(text); err != nil {
			return popup.ShowMsg{Message: "Copy failed: " + err.Error()}
		}
		return popup.ShowMsg{Message: popupCopyMessage(text)}
	}
}
