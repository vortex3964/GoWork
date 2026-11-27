package changeslist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const markerBlock = "keep1\n<<<<<<< old\nold\n=======\nnew\n>>>>>>> Ai change\nkeep2\n"

func newTestModel(t *testing.T) (Model, *WatchList, string) {
	t.Helper()
	dir := t.TempDir()
	wl, err := NewWatchList(nil)
	if err != nil {
		t.Fatalf("NewWatchList: %v", err)
	}
	t.Cleanup(func() { _ = wl.Watcher.Close() })
	wl.SetRoot(&dir)

	m := New(wl)
	m.SetRoot(dir)
	return m, wl, dir
}

func writeChangeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// TestRefreshDiffsSkipsClosedPane proves no file is scanned while the pane is
// closed, and that opening it triggers the deferred scan.
func TestRefreshDiffsSkipsClosedPane(t *testing.T) {
	m, wl, dir := newTestModel(t)
	path := writeChangeFile(t, dir, "a.txt", markerBlock)
	wl.Add(path)

	if m.Open() {
		t.Fatal("pane should start closed")
	}
	m.RefreshDiffs()
	if got := wl.GetChanges(path); len(got) != 0 {
		t.Fatalf("closed pane scanned the file: got %d changes", len(got))
	}

	m.open = true
	m.RefreshDiffs()
	if got := wl.GetChanges(path); len(got) != 1 {
		t.Fatalf("open pane did not scan the file: got %d changes", len(got))
	}
}

// TestRefreshDiffsSkipsUnchangedFiles proves the mtime+size gate avoids
// re-reading unchanged files while still refreshing changed ones.
func TestRefreshDiffsSkipsUnchangedFiles(t *testing.T) {
	m, wl, dir := newTestModel(t)
	path := writeChangeFile(t, dir, "a.txt", markerBlock)
	wl.Add(path)

	m.open = true
	m.RefreshDiffs()
	if got := wl.GetChanges(path); len(got) != 1 {
		t.Fatalf("initial refresh: got %d changes, want 1", len(got))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Same size and restored mtime: the stamp matches, so the replacement
	// content (which has no markers) must NOT be picked up.
	replacement := strings.Repeat("x", int(info.Size()))
	if err := os.WriteFile(path, []byte(replacement), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}
	m.RefreshDiffs()
	if got := wl.GetChanges(path); len(got) != 1 {
		t.Fatalf("unchanged file was re-read: got %d changes, want cached 1", len(got))
	}

	// A real change (different size) must refresh the entry.
	if err := os.WriteFile(path, []byte(replacement+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite changed: %v", err)
	}
	m.RefreshDiffs()
	if got := wl.GetChanges(path); len(got) != 0 {
		t.Fatalf("changed file was not refreshed: got %d changes, want 0", len(got))
	}
}

// TestRefreshDiffsRetainsWatchedFileOnTransientError proves a read/stat
// failure that is not "file does not exist" does not drop the watch entry or
// its last known changes, and that the file is picked up again once readable.
func TestRefreshDiffsRetainsWatchedFileOnTransientError(t *testing.T) {
	m, wl, dir := newTestModel(t)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeChangeFile(t, sub, "a.txt", markerBlock)
	key := filepath.Join("sub", "a.txt")
	wl.Add(key)

	m.open = true
	m.RefreshDiffs()
	if got := wl.GetChanges(key); len(got) != 1 {
		t.Fatalf("initial refresh: got %d changes, want 1", len(got))
	}

	// Make stat fail with ENOTDIR (a transient error, not ENOENT) by
	// replacing the parent directory with a regular file.
	moved := sub + ".moved"
	if err := os.Rename(sub, moved); err != nil {
		t.Fatalf("rename dir: %v", err)
	}
	if err := os.WriteFile(sub, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("replace dir with file: %v", err)
	}
	m.RefreshDiffs()
	if !wl.Has(key) {
		t.Fatal("transient stat error removed the watched file")
	}
	if got := wl.GetChanges(key); len(got) != 1 {
		t.Fatalf("transient stat error dropped cached changes: got %d, want 1", len(got))
	}

	// Restore with two hunks; the next refresh must resume normally.
	if err := os.Remove(sub); err != nil {
		t.Fatalf("remove decoy: %v", err)
	}
	if err := os.Rename(moved, sub); err != nil {
		t.Fatalf("restore dir: %v", err)
	}
	writeChangeFile(t, sub, "a.txt", markerBlock+markerBlock)
	m.RefreshDiffs()
	if got := wl.GetChanges(key); len(got) != 2 {
		t.Fatalf("recovered file was not refreshed: got %d changes, want 2", len(got))
	}
}

// TestRefreshDiffsDropsDeletedFile proves a file that is really gone is
// removed from the watch list (and its cached changes) instead of lingering.
func TestRefreshDiffsDropsDeletedFile(t *testing.T) {
	m, wl, dir := newTestModel(t)
	path := writeChangeFile(t, dir, "a.txt", markerBlock)
	key := "a.txt"
	wl.Add(key)

	m.open = true
	m.RefreshDiffs()
	if !wl.Has(key) {
		t.Fatal("file was not watched after initial scan")
	}
	if got := wl.GetChanges(key); len(got) != 1 {
		t.Fatalf("initial refresh: got %d changes, want 1", len(got))
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	m.RefreshDiffs()

	if wl.Has(key) {
		t.Fatal("deleted file is still watched")
	}
	if got := wl.GetChanges(key); len(got) != 0 {
		t.Fatalf("deleted file kept cached changes: got %d, want 0", len(got))
	}
	if _, ok := m.fileStamps[key]; ok {
		t.Fatal("deleted file kept its stamp")
	}
	m.RebuildRows()
	if len(m.filtered) != 0 {
		t.Fatalf("deleted file still shown: got %d rows", len(m.filtered))
	}
}
