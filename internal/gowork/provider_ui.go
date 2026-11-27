package gowork

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	providerselect "GoWork/Tui/Components/ProviderSelect"

	"GoWork/providers"
	"GoWork/tools"
)

// authFailureDetail prefers the provider's response body, which carries
// entitlement messages such as "OpenCode Go subscription required", and falls
// back to its status message when the body is empty.
func authFailureDetail(perr *providers.ProviderError) string {
	if perr == nil {
		return "check your API key."
	}
	if perr.Err != nil {
		if detail := strings.TrimSpace(perr.Err.Error()); detail != "" {
			return detail
		}
	}
	if detail := strings.TrimSpace(perr.Message); detail != "" {
		return detail
	}
	return "check your API key."
}

func (m *model) openProviderSelect() tea.Cmd {
	m.Mode = modeProviderSelect
	m.Prompt.Blur()
	m.providerSelect = providerselect.New(
		get_supported_providers(),
		get_supported_providers_local(),
		os.Getenv("API_KEY"),
	)
	m.providerSelect.SetSize(m.winWidth, m.winHeight)
	return m.providerSelect.Init()
}

// handleProviderSelected applies a completed provider/model pick: it
// swaps the live provider, records the choice in .env, and refreshes the
// model metadata (context window) for the new selection.
func (m model) handleProviderSelected(msg providerselect.SelectedMsg) (tea.Model, tea.Cmd) {
	m.restoreModeForActiveTab()
	// The picker may have been open while a turn was running (it only
	// blocks ctrl+p, not the picker's Enter). Double-check aiThink so
	// we never swap providers under an in-flight agentic loop.
	if *m.AiThink {
		m.MessageArea.AppendMessage(">**INFO** Provider not switched: a turn is still running.", false)
		return m, nil
	}
	apiKey := msg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}
	p, err := providers.Select_provider(msg.Provider, msg.ModelID, apiKey)
	if err != nil {
		m.MessageArea.AppendMessage("Failed to switch provider: "+err.Error(), false)
		return m, nil
	}
	if m.model == nil {
		m.model = &tools.ProviderHolder{}
	}
	m.model.Set(p)
	m.status.providerName = msg.Provider
	m.status.modelID = msg.ModelID
	m.status.contextWindow = 0
	m.status.lastPromptTokens = 0

	keyToSave := ""
	if msg.WroteNewKey {
		keyToSave = msg.APIKey
		_ = os.Setenv("API_KEY", msg.APIKey)
	}
	if err := saveProviderPrefs(msg.Provider, msg.ModelID, keyToSave); err != nil {
		m.MessageArea.AppendMessage("Switched provider but failed to save .env: "+err.Error(), false)
	} else {
		_ = os.Setenv("PROVIDER", msg.Provider)
		_ = os.Setenv("model", msg.ModelID)
	}
	m.MessageArea.AppendMessage(
		fmt.Sprintf("Switched to %s / %s", msg.Provider, msg.ModelID),
		false,
	)
	warnCmd := toolSupportWarningCmd(msg.ModelID, msg.Provider)
	return m, tea.Batch(fetchModelInfoCmd(p, m.status.modelID), warnCmd)
}
