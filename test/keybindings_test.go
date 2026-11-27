package test

import (
	"testing"

	gowork "GoWork/internal/gowork"
)

func TestIsTurnInterrupt(t *testing.T) {
	tests := []struct {
		key         string
		turnRunning bool
		want        bool
	}{
		// ctrl+i and tab share byte 0x09 and both stop a running turn.
		{"ctrl+i", true, true},
		{"tab", true, true},
		// When idle, tab must keep switching tabs and ctrl+i does nothing.
		{"ctrl+i", false, false},
		{"tab", false, false},
		// Unrelated keys are never interrupts.
		{"enter", true, false},
	}
	for _, tt := range tests {
		if got := gowork.IsTurnInterrupt(tt.key, tt.turnRunning); got != tt.want {
			t.Errorf("isTurnInterrupt(%q, %v) = %v, want %v",
				tt.key, tt.turnRunning, got, tt.want)
		}
	}
}
