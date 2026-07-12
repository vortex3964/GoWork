// Package focus defines the single enum that root uses to decide who
// gets raw keypresses. Nothing but main.go ever WRITES a Focus value;
// child components only READ it (passed in, or checked against a value
// main.go owns) to decide whether they should react to a keybind at all.

package focus

type Focus int

const (
	Prompt Focus = iota
	// Viewport: the scrollable message list owns the keyboard.
	Viewport
)
