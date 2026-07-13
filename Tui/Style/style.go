// Package style is the single source of truth for color and shape
// across every component. Components should reference these tokens
// instead of hardcoding hex values or defining their own — that's
// what makes a future theme swap (or a light/dark mode) a one-file
// change instead of a grep-and-replace.
package style

import "charm.land/lipgloss/v2"

// Palette (Ayu Dark)
var (
	Primary    = lipgloss.Color("#E6B450") // Ayu accent gold
	Border     = lipgloss.Color("#E6B450") // Ayu accent gold
	Muted      = lipgloss.Color("#626A73") // Ayu comment gray
	Text       = lipgloss.Color("#B3B1AD") // Ayu fg
	Background = lipgloss.Color("#0F1419") // Ayu bg
	Danger     = lipgloss.Color("#FF3333") // Ayu syntax.error red
	Success    = lipgloss.Color("#7FD962") // Ayu vcs.added green
	Warning    = lipgloss.Color("#FFB454") // Ayu syntax.func orange
	Info       = lipgloss.Color("#59C2FF") // Ayu syntax.entity blue
	Secondary  = lipgloss.Color("#0D1017")
	Highlight  = lipgloss.Color("#253340")
	Special    = lipgloss.Color("#95E6CB")
)

// TabAccent is intentionally NOT part of the Ayu palette above.
// Tabs are the one component kept on the original brand purple
// instead of following the theme swap.
var TabAccent = lipgloss.Color("#7010E3")

// Tabs

// TabBorder is the border for inactive tabs.
var TabBorder = lipgloss.Border{
	Top:         "─",
	Bottom:      "─",
	Left:        "│",
	Right:       "│",
	TopLeft:     "╭",
	TopRight:    "╮",
	BottomLeft:  "┴",
	BottomRight: "┴",
}

var ActiveTabBorder = lipgloss.Border{
	Top:         "─",
	Bottom:      " ",
	Left:        "│",
	Right:       "│",
	TopLeft:     "╭",
	TopRight:    "╮",
	BottomLeft:  "┘",
	BottomRight: "└",
}

var TabStyle = lipgloss.NewStyle().
	Border(TabBorder).
	PaddingLeft(1).
	PaddingRight(1).
	BorderForeground(TabAccent)

var PromptStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(Border).
	PaddingLeft(1).
	PaddingRight(1)
