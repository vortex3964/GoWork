package gowork

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	changeslist "GoWork/Tui/Components/ChangesList"
	tabs "GoWork/Tui/Components/Tabs"
)

// newPromptFocusModel builds a focused, code-tab model with all four tabs and
// a message to copy, the setup for the copy/interrupt focus regressions.
func newPromptFocusModel(t *testing.T) model {
	t.Helper()
	m := newMouseCopyModel(t)
	m.Tabs = tabs.New("code", "skills", "tools", "stats")
	m.ChangesList = changeslist.New(nil)
	m.Prompt.Focus()
	return m
}

func typeKey(t *testing.T, m model, r rune) model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	return updated.(model)
}

func pasteText(t *testing.T, m model, text string) model {
	t.Helper()
	updated, _ := m.Update(tea.PasteMsg{Content: text})
	return updated.(model)
}

// dragMessageSelection performs a character drag across the first message
// body and releases it, leaving the copy command unexecuted.
func dragMessageSelection(t *testing.T, m model) model {
	t.Helper()
	x, y := 6, m.panelTop()+2
	m = dragClick(t, m, x, y)
	m = dragMotion(t, m, x+5, y)
	m, _ = dragRelease(t, m, x+5, y)
	return m
}

func TestTypingAfterMessageAreaDragCopyLandsInPrompt(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Mode = modeIdle
	m.Prompt.Blur()

	m = dragMessageSelection(t, m)
	if m.Mode != modePrompt {
		t.Fatalf("mode after message drag-copy = %v, want prompt", m.Mode)
	}
	if !m.Prompt.Focused() {
		t.Fatal("message drag-copy left the prompt blurred")
	}

	m = typeKey(t, m, 'x')
	if got := m.Prompt.Value(); got != "x" {
		t.Fatalf("typing after message drag-copy produced %q, want %q without an extra Enter", got, "x")
	}
}

func TestPasteAfterMessageAreaDragCopyLandsInPrompt(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Mode = modeIdle
	m.Prompt.Blur()

	m = dragMessageSelection(t, m)
	m = pasteText(t, m, "pasted after drag")

	if got := m.Prompt.Value(); got != "pasted after drag" {
		t.Fatalf("paste after message drag-copy produced %q", got)
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("paste did not leave a usable prompt: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}
}

func dragPromptSelection(t *testing.T, m model) model {
	t.Helper()
	x, y := 5, m.promptTop()+1
	m = dragClick(t, m, x, y)
	m = dragMotion(t, m, x+4, y)
	m, _ = dragRelease(t, m, x+4, y)
	return m
}

func TestTypingAfterPromptBarDragCopyLandsInPrompt(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Prompt.SetValue("hello prompt text")
	m.applyPanelSize()
	m.Mode = modeIdle
	m.Prompt.Blur()

	m = dragPromptSelection(t, m)
	if m.Mode != modePrompt {
		t.Fatalf("mode after prompt drag-copy = %v, want prompt", m.Mode)
	}
	if !m.Prompt.Focused() {
		t.Fatal("prompt drag-copy left the prompt blurred")
	}

	m = typeKey(t, m, 'z')
	if got := m.Prompt.Value(); got != "hello prompt textz" {
		t.Fatalf("typing after prompt drag-copy produced %q", got)
	}
}

func TestPasteAfterPromptBarDragCopyLandsInPrompt(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Prompt.SetValue("hello prompt text")
	m.applyPanelSize()
	m.Mode = modeIdle
	m.Prompt.Blur()

	m = dragPromptSelection(t, m)
	m = pasteText(t, m, " plus paste")

	if got := m.Prompt.Value(); got != "hello prompt text plus paste" {
		t.Fatalf("paste after prompt drag-copy produced %q", got)
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("paste did not leave a usable prompt: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}
}

func TestTypingAfterInterruptLandsInPrompt(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Mode = modeIdle
	m.Prompt.Blur()
	m.Prompt.SetValue("kept ")

	cancelled := false
	m.cancel = func() { cancelled = true }
	*m.AiThink = true

	updated, _ := m.Update(ctrlKey('i'))
	m = updated.(model)

	if !cancelled {
		t.Fatal("ctrl+i did not cancel the running turn")
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("interrupt did not restore the prompt: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}
	m = typeKey(t, m, 'x')
	if got := m.Prompt.Value(); got != "kept x" {
		t.Fatalf("typing after interrupt produced %q, want %q", got, "kept x")
	}
}

func TestPasteAfterInterruptLandsInPrompt(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Mode = modeIdle
	m.Prompt.Blur()

	cancelled := false
	m.cancel = func() { cancelled = true }
	*m.AiThink = true

	updated, _ := m.Update(ctrlKey('i'))
	m = updated.(model)
	if !cancelled {
		t.Fatal("ctrl+i did not cancel the running turn")
	}

	m = pasteText(t, m, "pasted after interrupt")
	if got := m.Prompt.Value(); got != "pasted after interrupt" {
		t.Fatalf("paste after interrupt produced %q", got)
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("paste after interrupt did not leave a usable prompt: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}
}

func TestCopyModeKeepsGlobalShortcuts(t *testing.T) {
	beginCopy := func(t *testing.T) model {
		t.Helper()
		m := newPromptFocusModel(t)
		if !m.MessageArea.BeginCopy() {
			t.Fatal("could not enter copy mode")
		}
		return m
	}

	t.Run("ctrl+l opens changes list", func(t *testing.T) {
		m := beginCopy(t)
		updated, _ := m.Update(ctrlKey('l'))
		m = updated.(model)
		if m.Mode != modeChangeHandling {
			t.Fatalf("mode after ctrl+l in copy mode = %v, want changes", m.Mode)
		}
		if m.MessageArea.InCopyMode() {
			t.Fatal("ctrl+l left copy mode active")
		}
	})

	t.Run("ctrl+p opens provider picker", func(t *testing.T) {
		m := beginCopy(t)
		updated, _ := m.Update(ctrlKey('p'))
		m = updated.(model)
		if m.Mode != modeProviderSelect {
			t.Fatalf("mode after ctrl+p in copy mode = %v, want provider select", m.Mode)
		}
		if m.MessageArea.InCopyMode() {
			t.Fatal("ctrl+p left copy mode active")
		}
	})

	t.Run("ctrl+t opens tools tab", func(t *testing.T) {
		m := beginCopy(t)
		updated, _ := m.Update(ctrlKey('t'))
		m = updated.(model)
		if got := m.Tabs.Active().Name; got != "tools" {
			t.Fatalf("active tab after ctrl+t in copy mode = %q, want tools", got)
		}
		if m.MessageArea.InCopyMode() {
			t.Fatal("ctrl+t left copy mode active")
		}
	})

	t.Run("ctrl+i interrupts and restores prompt", func(t *testing.T) {
		m := beginCopy(t)
		m.Mode = modeIdle
		m.Prompt.Blur()
		cancelled := false
		m.cancel = func() { cancelled = true }
		*m.AiThink = true

		updated, _ := m.Update(ctrlKey('i'))
		m = updated.(model)
		if !cancelled || !m.stopRequested {
			t.Fatalf("ctrl+i in copy mode did not interrupt: cancelled=%v stop=%v", cancelled, m.stopRequested)
		}
		if m.MessageArea.InCopyMode() {
			t.Fatal("ctrl+i left copy mode active")
		}
		if m.Mode != modePrompt || !m.Prompt.Focused() {
			t.Fatalf("ctrl+i did not restore the prompt: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
		}
	})

	t.Run("ctrl+c quits", func(t *testing.T) {
		m := beginCopy(t)
		_, cmd := m.Update(ctrlKey('c'))
		if cmd == nil {
			t.Fatal("ctrl+c in copy mode returned no command")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("ctrl+c in copy mode did not quit")
		}
	})

	t.Run("esc cancels the selection", func(t *testing.T) {
		m := beginCopy(t)
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		m = updated.(model)
		if m.MessageArea.InCopyMode() {
			t.Fatal("esc did not cancel copy mode")
		}
	})
}

func TestPasteWhileCopyModeActiveEndsCopyMode(t *testing.T) {
	m := newPromptFocusModel(t)
	m.Prompt.SetValue("base")
	m.Prompt.Focus()
	if !m.MessageArea.BeginCopy() {
		t.Fatal("could not enter copy mode")
	}

	m = pasteText(t, m, " pasted")
	if m.MessageArea.InCopyMode() {
		t.Fatal("paste left copy mode active")
	}
	if got := m.Prompt.Value(); got != "base pasted" {
		t.Fatalf("paste in copy mode produced %q", got)
	}
	if m.Mode != modePrompt || !m.Prompt.Focused() {
		t.Fatalf("paste in copy mode did not leave a usable prompt: mode=%v focused=%v", m.Mode, m.Prompt.Focused())
	}
}
