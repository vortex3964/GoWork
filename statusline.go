package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

const statusLineHeight = 1

type statusLine struct {
	modelID      string
	providerName string
	projectRoot  string // just the root folder's name (e.g. "GoWork"), not the full path

	contextWindow    int // model's max context size, 0 until modelInfoMsg comes back successfully
	lastPromptTokens int // prompt tokens from the most recent request - stands in for "how full is the context window right now"
	sessionTokens    int // running total of prompt+completion tokens for the whole session
}

func newStatusLine(root string, providerName string, modelID string) statusLine {
	name := "?"
	if root != "" {
		name = filepath.Base(root)
	}

	return statusLine{
		modelID:      modelID,
		providerName: providerName,
		projectRoot:  name,
	}
}

const (
	statusModelBG    = "#7aa2f7" // model
	statusProviderBG = "#414868" // provider
	statusRootBG     = "#3b4261" // project root
	statusCtxBG      = "#e0af68" // context window % used
	statusTokBG      = "#9ece6a" // session token total

	statusFG = "#1a1b26"
)

const sepChar = "\uE0B0"

type segment struct {
	bg   string
	text string
}

func renderChain(segs []segment) string {
	var b strings.Builder
	for i, s := range segs {
		b.WriteString(lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(statusFG)).
			Background(lipgloss.Color(s.bg)).
			Padding(0, 1).
			Render(s.text))
		if i < len(segs)-1 {
			next := segs[i+1]
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(s.bg)).
				Background(lipgloss.Color(next.bg)).
				Render(sepChar))
		}
	}
	return b.String()
}
func renderStatusLine(m model) string {
	left := renderChain([]segment{
		{bg: statusModelBG, text: m.status.modelID},
		{bg: statusProviderBG, text: m.status.providerName},
	})

	center := renderChain([]segment{
		{bg: statusRootBG, text: m.status.projectRoot},
	})

	ctxLabel := "n/a ctx"
	if m.status.contextWindow > 0 {
		pct := int(float64(m.status.lastPromptTokens) / float64(m.status.contextWindow) * 100)
		switch {
		case pct < 0:
			pct = 0
		case pct > 100:
			pct = 100
		}
		ctxLabel = fmt.Sprintf("%d%% ctx", pct)
	}
	right := renderChain([]segment{
		{bg: statusCtxBG, text: ctxLabel},
		{bg: statusTokBG, text: fmt.Sprintf("%d tok", m.status.sessionTokens)},
	})

	total := lipgloss.Width(left) + lipgloss.Width(center) + lipgloss.Width(right)
	gap := m.winWidth - total
	if gap < 2 {
		return left + center + right
	}
	leftGap := gap / 2
	rightGap := gap - leftGap
	return left + strings.Repeat(" ", leftGap) + center + strings.Repeat(" ", rightGap) + right
}
