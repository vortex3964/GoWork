// Package style is the single source of truth for color and shape
// across every component. Components should reference these tokens
// instead of hardcoding hex values or defining their own — that's
// what makes a future theme swap (or a light/dark mode) a one-file
// change instead of a grep-and-replace.
package style

import "github.com/charmbracelet/lipgloss"

// Palette (Ayu Dark)
var (
	Primary    = lipgloss.Color("#E6B450") // Ayu accent gold
	Border     = lipgloss.Color("#E6B450") // Ayu accent gold
	Muted      = lipgloss.Color("#626A73") // Ayu comment gray
	Text       = lipgloss.Color("#B3B1AD") // Ayu fg
	Background = lipgloss.Color("#0A0E14") // Ayu bg
	Danger     = lipgloss.Color("#FF3333") // Ayu error red
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
