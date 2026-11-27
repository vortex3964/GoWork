package messagearea

import (
	"strings"
	"testing"
)

func buildDragModel(t *testing.T) Model {
	t.Helper()
	m := New()
	m.SetSize(100, 30)
	m.AppendMessage("first user text", true)
	idx := m.AppendTool("read_file", "main.go")
	m.UpdateToolMessage(idx, ToolDone, "package main\nfunc main() {}")
	m.AppendMessage("last assistant text", false)
	return m
}

// dragPoint returns band-relative screen coordinates for a display column on
// a viewport content line.
func dragPoint(m Model, line, col int) (x, y int) {
	width := m.vp.contentWidth()
	if width < 1 {
		width = 1
	}
	row := m.vp.RowAtLine(line) + col/width
	x = padSide + m.vp.Style.GetBorderLeftSize() + m.vp.Style.GetPaddingLeft() + col%width
	y = m.vp.Style.GetBorderTopSize() + row - m.vp.YOffset()
	return x, y
}

func dragTo(m *Model, fromLine, fromCol, toLine, toCol int) {
	fromX, fromY := dragPoint(*m, fromLine, fromCol)
	if !m.BeginTextDrag(fromX, fromY) {
		panic("drag start missed the message text")
	}
	toX, toY := dragPoint(*m, toLine, toCol)
	m.UpdateTextDrag(toX, toY)
}

func TestTextDragSelectsPartOfMessage(t *testing.T) {
	m := buildDragModel(t)
	m.vp.SetYOffset(0)
	bodyLine := m.blockStart[0] + 1
	dragTo(&m, bodyLine, 0, bodyLine, 5)

	if !m.TextDragActive() || !m.TextDragMoved() {
		t.Fatal("drag did not become a text selection")
	}
	preview, ok := m.TextDragText()
	if !ok || preview != "first" {
		t.Fatalf("preview = %q (ok=%v), want %q", preview, ok, "first")
	}

	text, ok := m.EndTextDrag()
	if !ok || text != "first" {
		t.Fatalf("EndTextDrag = %q (ok=%v), want %q", text, ok, "first")
	}
	if m.TextDragActive() {
		t.Fatal("drag state survived EndTextDrag")
	}
}

func TestTextDragExtractsAcrossMessageBlocks(t *testing.T) {
	m := buildDragModel(t)
	m.vp.SetYOffset(0)
	firstBody := m.blockStart[0] + 1
	lastBody := m.blockStart[2] + 1
	dragTo(&m, firstBody, 6, lastBody, 4)

	text, ok := m.TextDragText()
	if !ok {
		t.Fatal("cross-block drag has no text")
	}
	for _, want := range []string{"user text", "read_file main.go", "package main", "func main() {}", "last"} {
		if !strings.Contains(text, want) {
			t.Errorf("selection missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Errorf("selection contains ANSI styling: %q", text)
	}
	if strings.Index(text, "user text") > strings.Index(text, "last") {
		t.Error("selection is not in display order")
	}
}

func TestTextDragClickWithoutMotionKeepsMessageSelection(t *testing.T) {
	m := buildDragModel(t)
	m.vp.SetYOffset(0)
	if !m.HandleClick(10, 1, false) {
		t.Fatal("HandleClick missed the first message")
	}
	if !m.BeginTextDrag(10, 1) {
		t.Fatal("BeginTextDrag rejected a click inside the message text")
	}
	if m.TextDragMoved() {
		t.Fatal("a press without motion reported a moved drag")
	}
	if text, ok := m.EndTextDrag(); ok || text != "" {
		t.Fatalf("click-only drag copied %q", text)
	}
	if !m.InCopyMode() {
		t.Fatal("click-only drag cancelled the existing copy selection")
	}
	if from, to, ok := m.Selection(); !ok || from != 0 || to != 0 {
		t.Fatalf("click selection = %d..%d (ok=%v), want 0..0", from, to, ok)
	}
}

// TestDragMotionReusesCachedViewport is the regression guard for the copy
// drag's per-motion cost: moving the pointer must only repaint the highlight,
// never rebuild the message viewport. viewRenders stays flat across all
// motion/frame pairs once the drag has started.
func TestDragMotionReusesCachedViewport(t *testing.T) {
	m := buildDragModel(t)
	m.vp.SetYOffset(0)
	bodyLine := m.blockStart[0] + 1
	dragTo(&m, bodyLine, 0, bodyLine, 3)

	before := m.vp.viewRenders
	for i := 0; i < 100; i++ {
		x, y := dragPoint(m, bodyLine, i%8)
		if !m.UpdateTextDrag(x, y) {
			t.Fatalf("motion %d missed the drag focus", i)
		}
		_ = m.View()
	}
	if got := m.vp.viewRenders - before; got != 0 {
		t.Fatalf("drag motion rendered the full viewport %d times, want 0", got)
	}
}

func BenchmarkDragMotionView(b *testing.B) {
	m := buildStreamModel(200)
	m.BeginTextDrag(6, 1)
	m.UpdateTextDrag(12, 1)
	before := m.vp.viewRenders

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.UpdateTextDrag(6+i%20, 1)
		_ = m.View()
	}
	b.StopTimer()
	b.ReportMetric(float64(m.vp.viewRenders-before)/float64(b.N), "viewport-render/op")
}

func TestTextDragDrawsVisibleHighlight(t *testing.T) {
	m := buildDragModel(t)
	m.vp.SetYOffset(0)
	before := m.View()
	bodyLine := m.blockStart[0] + 1
	dragTo(&m, bodyLine, 0, bodyLine, 5)
	after := m.View()

	if after == before {
		t.Fatal("dragging did not change the rendered view")
	}
	if !strings.Contains(stripANSI(after), "first user text") {
		t.Fatalf("highlighted view lost the message text:\n%s", stripANSI(after))
	}
	if !strings.Contains(after, "\x1b[") {
		t.Fatal("highlighted view has no styling escape")
	}
}
