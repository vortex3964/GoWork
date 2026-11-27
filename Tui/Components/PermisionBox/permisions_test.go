package permisionbox

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func testImpact() Impact {
	return Impact{
		Summary: "Edits a.go: replaces 1 occurrence (2 line(s) -> 3 line(s)); the file currently has 10 line(s).",
		Preview: "- old line one\n- old line two\n+ new line one\n+ new line two\n+ new line three",
	}
}

func TestViewRendersImpactAndControls(t *testing.T) {
	m := New("write_file", "Edits an existing file by replacing an exact snippet.", "a.go", testImpact())
	m.SetSize(100, 40)

	view := m.View()
	for _, want := range []string{
		"PERMISSION REQUESTED",
		"Tool: write_file",
		"Impact: Edits a.go",
		"- old line one",
		"+ new line one",
		"Runs: a.go",
		"Accept",
		"Reject",
		"Accept All",
		"Reason:",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("permission view missing %q:\n%s", want, view)
		}
	}
}

func TestViewFitsSmallTerminals(t *testing.T) {
	sizes := [][2]int{
		{80, 24},
		{60, 16},
		{40, 20},
		{30, 13},
		{24, 13},
		{20, 10},
	}
	for _, size := range sizes {
		m := New("write_file", "Edits an existing file by replacing an exact snippet.", "a.go", testImpact())
		m.SetSize(size[0], size[1])
		view := m.View()

		if w := lipgloss.Width(view); w > size[0] {
			t.Errorf("%dx%d view width = %d, overflows terminal", size[0], size[1], w)
		}
		if h := lipgloss.Height(view); h > size[1] {
			t.Errorf("%dx%d view height = %d, overflows terminal", size[0], size[1], h)
		}
	}
}

func TestLongImpactPreviewStaysBounded(t *testing.T) {
	longSummary := strings.Repeat("This edit changes a lot of lines and the explanation is intentionally long. ", 8)
	var preview strings.Builder
	for i := 0; i < 40; i++ {
		preview.WriteString("+ added line ")
		preview.WriteString(strings.Repeat("x", 40))
		preview.WriteString("\n")
	}
	m := New("edit_file", strings.Repeat("generic description ", 20), "big.go", Impact{
		Summary: longSummary,
		Preview: preview.String(),
	})
	m.SetSize(40, 20)
	view := m.View()

	if w := lipgloss.Width(view); w > 40 {
		t.Errorf("view width = %d, want <= 40", w)
	}
	if h := lipgloss.Height(view); h > 20 {
		t.Errorf("view height = %d, want <= 20", h)
	}
	if !strings.Contains(view, "Impact:") {
		t.Errorf("impact summary missing from cramped view:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Errorf("expected truncation marker for long impact/preview:\n%s", view)
	}
}
