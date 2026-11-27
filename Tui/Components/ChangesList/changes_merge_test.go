package changeslist

import (
	"bytes"
	"testing"
)

// legacyMerge is the old full-file implementation, used as the oracle.
func legacyMerge(src []byte, changes []Change, edits []Edit) []byte {
	accepted := AcceptAllChangesBytes(src, changes)
	updated := applyEdits(accepted, edits)
	return MergeMarkerBytes(RejectAllChangesBytes(src, changes), updated)
}

func TestInsertMergeMarkerHunkMatchesFullDiff(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		start int
		end   int
		new   string
	}{
		{"replace line", "a\nb\nc\n", 2, 4, "B\n"},
		{"insert line", "a\nb\n", 2, 2, "x\n"},
		{"delete line", "a\nb\nc\n", 2, 4, ""},
		{"append at EOF", "a\nb\n", 4, 4, "c\n"},
		{"replace entire file", "a\nb\n", 0, 4, "x\ny\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := InsertMergeMarkerHunk([]byte(tc.base), tc.start, tc.end, []byte(tc.new))
			updated := string([]byte(tc.base)[:tc.start]) + tc.new + string([]byte(tc.base)[tc.end:])
			want := MergeMarkerBytes([]byte(tc.base), []byte(updated))
			if !bytes.Equal(got, want) {
				t.Errorf("hunk mismatch:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

func TestMergeMarkerEditsMatchesFullDiff(t *testing.T) {
	const oneHunk = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\nc\nd\n"
	const multiHunk = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\nc\n<<<<<<< old\nd\n=======\nD\n>>>>>>> Ai change\ne\nf\n"
	const adjacentHunks = "a\n<<<<<<< old\nb\n=======\nB\n>>>>>>> Ai change\n<<<<<<< old\nc\n=======\nC\n>>>>>>> Ai change\nd\n"
	const sharedLines = "a\n<<<<<<< old\nb\nc\n=======\nb\nX\n>>>>>>> Ai change\nd\ne\n"

	cases := []struct {
		name string
		src  string
		// editStart/editEnd are offsets in the accepted view; derived from
		// the textual marker asked for below to keep the cases readable.
		oldSub string
		newSub string
		all    bool
	}{
		{"no pending replace line", "a\nb\nc\n", "b", "B", false},
		{"no pending insert at start", "a\nb\n", "", "x\n", false},
		{"no pending delete whole line", "a\nb\nc\n", "b\n", "", false},
		{"no pending midline replace", "abc\ndef\n", "b", "X", false},
		{"one pending hunk replace elsewhere", oneHunk, "d", "D", false},
		{"one pending hunk insert elsewhere", oneHunk, "", "x\n", false},
		{"one pending hunk delete elsewhere", oneHunk, "d\n", "", false},
		{"multiple pending hunks replace elsewhere", multiHunk, "f", "F", false},
		{"multiple pending hunks insert elsewhere", multiHunk, "", "x\n", false},
		{"multiple pending hunks delete elsewhere", multiHunk, "f\n", "", false},
		{"edit inside pending new half", oneHunk, "B", "X", false},
		{"edit replaces seed of pending block", multiHunk, "B", "X", false},
		{"adjacent pending hunks plus edit", adjacentHunks, "d", "D", false},
		{"pending hunk sharing lines plus edit", sharedLines, "e", "E", false},
		{"replace all occurrences", "foo\nbar\nfoo\nbaz\n", "foo", "qux", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			changes := GetDiffsBytes(src, "f.txt")
			accepted := AcceptAllChangesBytes(src, changes)

			var edits []Edit
			if tc.oldSub == "" {
				// Insertion at the start of the accepted view.
				edits = []Edit{{Start: 0, End: 0, New: []byte(tc.newSub)}}
			} else {
				edits = findByteEdits(accepted, []byte(tc.oldSub), []byte(tc.newSub), tc.all)
			}
			if len(edits) == 0 {
				t.Fatalf("test setup: %q not found in accepted %q", tc.oldSub, accepted)
			}

			got := MergeMarkerEdits(src, accepted, changes, edits)
			want := legacyMerge(src, changes, edits)
			if !bytes.Equal(got, want) {
				t.Errorf("merge mismatch:\ngot:  %q\nwant: %q\nsrc:  %q", got, want, src)
			}
		})
	}
}

// findByteEdits mirrors the write_file helper: non-overlapping occurrences of
// old, optionally all of them.
func findByteEdits(data, old, new []byte, all bool) []Edit {
	var edits []Edit
	for pos := 0; pos+len(old) <= len(data); {
		idx := bytes.Index(data[pos:], old)
		if idx < 0 {
			break
		}
		start := pos + idx
		edits = append(edits, Edit{Start: start, End: start + len(old), New: new})
		if !all {
			break
		}
		pos = start + len(old)
	}
	return edits
}

// TestMergeMarkerEditsNoPendingDoesNotNeedChanges keeps the no-pending path
// covered directly, including an edit that spans several lines.
func TestMergeMarkerEditsNoPendingDoesNotNeedChanges(t *testing.T) {
	src := []byte("one\ntwo\nthree\nfour\n")
	accepted := src
	edits := []Edit{{Start: 4, End: 14, New: []byte("TWO\nTHREE\n")}}
	got := MergeMarkerEdits(src, accepted, nil, edits)
	want := MergeMarkerBytes(src, applyEdits(accepted, edits))
	if !bytes.Equal(got, want) {
		t.Errorf("merge mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestMergeMarkerEditsInsertionPositions covers an insertion inside a line
// (marker must cover the whole line) and one between lines (marker stays at
// the boundary).
func TestMergeMarkerEditsInsertionPositions(t *testing.T) {
	src := []byte("alpha\nbeta\n")
	cases := []struct {
		name  string
		start int
		new   string
	}{
		{"midline", 2, "XY"},
		{"line boundary", 6, "XY\n"},
		{"start of file", 0, "XY\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edits := []Edit{{Start: tc.start, End: tc.start, New: []byte(tc.new)}}
			got := MergeMarkerEdits(src, src, nil, edits)
			want := MergeMarkerBytes(src, applyEdits(src, edits))
			if !bytes.Equal(got, want) {
				t.Errorf("merge mismatch:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

// TestMergeMarkerEditsNoOpKeepsContent guards against a no-op edit (or a
// marker block whose halves are identical) dropping the equal text.
func TestMergeMarkerEditsNoOpKeepsContent(t *testing.T) {
	src := []byte("a\nb\nc\n")
	got := MergeMarkerEdits(src, src, nil, []Edit{{Start: 2, End: 4, New: []byte("b\n")}})
	if !bytes.Equal(got, src) {
		t.Errorf("no-op edit changed content: got %q, want %q", got, src)
	}

	markerSrc := []byte("a\n<<<<<<< old\nb\n=======\nb\n>>>>>>> Ai change\nc\n")
	changes := GetDiffsBytes(markerSrc, "f.txt")
	accepted := AcceptAllChangesBytes(markerSrc, changes)
	got = MergeMarkerEdits(markerSrc, accepted, changes, nil)
	want := MergeMarkerBytes(RejectAllChangesBytes(markerSrc, changes), accepted)
	if !bytes.Equal(got, want) {
		t.Errorf("identical pending block mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestMergeMarkerEditsDuplicateReplacementMatchesFullDiff is the concrete
// regression case: replacing every "d" with "bar" in a file that already has
// "bar" lines lets a global diff align the duplicates differently from two
// fixed-offset hunks.
func TestMergeMarkerEditsDuplicateReplacementMatchesFullDiff(t *testing.T) {
	src := []byte("a\nbar\nd\nbar\nd\n")
	edits := findByteEdits(src, []byte("d"), []byte("bar"), true)
	if len(edits) != 2 {
		t.Fatalf("setup: found %d edits, want 2", len(edits))
	}

	got := MergeMarkerEdits(src, src, nil, edits)
	want := MergeMarkerBytes(src, bytes.ReplaceAll(src, []byte("d"), []byte("bar")))
	if !bytes.Equal(got, want) {
		t.Errorf("duplicate-line merge mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestMergeMarkerEditsNoTrailingNewlineFallsBack keeps the old behaviour for
// files whose final line has no newline (the rewrite can add one).
func TestMergeMarkerEditsNoTrailingNewlineFallsBack(t *testing.T) {
	src := []byte("a\nb")
	accepted := src
	edits := []Edit{{Start: 0, End: 1, New: []byte("A")}}
	got := MergeMarkerEdits(src, accepted, nil, edits)
	want := mergeMarkerEditsFallback(src, accepted, nil, edits)
	if !bytes.Equal(got, want) {
		t.Errorf("fallback mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
