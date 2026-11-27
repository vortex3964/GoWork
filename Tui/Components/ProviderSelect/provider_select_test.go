package providerselect

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func localStepModel() Model {
	m := New(
		[]string{"google", "local"},
		[]string{"ollama", "llamaCpp", "lmStudio"},
		"",
	)
	m.SetSize(100, 40)
	m.step = stepLocal
	m.list = m.newList("Select local provider", stringItems(m.localProviders))
	// The host app marks launchable providers with a status label.
	m.SetLocalServerStatus("llamaCpp", "not running · press s to start")
	m.SetLocalServerStatus("lmStudio", "not running · press s to start")
	return m
}

func toggleMsgFromCmd(cmd tea.Cmd) (ServerToggleMsg, bool) {
	if cmd == nil {
		return ServerToggleMsg{}, false
	}
	msg := cmd()
	toggle, ok := msg.(ServerToggleMsg)
	return toggle, ok
}

func TestLocalServerToggleKey(t *testing.T) {
	m := localStepModel()
	m.list.Select(1) // llamaCpp

	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	toggle, ok := toggleMsgFromCmd(cmd)
	if !ok {
		t.Fatalf("s did not emit ServerToggleMsg (cmd=%v)", cmd)
	}
	if toggle.Provider != "llamaCpp" {
		t.Fatalf("toggle provider = %q, want llamaCpp", toggle.Provider)
	}
}

func TestLocalServerToggleOnlyForLaunchableProviders(t *testing.T) {
	m := localStepModel()
	m.list.Select(0) // ollama has no launch control

	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if toggle, ok := toggleMsgFromCmd(cmd); ok {
		t.Fatalf("s emitted ServerToggleMsg for ollama (%q)", toggle.Provider)
	}
}

func TestLocalServerToggleIgnoredWhileFiltering(t *testing.T) {
	m := localStepModel()
	m.list.Select(1)

	m, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if toggle, ok := toggleMsgFromCmd(cmd); ok {
		t.Fatalf("typing in the filter emitted ServerToggleMsg(%q)", toggle.Provider)
	}
}

func TestLocalServerStatusRendering(t *testing.T) {
	m := localStepModel()
	m.list.Select(1)
	m.SetLocalServerStatus("llamaCpp", "running · managed by GoWork")

	view := m.View()
	if !strings.Contains(view, "server: running") {
		t.Fatalf("picker view does not show server status:\n%s", view)
	}
	if !strings.Contains(view, "s start/stop server") {
		t.Fatalf("picker help does not mention the launch key:\n%s", view)
	}
}

func TestLocalServerStatusOnlyOnLocalStep(t *testing.T) {
	m := localStepModel()
	m.step = stepProvider
	m.list = m.newList("Select provider", stringItems(m.providers))
	m.SetLocalServerStatus("llamaCpp", "running · managed by GoWork")

	if strings.Contains(m.View(), "server: running") {
		t.Fatal("server status leaked outside the local-provider step")
	}
}
