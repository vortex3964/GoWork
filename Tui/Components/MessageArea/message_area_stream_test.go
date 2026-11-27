package messagearea

import (
	"strings"
	"testing"
)

// TestLiveStreamRendersMarkdownOnce feeds the message area many growing
// snapshots like the update loop's throttled deltas, then finalizes. While
// streaming it must only build the cheap plain-text block; the full answer
// gets exactly one glamour render when the stream ends.
func TestLiveStreamRendersMarkdownOnce(t *testing.T) {
	m := New()
	m.SetSize(100, 30)

	idx := m.LiveAssistantStart()
	if m.renderCount != 0 {
		t.Fatalf("starting a live message rendered markdown %d times, want 0", m.renderCount)
	}

	var content strings.Builder
	for i := 0; i < 250; i++ {
		content.WriteString("token ")
		m.LiveAssistantUpdate(idx, content.String())
		if m.renderCount != 0 {
			t.Fatalf("delta %d triggered a glamour render (count=%d)", i, m.renderCount)
		}
	}

	// A resize mid-stream must keep the live block plain, too.
	m.SetSize(80, 24)
	if m.renderCount != 0 {
		t.Fatalf("resize during stream rendered markdown %d times, want 0", m.renderCount)
	}

	// The streamed view is the raw text while tokens are arriving.
	if !strings.Contains(strings.Join(m.vp.lines, "\n"), "token token") {
		t.Fatalf("live content missing streamed text")
	}

	m.LiveAssistantFinish(idx, "# Heading\n\nSome **bold** text")
	if m.renderCount != 1 {
		t.Fatalf("final content rendered markdown %d times, want exactly 1", m.renderCount)
	}
	view := strings.Join(m.vp.lines, "\n")
	if !strings.Contains(view, "Heading") {
		t.Fatalf("final content missing rendered heading, got:\n%s", view)
	}
	if strings.Contains(view, "# Heading") {
		t.Fatalf("final content still shows raw markdown marker:\n%s", view)
	}

	// Late updates after finalization are ignored, so the markdown render
	// can't be undone by a stray trailing delta.
	m.LiveAssistantUpdate(idx, "late")
	if m.renderCount != 1 {
		t.Fatalf("update after finish rendered markdown again (count=%d)", m.renderCount)
	}
}

// TestContentCacheTracksAllMutations keeps the incrementally maintained
// viewport line buffer in sync with the rendered blocks across appends, live
// updates, tool updates, finalization and resize.
func TestContentCacheTracksAllMutations(t *testing.T) {
	m := New()
	m.SetSize(90, 28)

	assertContent := func(stage string) {
		t.Helper()
		want := strings.Split(strings.Join(m.rendered, blockSeparator), "\n")
		got := strings.Join(m.vp.lines, "\n")
		if got != strings.Join(want, "\n") {
			t.Fatalf("%s: viewport lines out of sync (got %d lines, want %d)", stage, len(m.vp.lines), len(want))
		}
	}

	m.AppendMessage("hello", true)
	assertContent("append user")

	toolIdx := m.AppendTool("shell", "ls")
	assertContent("append tool")

	liveIdx := m.LiveAssistantStart()
	assertContent("start live")

	m.LiveAssistantUpdate(liveIdx, "partial")
	assertContent("stream delta")

	m.UpdateToolMessage(toolIdx, ToolDone, "file.txt")
	assertContent("tool update")

	m.LiveAssistantFinish(liveIdx, "final answer")
	assertContent("finish live")

	m.AppendMessage("another", true)
	assertContent("append after finish")

	m.SetSize(60, 20)
	assertContent("resize")
}

// TestAppendAfterLiveBlockIsNeverDropped is the regression test for the
// streaming append cache: messages appended after the live message must stay
// in the viewport, in order, even when later deltas replace the live block and
// the block grows or shrinks.
func TestAppendAfterLiveBlockIsNeverDropped(t *testing.T) {
	m := New()
	m.SetSize(90, 28)

	m.AppendMessage("before live", true)
	liveIdx := m.LiveAssistantStart()
	m.LiveAssistantUpdate(liveIdx, "stream one")

	m.AppendMessage("after live one", true)
	toolIdx := m.AppendTool("shell", "echo hi")
	m.AppendMessage("after-live-two", false)

	m.LiveAssistantUpdate(liveIdx, "stream two with more text")
	m.LiveAssistantFinish(liveIdx, "final **answer**")
	m.UpdateToolMessage(toolIdx, ToolDone, "hi")

	want := strings.Split(strings.Join(m.rendered, blockSeparator), "\n")
	got := strings.Join(m.vp.lines, "\n")
	if got != strings.Join(want, "\n") {
		t.Fatalf("viewport cache lost messages appended after the live block (got %d lines, want %d)", len(m.vp.lines), len(want))
	}

	content := strings.Join(m.vp.lines, "\n")
	order := []string{"before live", "final", "after live one", "shell", "after-live-two"}
	last := -1
	for _, text := range order {
		idx := strings.Index(content, text)
		if idx < 0 {
			t.Fatalf("appended message %q missing from the viewport:\n%s", text, content)
		}
		if idx < last {
			t.Fatalf("message %q is out of order in the viewport", text)
		}
		last = idx
	}
}
