package gowork

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	providerselect "GoWork/Tui/Components/ProviderSelect"

	"GoWork/providers"
	"GoWork/tools"
)

// localServerProbeMsg refreshes a picker status from a background health
// probe. It is separate from ServerStatusMsg so opening the picker does not
// spam the message area.
type localServerProbeMsg struct {
	Provider string
	Status   string
}

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
	// Seed the picker with what we already know (binary present, managed
	// process running), then refresh external-running status in the
	// background. Opening the picker never probes synchronously and never
	// launches anything.
	cmds := []tea.Cmd{m.providerSelect.Init()}
	if m.localServers != nil {
		mgr := m.localServers
		for _, name := range get_supported_providers_local() {
			if label, ok := mgr.StatusLabel(name); ok {
				m.providerSelect.SetLocalServerStatus(name, label)
			} else {
				continue
			}
			name := name
			cmds = append(cmds, func() tea.Msg {
				return localServerProbeMsg{
					Provider: name,
					Status:   mgr.Probe(context.Background(), name).Label(),
				}
			})
		}
	}
	m.providerSelect.SetSize(m.winWidth, m.winHeight)
	return tea.Batch(cmds...)
}

// handleLocalServerToggle starts or stops the highlighted local server. The
// work runs in a tea.Cmd so the picker keeps painting while the server loads;
// the manager bounds startup and cleans the child up on timeout/cancel.
func (m model) handleLocalServerToggle(msg providerselect.ServerToggleMsg) (tea.Model, tea.Cmd) {
	if m.localServers == nil {
		m.providerSelect.SetLocalServerStatus(msg.Provider, "launch controls unavailable")
		return m, nil
	}

	status := m.localServers.Status(msg.Provider)
	if status.Busy != "" {
		m.providerSelect.SetLocalServerStatus(msg.Provider, status.Label())
		return m, nil
	}

	provider := msg.Provider
	mgr := m.localServers
	if status.Running && status.Managed {
		m.providerSelect.SetLocalServerStatus(provider, "stopping…")
		return m, func() tea.Msg {
			st, err := mgr.Stop(context.Background(), provider)
			return providerselect.ServerStatusMsg{
				Provider: provider,
				Status:   st.Label(),
				Err:      err,
			}
		}
	}

	// Start also covers an externally running server: it probes first and
	// reports "already running" instead of spawning a second process.
	m.providerSelect.SetLocalServerStatus(provider, "starting…")
	return m, func() tea.Msg {
		st, err := mgr.Start(context.Background(), provider)
		return providerselect.ServerStatusMsg{
			Provider: provider,
			Status:   st.Label(),
			Err:      err,
		}
	}
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
