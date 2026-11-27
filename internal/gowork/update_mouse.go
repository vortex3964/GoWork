package gowork

import tea "charm.land/bubbletea/v2"

func (m model) handleMouseClickMsg(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.Mode == modeProviderSelect || m.Mode == modePermission {
		return m, nil
	}
	if m.Mode == modeQuestionnaire {
		return m, nil
	}
	if msg.Button == tea.MouseLeft {
		// On the stats tab the whole area below the tabs belongs to the
		// dashboard: clicks select list rows, wheel scrolls.
		if m.Tabs.Active().Name == "stats" && msg.Y >= m.panelTop() {
			m.Stats.HandleMouseClick(msg.X, msg.Y-m.panelTop())
			return m, nil
		}
		if m.Tabs.Active().Name == "skills" && msg.Y >= m.panelTop() && msg.Y < m.promptTop() {
			m.Skills.HandleMouseClick(msg.X, msg.Y-m.panelTop())
			return m, nil
		}
		// The todo panel toggle arrow, drawn at the middle of the
		// message band. Clicking it flips the sidebar open/closed.
		if m.Tabs.Active().Name == "code" && msg.Y == m.arrowRow() && msg.X == m.arrowCol() {
			m.toggleTodoPanel()
			return m, nil
		}
		// Starting a drag anywhere on the panel's left edge (the same
		// column as the toggle arrow, but any band row) arms the resize
		// handle MouseMotionMsg then follows with the live cursor.
		if m.todoPanelOpen && m.Tabs.Active().Name == "code" &&
			msg.Y > m.panelTop() && msg.Y < m.promptTop() &&
			abs(msg.X-m.arrowCol()) <= 1 {
			m.panelDragActive = true
			m.panelDragStartX = msg.X
			m.panelDragWidth = m.panelWidth()
			return m, nil
		}
	}

	// Copy interactions in the message band on the code tab: left click
	// selects a message (shift-click extends), right click copies it.
	// Both buttons must run this block, so it sits outside the left-click
	// guard above; the arrow/resize handlers already consumed their clicks.
	// Wheel events go through handleMouseWheelMsg, so viewport scrolling is
	// unaffected.
	if m.Tabs.Active().Name == "code" && m.Mode != modeChangeHandling &&
		msg.Y > m.panelTop() && msg.Y < m.promptTop() && msg.X < m.msgAreaWidth() {
		switch msg.Button {
		case tea.MouseRight:
			m.MessageArea.CancelTextDrag()
			m.Prompt.CancelTextDrag()
			if m.MessageArea.HandleClick(msg.X, msg.Y-m.panelTop(), false) {
				text, ok := m.MessageArea.SelectedText()
				m.MessageArea.EndCopy()
				return m, copyTextCmd(text, ok)
			}
			return m, nil
		case tea.MouseLeft:
			m.Prompt.CancelTextDrag()
			extend := msg.Mod.Contains(tea.ModShift)
			if m.MessageArea.HandleClick(msg.X, msg.Y-m.panelTop(), extend) {
				// Arm a character-granular drag on the same press. If the
				// pointer never moves this stays invisible and the normal
				// whole-message click selection above is all that happens.
				m.MessageArea.BeginTextDrag(msg.X, msg.Y-m.panelTop())
				return m, showPopupCmd(copyModeHint)
			}
		}
	}

	if msg.Button == tea.MouseLeft {
		// The tab strip renders taller than one line (its border adds top
		// and bottom rows), so let clicks anywhere on it switch tabs. The
		// prompt bar sits pinned just above the statusline at the bottom
		// of the screen, so it owns exactly m.Prompt.Height() rows starting
		// at promptTop() - anything below that is the statusline, which
		// isn't clickable. On the code and skills tabs, whose layouts
		// pin the prompt bar, clicking it focuses it.
		switch {
		case msg.Y < m.Tabs.Height():
			prev := m.Tabs.ActiveIdx
			m.Tabs.HandleClick(msg.X)
			if m.Tabs.ActiveIdx != prev {
				// Leaving the code tab shouldn't leave a silent selection
				// waiting to reappear when the user comes back.
				m.MessageArea.EndCopy()
				m.MessageArea.CancelTextDrag()
				m.Prompt.CancelTextDrag()
			}
			m.syncModeToTab()
		case (m.Tabs.Active().Name == "code" || m.Tabs.Active().Name == "skills" || m.Tabs.Active().Name == "tools") &&
			msg.Y >= m.promptTop() && msg.Y < m.promptTop()+m.Prompt.Height():
			m.historyIdx = 0
			if m.Tabs.Active().Name == "code" {
				m.Mode = modePrompt
			}
			m.MessageArea.CancelTextDrag()
			m.Prompt.BeginTextDrag(msg.X, msg.Y-m.promptTop())
			return m, m.Prompt.Focus()
		}
	}
	return m, nil
}

func (m model) handleMouseWheelMsg(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if m.Mode == modeProviderSelect || m.Mode == modePermission {
		return m, nil
	}
	if m.Mode == modeQuestionnaire {
		return m, nil
	}
	// Scrolling either box doesn't require focus — same as scrolling
	// a window you're not "in" in nvim.
	if m.Tabs.Active().Name == "stats" {
		m.Stats.HandleMouseWheel(msg.X, msg.Y-m.panelTop(), msg.Button == tea.MouseWheelUp)
		return m, nil
	}
	if m.Tabs.Active().Name == "skills" {
		promptTop := m.promptTop()
		switch {
		case msg.Y >= promptTop && msg.Y < promptTop+m.Prompt.Height():
			switch msg.Button {
			case tea.MouseWheelUp:
				m.Prompt.ScrollUp()
			case tea.MouseWheelDown:
				m.Prompt.ScrollDown()
			}
		case msg.Y >= topBarHeight && msg.Y < promptTop:
			m.Skills.HandleMouseWheel(msg.Button == tea.MouseWheelUp)
		}
		return m, nil
	}
	if m.Tabs.Active().Name == "tools" {
		promptTop := m.promptTop()
		switch {
		case msg.Y >= promptTop && msg.Y < promptTop+m.Prompt.Height():
			switch msg.Button {
			case tea.MouseWheelUp:
				m.Prompt.ScrollUp()
			case tea.MouseWheelDown:
				m.Prompt.ScrollDown()
			}
		case msg.Y >= topBarHeight && msg.Y < promptTop:
			m.Tools.HandleMouseWheel(msg.Button == tea.MouseWheelUp)
		}
		return m, nil
	}
	if m.Tabs.Active().Name == "code" {
		promptTop := m.promptTop()
		switch {
		case msg.Y >= promptTop && msg.Y < promptTop+m.Prompt.Height():
			switch msg.Button {
			case tea.MouseWheelUp:
				m.Prompt.ScrollUp()
			case tea.MouseWheelDown:
				m.Prompt.ScrollDown()
			}
		case m.Mode == modeChangeHandling && msg.Y >= topBarHeight && msg.Y < promptTop:
			// The wheel is reserved for the file-explorer pane
			// while the changes list is open the results list
			// is navigated with the arrow keys instead.
			switch msg.Button {
			case tea.MouseWheelUp:
				m.ChangesList.ExplorerScrollUp()
			case tea.MouseWheelDown:
				m.ChangesList.ExplorerScrollDown()
			}
		case msg.Y >= topBarHeight && msg.Y < promptTop:
			switch msg.Button {
			case tea.MouseWheelUp:
				m.MessageArea.ScrollUp()
			case tea.MouseWheelDown:
				m.MessageArea.ScrollDown()
			}
		}
	}
	return m, nil
}

func (m model) handleMouseMotionMsg(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	if m.Mode == modePermission {
		return m, nil
	}
	if m.panelDragActive && m.todoPanelOpen && m.Tabs.Active().Name == "code" {
		delta := msg.X - m.panelDragStartX
		w := m.panelDragWidth + delta
		if w < 20 {
			w = 20
		}
		if w > m.winWidth/2 {
			w = m.winWidth / 2
		}
		m.todoPanelWidth = w
		m.applyPanelSize()
		return m, nil
	}
	if m.Prompt.TextDragActive() {
		m.Prompt.UpdateTextDrag(msg.X, msg.Y-m.promptTop())
		return m, nil
	}
	if m.Tabs.Active().Name == "code" && m.MessageArea.TextDragActive() {
		m.MessageArea.UpdateTextDrag(msg.X, msg.Y-m.panelTop())
		return m, nil
	}
	return m, nil
}

func (m model) handleMouseReleaseMsg(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	if m.Mode == modePermission {
		return m, nil
	}
	m.panelDragActive = false
	// A completed drag-copy is an intent to keep interacting with the
	// prompt: restore its focus and prompt mode so the next keystroke or
	// paste lands in the textarea without an extra Enter.
	if m.Prompt.TextDragActive() {
		text, ok := m.Prompt.EndTextDrag()
		m.MessageArea.CancelTextDrag()
		m.focusCodeTabPrompt()
		if ok {
			return m, copyTextCmd(text, true)
		}
		return m, nil
	}
	if m.MessageArea.TextDragActive() {
		moved := m.MessageArea.TextDragMoved()
		text, ok := m.MessageArea.EndTextDrag()
		if moved {
			// Even an empty selection (whitespace-only drag) was still a
			// copy gesture: leave the prompt ready for the next keystroke.
			m.focusCodeTabPrompt()
		}
		if ok {
			return m, copyTextCmd(text, true)
		}
		return m, nil
	}
	return m, nil
}
