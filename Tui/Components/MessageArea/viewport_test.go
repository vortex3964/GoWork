package messagearea

import (
	"fmt"
	"strings"
	"testing"

	bviewport "charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// TestContentViewportMatchesBubblesViewport locks in rendering parity for
// borders, padding, soft wrapping and vertical offsets. The custom viewport
// exists purely as a performance optimisation, so its visible output must
// stay identical to the bubbles viewport it replaced.
func TestContentViewportMatchesBubblesViewport(t *testing.T) {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		PaddingLeft(2).
		PaddingRight(2)

	long := strings.Repeat("word ", 40)
	ansiLine := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Render("styled") + " tail " + strings.Repeat("x", 100)
	cases := map[string][]string{
		"empty":     {},
		"short":     {"first", "second", "third"},
		"blank gap": {"a", "", "b", "", "", "c"},
		"long":      {"short", long, "after " + long},
		"unbroken":  {strings.Repeat("x", 300), "next"},
		"ansi":      {ansiLine, "plain"},
	}
	sizes := []struct{ w, h int }{
		{40, 10},
		{80, 7},
		{20, 5},
		{120, 4},
	}
	offsets := []int{0, 1, 3, 7, 100}

	for name, lines := range cases {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", name, size.w, size.h), func(t *testing.T) {
				old := bviewport.New()
				old.Style = style
				old.SoftWrap = true
				old.SetWidth(size.w)
				old.SetHeight(size.h)
				old.SetContent(strings.Join(lines, "\n"))

				next := newContentViewport(style)
				next.SetWidth(size.w)
				next.SetHeight(size.h)
				next.SetLines(lines)

				for _, offset := range offsets {
					old.SetYOffset(offset)
					next.SetYOffset(offset)
					if got, want := next.View(), old.View(); got != want {
						t.Fatalf("offset %d: view differs\n--- got ---\n%s\n--- want ---\n%s", offset, got, want)
					}
				}
			})
		}
	}
}
