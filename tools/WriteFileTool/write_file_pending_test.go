package writefiletool_test

import (
	"bytes"
	"context"
	"testing"

	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/tools"
	writefiletool "GoWork/tools/WriteFileTool"
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
	return testTool{AgentTool: writefiletool.New(), args: args}, wl, dir, path
}

func TestRun_WithPendingChange_PreservesMarkersAndMatchesLegacyDiff(t *testing.T) {
	const src = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\nc\nd\n"
	tool, wl, _, path := setupToolWithPending(t, "pending.go", src)

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "pending.go",
		Old:      "d",
		New:      "D",
	})
	result, err := tool.Run(context.Background(), input)
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
	accepted := cl.AcceptAllChangesBytes([]byte(src), changes)
	updated := bytes.Replace(accepted, []byte("d"), []byte("D"), 1)
	want := cl.MergeMarkerBytes(cl.RejectAllChangesBytes([]byte(src), changes), updated)

	got := readFile(t, path)
	if got != string(want) {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRun_EditAdjacentToPendingHunk_MergesHunks covers a substitution whose
// line directly follows a pending block; the old diff emitted one combined
// hunk and so must this path.
func TestRun_EditAdjacentToPendingHunk_MergesHunks(t *testing.T) {
	const src = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\nc\nd\n"
	tool, wl, _, path := setupToolWithPending(t, "adjacent.go", src)

	input := mustMarshal(t, writefiletool.Input{
		FilePath: "adjacent.go",
		Old:      "c",
		New:      "C",
	})
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	changes := wl.GetChanges("adjacent.go")
	accepted := cl.AcceptAllChangesBytes([]byte(src), changes)
	updated := bytes.Replace(accepted, []byte("c"), []byte("C"), 1)
	want := cl.MergeMarkerBytes(cl.RejectAllChangesBytes([]byte(src), changes), updated)

	got := readFile(t, path)
	if got != string(want) {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRun_ReplaceAllWithPendingChange_MatchesLegacyDiff(t *testing.T) {
	const src = "x\n<<<<<<< old\na\n=======\nA\n>>>>>>> Ai change\nfoo\nmid\nfoo\ny\n"
	tool, wl, _, path := setupToolWithPending(t, "all.go", src)

	input := mustMarshal(t, writefiletool.Input{
		FilePath:   "all.go",
		Old:        "foo",
		New:        "bar",
		ReplaceAll: true,
	})
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected code-level error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got tool error: %s", result.Content)
	}

	changes := wl.GetChanges("all.go")
	accepted := cl.AcceptAllChangesBytes([]byte(src), changes)
	updated := bytes.ReplaceAll(accepted, []byte("foo"), []byte("bar"))
	want := cl.MergeMarkerBytes(cl.RejectAllChangesBytes([]byte(src), changes), updated)

	got := readFile(t, path)
	if got != string(want) {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
