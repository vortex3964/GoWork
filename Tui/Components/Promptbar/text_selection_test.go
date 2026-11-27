package promptbar

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"GoWork/Tui/Style"
)

func promptTextOrigin(m Model) (x, y int) {
	x = marginSide + style.PromptStyle.GetBorderLeftSize() + style.PromptStyle.GetPaddingLeft()
	y = m.palette.Height() + style.PromptStyle.GetBorderTopSize()
	return x, y
}

func TestTextDragSelectsPromptSubstring(t *testing.T) {
	m := New()
	m.SetWidth(100)
	m.SetValue("hello prompt text")
	x, y := promptTextOrigin(m)

	if !m.BeginTextDrag(x, y) {
		t.Fatal("BeginTextDrag missed the first prompt character")
	}
	if !m.UpdateTextDrag(x+5, y) {
		t.Fatal("UpdateTextDrag rejected the drag focus")
	}
	if !m.TextDragMoved() {
		t.Fatal("drag did not report movement")
	}
	preview, ok := m.TextDragText()
	if !ok || preview != "hello" {
		t.Fatalf("preview = %q (ok=%v), want %q", preview, ok, "hello")
	}

	text, ok := m.EndTextDrag()
	if !ok || text != "hello" {
		t.Fatalf("EndTextDrag = %q (ok=%v), want %q", text, ok, "hello")
	}
	if m.TextDragActive() {
		t.Fatal("drag state survived EndTextDrag")
	}
	if m.Value() != "hello prompt text" {
		t.Fatalf("drag changed the prompt value to %q", m.Value())
	}
}

func TestTextDragSelectsAcrossPromptLines(t *testing.T) {
	m := New()
	m.SetWidth(100)
	m.SetValue("first line\nsecond line")
	x, y := promptTextOrigin(m)

	m.BeginTextDrag(x, y)
	m.UpdateTextDrag(x+6, y+1)
	text, ok := m.EndTextDrag()
	if !ok || text != "first line\nsecond" {
		t.Fatalf("EndTextDrag = %q (ok=%v), want %q", text, ok, "first line\nsecond")
	}
}

func TestTextDragHighlightsAndClickWithoutMotionDoesNotCopy(t *testing.T) {
	m := New()
	m.SetWidth(100)
	m.SetValue("hello prompt text")
	x, y := promptTextOrigin(m)
	before := m.View()

	if !m.BeginTextDrag(x, y) {
		t.Fatal("BeginTextDrag missed the prompt text")
	}
	if m.TextDragMoved() {
		t.Fatal("press without motion reported a moved drag")
	}
	if text, ok := m.EndTextDrag(); ok || text != "" {
		t.Fatalf("click-only drag copied %q", text)
	}
	if m.View() != before {
		t.Fatal("click-only drag changed the rendered view")
	}

	m.BeginTextDrag(x, y)
	m.UpdateTextDrag(x+5, y)
	after := m.View()
	if after == before {
		t.Fatal("dragging did not change the rendered view")
	}
	if !strings.Contains(ansi.Strip(after), "hello prompt text") {
		t.Fatalf("highlighted view lost the prompt text:\n%s", ansi.Strip(after))
	}
	if !strings.Contains(after, "\x1b[") {
		t.Fatal("highlighted view has no styling escape")
	}
}

func TestPromptVisualRowsMapWrappedText(t *testing.T) {
	m := New()
	m.SetWidth(20) // inner textarea width is 10 cells
	m.SetValue("one two three four")
	rows := promptVisualRows(m.Value(), m.area.Width())
	if len(rows) < 2 {
		t.Fatalf("wrapped prompt has %d rows, want at least 2", len(rows))
	}
	if rows[0].line != 0 || rows[0].start != 0 {
		t.Fatalf("first row = %+v, want line 0 starting at rune 0", rows[0])
	}
	var rebuilt strings.Builder
	for i, r := range rows {
		if i > 0 {
			rebuilt.WriteByte('\n')
		}
		runes := []rune(strings.Split(m.Value(), "\n")[r.line])
		rebuilt.WriteString(string(runes[r.start:r.end]))
	}
	got := strings.ReplaceAll(rebuilt.String(), "\n", "")
	want := strings.ReplaceAll(m.Value(), " ", "")
	if strings.ReplaceAll(got, " ", "") != want {
		t.Fatalf("wrapped row ranges lost text: got %q want %q", got, want)
	}
}
