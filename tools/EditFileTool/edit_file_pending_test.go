package editfiletool_test

import (
	"context"
	"testing"

	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/tools"
	editfiletool "GoWork/tools/EditFileTool"
)

// setupToolWithPending seeds a file containing one marker block, registers it
// with a real WatchList and scans it so GetChanges returns the pending hunk.
func setupToolWithPending(t *testing.T, name, content string) (testTool, *cl.WatchList, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := writeTestFile(t, dir, name, content)

	wl, err := cl.NewWatchList(nil)
	if err != nil {
		t.Fatalf("NewWatchList: %v", err)
	}
	wl.SetRoot(&dir)
	wl.Add(path)

	m := cl.New(wl)
	m.SetRoot(dir)
	m.Toggle() // opens the pane and triggers the initial scan

	args, err := tools.InitDispatchArgs(dir, wl, nil)
	if err != nil {
		wl.Watcher.Close()
		t.Fatalf("failed to init dispatch args: %v", err)
	}
	t.Cleanup(func() {
		_ = args.Root.Close()
		_ = wl.Watcher.Close()
	})
	return testTool{AgentTool: editfiletool.New(), args: args}, wl, dir, path
}

// TestRun_WithPendingChange_PreservesMarkersAndMatchesLegacyDiff proves an
// edit outside an existing marker hunk leaves that hunk in place and produces
// the same bytes the old full-file diff produced.
func TestRun_WithPendingChange_PreservesMarkersAndMatchesLegacyDiff(t *testing.T) {
	const src = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\nc\nd\n"
	tool, wl, _, path := setupToolWithPending(t, "pending.go", src)

	result, err := tool.Run(context.Background(), mkInput(t, "pending.go", 4, 4, "D"))
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	changes := wl.GetChanges("pending.go")
	if len(changes) != 1 {
		t.Fatalf("watch list changes = %d, want 1", len(changes))
	}
	updated := []byte("a\nB\nc\nD\n") // line 4 (d) replaced in the accepted view
	want := cl.MergeMarkerBytes(cl.RejectAllChangesBytes([]byte(src), changes), updated)

	got := readFile(t, path)
	if got != string(want) {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRun_EditOverlappingPendingHunk_AbsorbsMarker covers changing a line
// that is itself the new side of a pending hunk.
func TestRun_EditOverlappingPendingHunk_AbsorbsMarker(t *testing.T) {
	const src = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\nc\n"
	tool, wl, _, path := setupToolWithPending(t, "overlap.go", src)

	result, err := tool.Run(context.Background(), mkInput(t, "overlap.go", 2, 2, "X"))
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	changes := wl.GetChanges("overlap.go")
	updated := []byte("a\nX\nc\n")
	want := cl.MergeMarkerBytes(cl.RejectAllChangesBytes([]byte(src), changes), updated)

	got := readFile(t, path)
	if got != string(want) {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
