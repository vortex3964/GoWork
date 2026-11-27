package messagearea

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// stripANSI removes terminal escape sequences so assertions can match the
// literal, human-readable text of a rendered view.
func stripANSI(s string) string { return ansi.Strip(s) }

// buildStreamModel makes a message area with n prior messages, the way a long
// session looks before the next answer starts streaming.
func buildStreamModel(n int) Model {
	m := New()
	m.SetSize(100, 30)
	for i := 0; i < n; i++ {
		m.AppendMessage(fmt.Sprintf("message %d: %s", i, strings.Repeat("some prior detail ", 3)), i%2 == 0)
	}
	return m
}

// TestLiveDeltaWorkDoesNotScaleWithHistory is the regression test for #23:
// 100 streamed deltas must measure the same (small) number of content lines
// whether there are 5 or 500 prior messages. A full-history push would make
// the two diverge by the size of the conversation.
func TestLiveDeltaWorkDoesNotScaleWithHistory(t *testing.T) {
	measureDeltas := func(history int) int {
		m := buildStreamModel(history)
		idx := m.LiveAssistantStart()
		before := m.vp.lineMeasures

		var content strings.Builder
		for i := 0; i < 100; i++ {
			content.WriteString("token ")
			m.LiveAssistantUpdate(idx, content.String())
		}
		if got := m.vp.lineMeasures - before; got <= 0 {
			t.Fatalf("%d history: no content lines measured at all", history)
		}
		return m.vp.lineMeasures - before
	}

	small := measureDeltas(5)
	large := measureDeltas(500)

	if large > small+20 {
		t.Fatalf("delta work scales with history: %d lines measured with 500 prior messages vs %d with 5", large, small)
	}
	// 100 deltas should only touch the live block's handful of lines each.
	if large > 100*50 {
		t.Fatalf("100 deltas measured %d content lines, want only the live tail", large)
	}
}

// TestLiveStreamKeepsHistoryAndTailCorrect checks the incremental buffer still
// renders a long conversation correctly: the newest prior message stays
// visible above the streamed answer, and scrolling reaches older messages.
func TestLiveStreamKeepsHistoryAndTailCorrect(t *testing.T) {
	m := buildStreamModel(500)
	idx := m.LiveAssistantStart()
	m.LiveAssistantUpdate(idx, "the streamed answer")

	view := stripANSI(m.View())
	if !strings.Contains(view, "message 499") {
		t.Fatalf("view lost the newest prior message:\n%s", view)
	}
	if !strings.Contains(view, "the streamed answer") {
		t.Fatalf("view lost the streamed tail:\n%s", view)
	}

	m.ScrollUp()
	m.ScrollUp()
	if m.vp.YOffset() >= m.vp.maxYOffset() {
		t.Fatal("ScrollUp did not move the viewport away from the bottom")
	}
	scrolled := stripANSI(m.View())
	if strings.Contains(scrolled, "the streamed answer") {
		t.Fatalf("scrolled view still shows the streamed tail:\n%s", scrolled)
	}

	m.vp.GotoTop()
	if !strings.Contains(stripANSI(m.View()), "message 0") {
		t.Fatalf("GotoTop did not show the beginning of the conversation")
	}
}

// TestResizeRecomputesWrappedRows covers resize handling: the soft-wrap cache
// must be rebuilt for the new content width, and the tail must stay visible.
func TestResizeRecomputesWrappedRows(t *testing.T) {
	m := buildStreamModel(20)
	idx := m.LiveAssistantStart()
	m.LiveAssistantUpdate(idx, strings.Repeat("wrap me ", 60))

	for _, size := range []struct{ w, h int }{{100, 30}, {40, 15}, {120, 40}} {
		m.SetSize(size.w, size.h)
		view := stripANSI(m.View())
		if !strings.Contains(view, "wrap me") {
			t.Fatalf("%dx%d: view lost the live answer:\n%s", size.w, size.h, view)
		}
		maxLine := 0
		for _, line := range strings.Split(view, "\n") {
			if w := len([]rune(line)); w > maxLine {
				maxLine = w
			}
		}
		if maxLine > size.w {
			t.Fatalf("%dx%d: rendered line width %d exceeds outer width", size.w, size.h, maxLine)
		}
	}
}

// BenchmarkStreamDelta compares pushing the whole conversation into the
// viewport on every delta (the pre-#23 behaviour) with replacing only the live
// tail. The line-measures metric makes the reduced work explicit, independent
// of machine speed.
func BenchmarkStreamDelta(b *testing.B) {
	for _, mode := range []string{"full-history", "incremental"} {
		b.Run(mode, func(b *testing.B) {
			m := buildStreamModel(500)
			// The old path cached this join but still handed the whole
			// string to the viewport on every delta.
			prefix := strings.Join(m.rendered, blockSeparator)
			idx := m.LiveAssistantStart()

			before := m.vp.lineMeasures
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				content := strings.Repeat("token ", 200+(i%200))
				if mode == "full-history" {
					block := m.renderLiveBlock(content, m.contentWidth())
					m.vp.SetLines(strings.Split(prefix+blockSeparator+block, "\n"))
					m.vp.GotoBottom()
					continue
				}
				m.LiveAssistantUpdate(idx, content)
			}
			b.StopTimer()
			b.ReportMetric(float64(m.vp.lineMeasures-before)/float64(b.N), "line-meas/op")
		})
	}
}
