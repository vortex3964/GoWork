package gowork

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	dialog "GoWork/Tui/Components/Dialog"
	"GoWork/providers"
)

func fetchModelInfoCmd(p providers.Provider, model string) tea.Cmd {
	return func() tea.Msg {
		info, err := p.Info(context.Background(), model)
		return modelInfoMsg{info: info, err: err}
	}
}

// toolSupportWarningCmd opens the centered modal when the selected model can't
// call tools. No-op if the provider/model is "None" (i.e. nothing was picked).
func toolSupportWarningCmd(modelName, providerName string) tea.Cmd {
	if providerName == "" || modelName == "" ||
		strings.EqualFold(providerName, "None") || strings.EqualFold(modelName, "None") {
		return nil
	}
	if providers.ModelSupportsTools(providerName, modelName) {
		return nil
	}
	return func() tea.Msg {
		return dialog.ShowMsg{
			Title:   "Tool calling unsupported",
			Message: fmt.Sprintf("%s doesn't support tool calling.", modelName),
			Buttons: []string{"OK"},
		}
	}
}
