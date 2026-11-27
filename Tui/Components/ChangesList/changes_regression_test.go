package changeslist

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// watchFile adds key to the watch list, scans it with the pane open and
// rebuilds the rows so the model is ready for an accept/reject action.
func watchFile(t *testing.T, m *Model, wl *WatchList, key string) {
	t.Helper()
	wl.Add(key)
	m.open = true
	m.RefreshDiffs()
	m.RebuildRows()
}

// TestAcceptRejectSelectedUsesProjectRoot proves accept/reject read and write
// the watched file under the project root, not a same-named file in the
// process's launch directory.
func TestAcceptRejectSelectedUsesProjectRoot(t *testing.T) {
	cases := []struct {
		name   string
		reject bool
		want   string
	}{
		{"accept", false, "keep1\nnew\nkeep2\n"},
		{"reject", true, "keep1\nold\nkeep2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, wl, dir := newTestModel(t)
			sub := filepath.Join(dir, "sub")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			path := writeChangeFile(t, sub, "a.txt", markerBlock)
			key := filepath.Join("sub", "a.txt")
			watchFile(t, &m, wl, key)
			if len(m.rows) != 2 {
				t.Fatalf("rows = %d, want header+change", len(m.rows))
			}

			// A decoy at the same relative path in the launch directory must
			// stay untouched.
			launch := t.TempDir()
			if err := os.MkdirAll(filepath.Join(launch, "sub"), 0o755); err != nil {
				t.Fatalf("mkdir launch: %v", err)
			}
			decoy := writeChangeFile(t, filepath.Join(launch, "sub"), "a.txt", markerBlock)
			t.Chdir(launch)

			m.cursor = 1 // the change row
			if tc.reject {
				if cmd := m.RejectSelected(); cmd == nil {
					t.Fatal("reject returned no event")
				} else if msg, ok := cmd().(ChangeEventMsg); !ok || strings.Contains(msg.Summary, "changed on disk") {
					t.Fatalf("unexpected reject message: %#v", msg)
				}
			} else {
				if cmd := m.AcceptSelected(); cmd == nil {
					t.Fatal("accept returned no event")
				} else if msg, ok := cmd().(ChangeEventMsg); !ok || strings.Contains(msg.Summary, "changed on disk") {
					t.Fatalf("unexpected accept message: %#v", msg)
				}
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read project file: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("project file = %q, want %q", got, tc.want)
			}
			decoyData, err := os.ReadFile(decoy)
			if err != nil {
				t.Fatalf("read decoy: %v", err)
			}
			if string(decoyData) != markerBlock {
				t.Fatalf("launch-dir decoy was modified: %q", decoyData)
			}
			if wl.Has(key) {
				t.Fatal("watch entry remained after its last change")
			}
		})
	}
}

// TestAcceptRejectAllUsesProjectRoot covers the header/global actions.
func TestAcceptRejectAllUsesProjectRoot(t *testing.T) {
	cases := []struct {
		name   string
		reject bool
		want   string
	}{
		{"accept all", false, "keep1\nnew\nkeep2\n"},
		{"reject all", true, "keep1\nold\nkeep2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, wl, dir := newTestModel(t)
			path := writeChangeFile(t, dir, "a.txt", markerBlock)
			key := "a.txt"
			watchFile(t, &m, wl, key)

			launch := t.TempDir()
			decoy := writeChangeFile(t, launch, "a.txt", markerBlock)
			t.Chdir(launch)

			m.cursor = 0 // the file header
			if tc.reject {
				if cmd := m.RejectAll(); cmd == nil {
					t.Fatal("reject all returned no event")
				} else if msg, ok := cmd().(ChangeEventMsg); !ok || !strings.Contains(msg.Summary, "across 1 files") {
					t.Fatalf("unexpected reject-all message: %#v", msg)
				}
			} else {
				if cmd := m.AcceptAll(); cmd == nil {
					t.Fatal("accept all returned no event")
				} else if msg, ok := cmd().(ChangeEventMsg); !ok || !strings.Contains(msg.Summary, "across 1 files") {
					t.Fatalf("unexpected accept-all message: %#v", msg)
				}
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read project file: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("project file = %q, want %q", got, tc.want)
			}
			decoyData, err := os.ReadFile(decoy)
			if err != nil {
				t.Fatalf("read decoy: %v", err)
			}
			if string(decoyData) != markerBlock {
				t.Fatalf("launch-dir decoy was modified: %q", decoyData)
			}
		})
	}
}

// TestAcceptSelectedStaleOffsetsRescan proves an accept against a file that
// shrank/changed after the scan re-scans and reports it instead of writing
// with stale offsets.
func TestAcceptSelectedStaleOffsetsRescan(t *testing.T) {
	m, wl, dir := newTestModel(t)
	path := writeChangeFile(t, dir, "a.txt", markerBlock)
	key := "a.txt"
	watchFile(t, &m, wl, key)

	replacement := "head\n<<<<<<< old\nx\n=======\ny\n>>>>>>> Ai change\n"
	if err := os.WriteFile(path, []byte(replacement), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	m.cursor = 1
	cmd := m.AcceptSelected()
	if cmd == nil {
		t.Fatal("stale accept returned no notice")
	}
	msg, ok := cmd().(ChangeEventMsg)
	if !ok || !strings.Contains(msg.Summary, "changed on disk") {
		t.Fatalf("unexpected stale message: %#v", msg)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != replacement {
		t.Fatalf("stale accept wrote the file: %q", got)
	}

	changes := wl.GetChanges(key)
	if len(changes) != 1 {
		t.Fatalf("rescan stored %d changes, want 1", len(changes))
	}
	wantStart := strings.Index(replacement, "<<<<<<<")
	if changes[0].Start != wantStart {
		t.Fatalf("rescan offset = %d, want %d", changes[0].Start, wantStart)
	}
	m.RebuildRows()
	if len(m.rows) != 2 {
		t.Fatalf("rows after rescan = %d, want header+change", len(m.rows))
	}
}

// TestAcceptSelectedCorruptOffsetsNoPanic tampers with the cached offsets
// while the stamp still matches, exercising the explicit bounds/marker check.
func TestAcceptSelectedCorruptOffsetsNoPanic(t *testing.T) {
	m, wl, dir := newTestModel(t)
	path := writeChangeFile(t, dir, "a.txt", markerBlock)
	key := "a.txt"
	watchFile(t, &m, wl, key)

	wl.mu.Lock()
	cl := wl.Changeslist[key]
	cl.Changes[0].End = len(markerBlock) + 100
	wl.Changeslist[key] = cl
	wl.mu.Unlock()
	m.RebuildRows()

	m.cursor = 1
	if cmd := m.AcceptSelected(); cmd == nil {
		t.Fatal("corrupt accept returned no notice")
	} else if msg, ok := cmd().(ChangeEventMsg); !ok || !strings.Contains(msg.Summary, "changed on disk") {
		t.Fatalf("unexpected corrupt-offset message: %#v", msg)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != markerBlock {
		t.Fatalf("corrupt offsets wrote the file: %q", got)
	}
	rescanned := wl.GetChanges(key)
	if len(rescanned) != 1 || !changeValid([]byte(markerBlock), rescanned[0]) {
		t.Fatalf("offsets were not re-scanned: %#v", rescanned)
	}
}

// TestAcceptAllStaleOffsetsNoPanic keeps the global action on the same safe
// path: stale offsets are skipped and the list is rebuilt.
func TestAcceptAllStaleOffsetsNoPanic(t *testing.T) {
	m, wl, dir := newTestModel(t)
	path := writeChangeFile(t, dir, "a.txt", markerBlock)
	key := "a.txt"
	watchFile(t, &m, wl, key)

	wl.mu.Lock()
	cl := wl.Changeslist[key]
	cl.Changes[0].Mid = len(markerBlock) + 100
	wl.Changeslist[key] = cl
	wl.mu.Unlock()
	m.RebuildRows()

	cmd := m.AcceptAll()
	if cmd == nil {
		t.Fatal("stale accept all returned no notice")
	}
	msg, ok := cmd().(ChangeEventMsg)
	if !ok || !strings.Contains(msg.Summary, "skipped") {
		t.Fatalf("unexpected stale accept-all message: %#v", msg)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != markerBlock {
		t.Fatalf("stale accept all wrote the file: %q", got)
	}
	if changes := wl.GetChanges(key); len(changes) != 1 || !changeValid([]byte(markerBlock), changes[0]) {
		t.Fatalf("accept all did not refresh offsets: %#v", changes)
	}
}

// TestStaleOffsetHelpersDoNotPanic covers the exported byte helpers directly.
func TestStaleOffsetHelpersDoNotPanic(t *testing.T) {
	data := []byte(markerBlock)
	huge := 1 << 20

	if got := AcceptChangeBytes(data, huge, huge+1, huge+2); !bytes.Equal(got, data) {
		t.Fatalf("accept with stale offsets changed data: %q", got)
	}
	if got := RejectChangeBytes(data, huge, huge+1, huge+2); !bytes.Equal(got, data) {
		t.Fatalf("reject with stale offsets changed data: %q", got)
	}
	if old, new := ChangeSections(data, Change{Start: huge, Mid: huge + 1, End: huge + 2}); old != "" || new != "" {
		t.Fatalf("stale sections = %q/%q, want empty", old, new)
	}

	stale := []Change{{Start: huge, Mid: huge + 1, End: huge + 2}}
	if got := AcceptAllChangesBytes(data, stale); !bytes.Equal(got, data) {
		t.Fatalf("accept all with stale offsets changed data: %q", got)
	}
	if got := RejectAllChangesBytes(data, stale); !bytes.Equal(got, data) {
		t.Fatalf("reject all with stale offsets changed data: %q", got)
	}

	// In-range offsets that don't land on marker text must also be rejected.
	wrong := Change{Start: 0, Mid: 0, End: 0}
	if got := AcceptChangeBytes(data, wrong.Start, wrong.Mid, wrong.End); !bytes.Equal(got, data) {
		t.Fatalf("accept with wrong markers changed data: %q", got)
	}
}

// TestApplyFilterDoesNotCorruptRows is the aliasing regression: filtering
// must never write through to the full row list.
func TestApplyFilterDoesNotCorruptRows(t *testing.T) {
	m, wl, dir := newTestModel(t)
	writeChangeFile(t, dir, "a.txt", markerBlock)
	writeChangeFile(t, dir, "b.txt", markerBlock)
	wl.Add("a.txt")
	wl.Add("b.txt")
	m.open = true
	m.RefreshDiffs()
	m.RebuildRows()

	if len(m.rows) != 4 {
		t.Fatalf("rows = %d, want 2 files x (header+change)", len(m.rows))
	}
	original := append([]row(nil), m.rows...)

	m.filter.SetValue("b.txt")
	m.applyFilter()
	if len(m.filtered) != 2 {
		t.Fatalf("filtered = %d, want b.txt's header+change", len(m.filtered))
	}
	for i := range original {
		if m.rows[i] != original[i] {
			t.Fatalf("filtering overwrote row %d: %#v, want %#v", i, m.rows[i], original[i])
		}
	}

	m.filter.SetValue("")
	m.applyFilter()
	if len(m.filtered) != len(original) {
		t.Fatalf("filtered after clearing = %d, want %d", len(m.filtered), len(original))
	}
	for i := range original {
		if m.filtered[i] != original[i] {
			t.Fatalf("filtered row %d = %#v, want %#v", i, m.filtered[i], original[i])
		}
		if m.rows[i] != original[i] {
			t.Fatalf("rows changed after filtering: %#v, want %#v", m.rows[i], original[i])
		}
	}
}
