// Package style is the single source of truth for color and shape
// across every component. Components should reference these tokens
// instead of hardcoding hex values or defining their own — that's
// what makes a future theme swap (or a light/dark mode) a one-file
// change instead of a grep-and-replace.
package style

import "github.com/charmbracelet/lipgloss"

//Pallete
var (
	Primary    = lipgloss.Color("#7010E3") 
	Border     = lipgloss.Color("#7010E3")
	Muted      = lipgloss.Color("#5C5C5C")
	Text       = lipgloss.Color("#FAFAFA")
	Background = lipgloss.Color("#1A1A1A")
	Danger     = lipgloss.Color("#E33636")
)

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
	BorderForeground(Border)
