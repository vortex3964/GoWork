package changeslist

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/sergi/go-diff/diffmatchpatch"
)

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// this will be used to track the changes
// we will have a watcher for write events on the os level
// and on every write we will run the dif func on the file to scan
// for accepts rejects intenaly or externaly
type Change struct {
	Id    string
	Start int // offset of "<<<<<<< old"
	Mid   int // offset of "======="
	End   int // offset of ">>>>>> Ai change"
}

type ChangeList struct {
	//will be used to avoid race conditions (later will consider it when the architecture is more complete)
	//mu sync.Mutex
	Changes []Change
}

func InitChangeList(path string) *ChangeList {
	return &ChangeList{
		Changes: []Change{},
	}
}

type WatchList struct {
	// root points at the dispatcher's root string (see SetRoot). All paths
	// are normalized relative to *root (see key), so a single key space
	// serves both tool-provided relative paths and the absolute paths
	// fsnotify reports. Updating the dispatcher's string is enough.
	root *string
	// WatchedFiles is a set of canonical (clean, root-relative) paths.
	WatchedFiles map[string]struct{} //this is go's way of having a set
	Changeslist  map[string]ChangeList
	//tracks the ai
	aiThink     *bool
	Watcher     *fsnotify.Watcher
	WatchedDirs map[string]struct{}
	events      chan WatcherEventMsg
	// mu guards the maps (WatchedFiles, Changeslist, WatchedDirs)
	// and every fsnotify Add/Remove call. The tools mutate the maps from
	// their own goroutines (write/edit/create call Add) while the main loop
	// and the watcher handler read and write them, so without the lock this
	// is a data race. fsnotify.Watcher is not safe for concurrent calls, so
	// serializing the Add calls here matters too.
	mu sync.Mutex
}

func NewWatchList(aiThink *bool) (*WatchList, error) {
	w, err := fsnotify.NewWatcher()

	if err != nil {
		return nil, err
	}

	wl := &WatchList{
		WatchedFiles: make(map[string]struct{}),
		Changeslist:  make(map[string]ChangeList),
		aiThink:      aiThink,
		Watcher:      w,
		WatchedDirs:  make(map[string]struct{}),
		events:       make(chan WatcherEventMsg, 32),
	}
	go wl.pump()
	return wl, nil
}

// SetRoot points the watch list at the dispatcher's root string. The string
// itself is owned by the dispatcher, so a later update there is visible here.
func (w *WatchList) SetRoot(root *string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.root = root
}

// pump is the only reader of w.Watcher.Events for the program's lifetime.
// It forwards relevant events into the buffered events channel so no
// watcher events are dropped between command re-arms.
func (w *WatchList) pump() {
	for event := range w.Watcher.Events {
		if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
			continue
		}
		select {
		case w.events <- WatcherEventMsg{FilePath: event.Name}:
		default:
		}
	}
}

func (w *WatchList) SetThink(t *bool) {
	w.aiThink = t
}

// key canonicalizes a path into the single key space used by every map.
// Tool-provided paths are relative to the project root; fsnotify reports
// absolute paths. Normalizing both to a clean root-relative path (or the
// clean path itself if it can't be made relative) makes them agree.
func (w *WatchList) key(path string) string {
	if filepath.IsAbs(path) && w.root != nil && *w.root != "" {
		if rel, err := filepath.Rel(*w.root, path); err == nil {
			return filepath.Clean(rel)
		}
	}
	return filepath.Clean(path)
}

// Add adds a file to the watch list.
func (w *WatchList) Add(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addLocked(path)
}

func (w *WatchList) addLocked(path string) {
	w.WatchedFiles[w.key(path)] = struct{}{}
}

// Has reports whether path is currently watched.
func (w *WatchList) Has(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.WatchedFiles[w.key(path)]
	return ok
}

// Remove stops watching path and drops any changes stored for it.
func (w *WatchList) Remove(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removeLocked(path)
}

func (w *WatchList) removeLocked(path string) {
	k := w.key(path)
	delete(w.WatchedFiles, k)
	delete(w.Changeslist, k)
}

// Files returns all currently watched file paths (original form).
func (w *WatchList) Files() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.filesLocked()
}

func (w *WatchList) filesLocked() []string {
	files := make([]string, 0, len(w.WatchedFiles))
	for f := range w.WatchedFiles {
		files = append(files, f)
	}
	return files
}

func (w *WatchList) GetChanges(filepath string) []Change {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Changeslist[w.key(filepath)].Changes
}

func (w *WatchList) addDirToWatcher(path string) {
	if w.Watcher == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addDirToWatcherLocked(path)
}

func (w *WatchList) addDirToWatcherLocked(path string) {
	if w.Watcher == nil {
		return
	}
	dir := filepath.Dir(path)
	if w.root != nil && *w.root != "" {
		dir = filepath.Join(*w.root, dir)
	}
	if _, ok := w.WatchedDirs[dir]; !ok {
		_ = w.Watcher.Add(dir)
		w.WatchedDirs[dir] = struct{}{}
	}
}

func (w *WatchList) hasWatchedFile(eventPath string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := w.key(eventPath)
	_, ok := w.WatchedFiles[k]
	return k, ok
}

// hunkRe matches a whole diff hunk in one shot and captures the mid
// (separator) and end marker lines as groups 1 and 2. The start marker
// is the overall match start, so no separate group is needed for it.
var hunkRe = regexp.MustCompile(`(?ms)^<{3,}[ \t]*old[ \t]*$\n.*?^(={3,}[ \t]*)$\n.*?^(>{3,}[ \t]*[^\n]*)$`)

// GetDiffsBytes scans data for diff hunks and returns a Change for each one
// found, in order of appearance, with the byte offset of each of the three
// markers (start, mid separator, end). filename is only used to build
// Change.Id ("filename/Cn"). No disk I/O.
// NOTE: a malformed marker means we will fail silently
func GetDiffsBytes(data []byte, filename string) []Change {
	matches := hunkRe.FindAllSubmatchIndex(data, -1)
	changes := make([]Change, 0, len(matches))
	for i, m := range matches {
		// m[0], m[1] = whole match start/end
		// m[2], m[3] = group 1 (mid separator line) start/end
		// m[4], m[5] = group 2 (end marker line) start/end
		changes = append(changes, Change{
			Id:    fmt.Sprintf("%s/C%d", filename, i+1),
			Start: m[0],
			Mid:   m[2],
			End:   m[4],
		})
	}
	return changes
}

// GetDiffs scans the file at path for diff hunks and returns a Change
// for each one found, in order of appearance, with the byte offset of
// each of the three markers (start, mid separator, end).
// WARN: a malformed marker means we will fail silently
func GetDiffs(path string) ([]Change, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}
	return GetDiffsBytes(data, filepath.Base(path)), nil
}

// AcceptChangeBytes applies an "accept" edit (keep new code, drop old +
// markers) to an in-memory buffer and returns the resulting bytes.
// Same marker-scanning logic as Accept_change, but with no disk I/O.
func AcceptChangeBytes(data []byte, start, middle, end int) []byte {
	endOfEnd := end
	for endOfEnd < len(data) && data[endOfEnd] != '\n' {
		endOfEnd++
	}
	if endOfEnd < len(data) {
		endOfEnd++
	}

	startOfNew := middle
	for startOfNew < len(data) && data[startOfNew] != '\n' {
		startOfNew++
	}
	if startOfNew < len(data) {
		startOfNew++
	}

	newCode := data[startOfNew:end]

	result := make([]byte, 0, len(data)-(endOfEnd-start)+len(newCode))
	result = append(result, data[:start]...)
	result = append(result, newCode...)
	result = append(result, data[endOfEnd:]...)
	return result
}

// RejectChangeBytes applies a "reject" edit (keep old code, drop new +
// markers) to an in-memory buffer and returns the resulting bytes.
// Same marker-scanning logic as Reject_change, but with no disk I/O.
func RejectChangeBytes(data []byte, start, middle, end int) []byte {
	endOfStart := start
	for endOfStart < len(data) && data[endOfStart] != '\n' {
		endOfStart++
	}
	if endOfStart < len(data) {
		endOfStart++
	}

	endOfEnd := end
	for endOfEnd < len(data) && data[endOfEnd] != '\n' {
		endOfEnd++
	}
	if endOfEnd < len(data) {
		endOfEnd++
	}

	oldCode := data[endOfStart:middle]

	result := make([]byte, 0, len(data)-(endOfEnd-start)+len(oldCode))
	result = append(result, data[:start]...)
	result = append(result, oldCode...)
	result = append(result, data[endOfEnd:]...)
	return result
}

// ChangeSections extracts the old and new halves of a change hunk from data
// (the file bytes the offsets were computed against), dropping the marker
// lines and trailing newlines. Used to preview what an accept/reject keeps.
func ChangeSections(data []byte, c Change) (old, new string) {
	oldStart := c.Start
	for oldStart < len(data) && data[oldStart] != '\n' {
		oldStart++
	}
	if oldStart < len(data) {
		oldStart++
	}

	oldEnd := c.Mid
	for oldEnd > oldStart && (data[oldEnd-1] == '\n' || data[oldEnd-1] == '\r') {
		oldEnd--
	}

	newStart := c.Mid
	for newStart < len(data) && data[newStart] != '\n' {
		newStart++
	}
	if newStart < len(data) {
		newStart++
	}

	newEnd := c.End
	for newEnd > newStart && (data[newEnd-1] == '\n' || data[newEnd-1] == '\r') {
		newEnd--
	}

	return string(data[oldStart:oldEnd]), string(data[newStart:newEnd])
}

func Accept_change(path string, start int, middle int, end int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.WriteFile(path, AcceptChangeBytes(data, start, middle, end), 0644)
}

func Reject_change(path string, start int, middle int, end int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.WriteFile(path, RejectChangeBytes(data, start, middle, end), 0644)
}

// AcceptAllChangesBytes applies every change in a single left-to-right pass.
// For each marker block it keeps the new code (between ======= and >>>>>>>)
// and skips the old code and markers. Since we walk forward using the
// pre-computed offsets, subsequent markers aren't shifted by earlier edits.
func AcceptAllChangesBytes(data []byte, changes []Change) []byte {
	var b strings.Builder
	b.Grow(len(data))
	pos := 0
	for _, c := range changes {
		b.Write(data[pos:c.Start])

		// advance past =======\n to reach new code
		s := c.Mid
		for s < len(data) && data[s] != '\n' {
			s++
		}
		if s < len(data) {
			s++
		}
		b.Write(data[s:c.End])

		// advance past >>>>>>>\n
		pos = c.End
		for pos < len(data) && data[pos] != '\n' {
			pos++
		}
		if pos < len(data) {
			pos++
		}
	}
	b.Write(data[pos:])
	return []byte(b.String())
}

// RejectAllChangesBytes applies every change in a single left-to-right pass.
// For each marker block it keeps the old code (between <<<<<<< and =======)
// and skips the new code and markers.
func RejectAllChangesBytes(data []byte, changes []Change) []byte {
	var b strings.Builder
	b.Grow(len(data))
	pos := 0
	for _, c := range changes {
		b.Write(data[pos:c.Start])

		// advance past <<<<<<< old\n to reach old code
		s := c.Start
		for s < len(data) && data[s] != '\n' {
			s++
		}
		if s < len(data) {
			s++
		}
		b.Write(data[s:c.Mid])

		// advance past >>>>>>>\n
		pos = c.End
		for pos < len(data) && data[pos] != '\n' {
			pos++
		}
		if pos < len(data) {
			pos++
		}
	}
	b.Write(data[pos:])
	return []byte(b.String())
}

func Accept_all_changes(path string, changes []Change) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.WriteFile(path, AcceptAllChangesBytes(data, changes), 0644)
}

func Reject_all_changes(path string, changes []Change) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.WriteFile(path, RejectAllChangesBytes(data, changes), 0644)
}

// MergeMarkerBytes diffs original against modified (line-by-line) and
// returns the merged text with <<<<<<< old / ======= / >>>>>>> AI change
// markers inserted around every changed hunk. No disk I/O.
func MergeMarkerBytes(original []byte, modified []byte) []byte {
	dmp := diffmatchpatch.New()
	text1 := string(original)
	text2 := string(modified)

	chars1, chars2, lineArray := dmp.DiffLinesToChars(text1, text2)
	diffs := dmp.DiffMain(chars1, chars2, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	var result strings.Builder
	var oldBuf, newBuf strings.Builder

	flush := func() {
		if oldBuf.Len() == 0 && newBuf.Len() == 0 {
			return
		}
		result.WriteString("<<<<<<< old\n")
		result.WriteString(oldBuf.String())
		result.WriteString("=======\n")
		result.WriteString(newBuf.String())
		result.WriteString(">>>>>>> Ai change\n")
		oldBuf.Reset()
		newBuf.Reset()
	}

	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			result.WriteString(d.Text)
		case diffmatchpatch.DiffDelete:
			oldBuf.WriteString(d.Text)
		case diffmatchpatch.DiffInsert:
			newBuf.WriteString(d.Text)
		}
	}
	flush()

	return []byte(result.String())
}

// Edit is a replacement of accepted[Start:End] with New. Start and End are
// byte offsets into the accepted view of a file (the on-disk bytes with every
// pending change applied). New is the replacement text.
type Edit struct {
	Start int
	End   int
	New   []byte
}

// InsertMergeMarkerHunk inserts exactly one merge-marker block into base,
// replacing base[start:end] with newText. It uses the caller's known byte
// offsets and never diffs base. Offsets must describe base.
func InsertMergeMarkerHunk(base []byte, start, end int, newText []byte) []byte {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(base) {
		end = len(base)
	}

	const startMarker = "<<<<<<< old\n"
	const midMarker = "=======\n"
	const endMarker = ">>>>>>> Ai change\n"

	out := make([]byte, 0, len(base)-(end-start)+len(newText)+len(startMarker)+len(midMarker)+len(endMarker))
	out = append(out, base[:start]...)
	out = append(out, startMarker...)
	out = append(out, base[start:end]...)
	out = append(out, midMarker...)
	out = append(out, newText...)
	out = append(out, endMarker...)
	out = append(out, base[end:]...)
	return out
}

// mergeUnit is one segment of the on-disk file in the order it appears:
// either untouched text shared by the accepted/rejected views, or a whole
// pending marker block. Accepted offsets let edits be placed without scanning
// the file again.
type mergeUnit struct {
	pending bool

	srcStart, srcEnd int // range in the on-disk buffer (marker block for pending)
	accStart, accEnd int // corresponding range in the accepted view

	oldStart, oldEnd int // pending: old half between <<<<<<< and =======
	newStart, newEnd int // pending: new half between ======= and >>>>>>>

	edit int // index into the expanded edit list, -1 when untouched
}

// afterLine returns the offset just past the newline that ends the line at
// pos (or len(data) when there is no newline).
func afterLine(data []byte, pos int) int {
	for pos < len(data) && data[pos] != '\n' {
		pos++
	}
	if pos < len(data) {
		pos++
	}
	return pos
}

// buildMergeUnits splits src into equal and pending-marker units using the
// known change offsets. It returns false when the offsets are malformed or
// out of order, in which case callers fall back to the full-file diff.
func buildMergeUnits(src []byte, changes []Change) ([]mergeUnit, bool) {
	units := make([]mergeUnit, 0, 2*len(changes)+1)
	srcPos, accPos := 0, 0

	for _, c := range changes {
		if c.Start < srcPos || c.Mid < c.Start || c.End < c.Mid || c.End > len(src) {
			return nil, false
		}
		if c.Start > srcPos {
			n := c.Start - srcPos
			units = append(units, mergeUnit{
				srcStart: srcPos, srcEnd: c.Start,
				accStart: accPos, accEnd: accPos + n,
				edit: -1,
			})
			accPos += n
		}

		oldStart := afterLine(src, c.Start)
		newStart := afterLine(src, c.Mid)
		blockEnd := afterLine(src, c.End)
		if oldStart > c.Mid || newStart > c.End || blockEnd < c.End {
			return nil, false
		}

		units = append(units, mergeUnit{
			pending:  true,
			srcStart: c.Start, srcEnd: blockEnd,
			accStart: accPos, accEnd: accPos + (c.End - newStart),
			oldStart: oldStart, oldEnd: c.Mid,
			newStart: newStart, newEnd: c.End,
			edit: -1,
		})
		accPos += c.End - newStart
		srcPos = blockEnd
	}

	if srcPos < len(src) {
		n := len(src) - srcPos
		units = append(units, mergeUnit{
			srcStart: srcPos, srcEnd: len(src),
			accStart: accPos, accEnd: accPos + n,
			edit: -1,
		})
	}

	return units, true
}

// mergeExpanded is one or more caller edits after being widened to cover any
// pending marker block they intersected. Widening keeps a new edit from
// cutting a pending hunk in half: the whole hunk is replaced by the new one.
type mergeExpanded struct {
	accStart, accEnd int
	members          []int // indices into the caller's edits, in order
	oldText          []byte
	newText          []byte
}

// expandEdits widens edits around pending blocks and expands their new text.
// It returns false when the edit list is malformed.
func expandEdits(src []byte, units []mergeUnit, edits []Edit, accepted []byte) ([]mergeExpanded, bool) {
	expanded := make([]mergeExpanded, 0, len(edits))
	prevEnd := 0
	for i, e := range edits {
		if e.Start < 0 || e.Start > e.End || e.End > len(accepted) || e.Start < prevEnd {
			return nil, false
		}
		prevEnd = e.End

		start, end := e.Start, e.End

		// Marker hunks are line-oriented: a match in the middle of a line
		// shows the whole old/new line. An insertion between lines stays at
		// its exact point; one inside a line widens to that line too.
		if start < end {
			if start > 0 && accepted[start-1] != '\n' {
				start = bytes.LastIndexByte(accepted[:start], '\n') + 1
			}
			if end > 0 && end < len(accepted) && accepted[end-1] != '\n' {
				if nl := bytes.IndexByte(accepted[end:], '\n'); nl >= 0 {
					end += nl + 1
				} else {
					end = len(accepted)
				}
			}
		} else if start > 0 && start < len(accepted) && accepted[start-1] != '\n' {
			lineStart := bytes.LastIndexByte(accepted[:start], '\n') + 1
			start = lineStart
			if nl := bytes.IndexByte(accepted[lineStart:], '\n'); nl >= 0 {
				end = lineStart + nl + 1
			} else {
				end = len(accepted)
			}
		}

		for _, u := range units {
			if !u.pending {
				continue
			}
			overlaps := start < u.accEnd && end > u.accStart
			// A pure insertion strictly inside a pending new half still has
			// to widen, otherwise it would split the existing marker block.
			insertsInside := start == end && u.accStart < start && start < u.accEnd
			if overlaps || insertsInside {
				if u.accStart < start {
					start = u.accStart
				}
				if u.accEnd > end {
					end = u.accEnd
				}
			}
		}

		expanded = append(expanded, mergeExpanded{
			accStart: start,
			accEnd:   end,
			members:  []int{i},
		})
	}

	sort.SliceStable(expanded, func(i, j int) bool {
		return expanded[i].accStart < expanded[j].accStart
	})

	// Widening can make two edits overlap (e.g. two replacements inside the
	// same pending hunk). Fold them into one hunk so offsets stay valid.
	merged := expanded[:0]
	for _, e := range expanded {
		if n := len(merged); n > 0 && e.accStart < merged[n-1].accEnd {
			merged[n-1].accEnd = max(merged[n-1].accEnd, e.accEnd)
			merged[n-1].members = append(merged[n-1].members, e.members...)
			continue
		}
		merged = append(merged, e)
	}
	expanded = merged

	for i := range expanded {
		e := &expanded[i]

		// New text is the accepted span with the caller's replacements
		// applied, in order.
		sort.Ints(e.members)
		pos := e.accStart
		for _, m := range e.members {
			me := edits[m]
			if me.Start < pos || me.End > e.accEnd {
				return nil, false
			}
			e.newText = append(e.newText, accepted[pos:me.Start]...)
			e.newText = append(e.newText, me.New...)
			pos = me.End
		}
		e.newText = append(e.newText, accepted[pos:e.accEnd]...)

		// A replacement that does not end in a newline joins the following
		// line in the rewritten file. The old line-based diff treats that
		// whole joined line as changed, so the marker hunk must cover it too.
		if len(e.newText) > 0 && e.newText[len(e.newText)-1] != '\n' && e.accEnd < len(accepted) {
			next := afterLine(accepted, e.accEnd)
			// Land on a whole pending block rather than splitting one.
			for _, u := range units {
				if u.pending && next > u.accStart && next < u.accEnd {
					next = u.accEnd
					break
				}
			}
			e.newText = append(e.newText, accepted[e.accEnd:next]...)
			e.accEnd = next
		}

		// Old text is the rejected view of the covered span: untouched bytes
		// plus the old half of every pending block inside it.
		for _, u := range units {
			if u.accEnd <= e.accStart || u.accStart >= e.accEnd {
				continue
			}
			if u.pending {
				e.oldText = append(e.oldText, src[u.oldStart:u.oldEnd]...)
				continue
			}
			lo := max(e.accStart, u.accStart)
			hi := min(e.accEnd, u.accEnd)
			from := u.srcStart + (lo - u.accStart)
			to := u.srcStart + (hi - u.accStart)
			e.oldText = append(e.oldText, src[from:to]...)
		}
	}

	return expanded, true
}

// addLineTokens adds every whole line in data to set, matching the tokens
// diffmatchpatch's line diff uses (each line includes its newline).
func addLineTokens(set map[string]struct{}, data []byte) {
	for start := 0; start < len(data); {
		idx := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if idx >= 0 {
			end = start + idx + 1
		}
		set[string(data[start:end])] = struct{}{}
		start = end
	}
}

// countChangedLines increments counts for every line in set that appears in
// data and reports whether one of them has now been seen twice.
func countChangedLines(data []byte, set map[string]struct{}, counts map[string]int) bool {
	if len(set) == 0 {
		return false
	}
	for start := 0; start < len(data); {
		idx := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if idx >= 0 {
			end = start + idx + 1
		}
		line := string(data[start:end])
		if _, changed := set[line]; changed {
			counts[line]++
			if counts[line] > 1 {
				return true
			}
		}
		start = end
	}
	return false
}

// unitAbsorbedByEdits reports whether an expanded edit already covers the
// pending unit, in which case the unit's content is part of that edit hunk.
func unitAbsorbedByEdits(u mergeUnit, expanded []mergeExpanded) bool {
	for _, e := range expanded {
		if u.accStart >= e.accStart && u.accEnd <= e.accEnd {
			return true
		}
	}
	return false
}

// ambiguousLineAlignment reports whether a global line diff could align
// repeated lines differently from the targeted per-hunk insertion.
//
// The fast path places each hunk at its known offset. A global diff is free
// to match a changed line against an identical line elsewhere in the file
// instead, shifting the hunk boundary (for example replacing every "d" with
// "bar" in a file that already contains "bar"). Whenever a line inside a
// changed hunk also occurs anywhere across the old and new views, defer to
// the legacy whole-file diff so the output remains byte-identical.
func ambiguousLineAlignment(src, accepted []byte, changes []Change, units []mergeUnit, expanded []mergeExpanded, edits []Edit) bool {
	changedLines := make(map[string]struct{})

	for _, e := range expanded {
		if bytes.Equal(e.oldText, e.newText) {
			continue // no marker block for this span
		}
		addLineTokens(changedLines, e.oldText)
		addLineTokens(changedLines, e.newText)
	}
	for _, u := range units {
		if !u.pending || unitAbsorbedByEdits(u, expanded) {
			continue
		}
		old := src[u.oldStart:u.oldEnd]
		new := src[u.newStart:u.newEnd]
		if bytes.Equal(old, new) {
			continue
		}
		addLineTokens(changedLines, old)
		addLineTokens(changedLines, new)
	}

	if len(changedLines) == 0 {
		return false
	}

	// A changed line is unambiguous only if it occurs exactly once across
	// both views. If it occurs again — as a pending old/new half, in another
	// edit's replacement, or in untouched text — Myers is free to pair the
	// global occurrences differently than the per-hunk path does.
	rejected := RejectAllChangesBytes(src, changes)
	updated := applyEdits(accepted, edits)

	counts := make(map[string]int)
	if countChangedLines(rejected, changedLines, counts) {
		return true
	}
	return countChangedLines(updated, changedLines, counts)
}

// MergeMarkerEdits returns src with marker blocks for every pending change
// plus fresh marker blocks for edits. It works from the known marker offsets
// and edit byte ranges, so the file is never diffed as a whole.
//
// accepted must be src with all pending changes accepted, and each Edit's
// offsets refer to accepted. Existing pending changes are preserved by
// re-emitting their old/new halves; an edit that intersects a pending block
// absorbs that whole block, mirroring the previous full-file diff.
//
// When a changed line also occurs elsewhere in either view, a global line
// diff is free to pair the duplicate occurrences differently than fixed
// per-hunk offsets would. Those cases (and malformed offsets) fall back to
// the legacy whole-file diff so the output stays byte-identical.
func MergeMarkerEdits(src, accepted []byte, changes []Change, edits []Edit) []byte {
	// The line-oriented fast path assumes newline-terminated text. A file
	// without a final newline can gain one when it is rewritten; fall back to
	// the old diff for that rare case so behaviour stays identical.
	if (len(src) > 0 && src[len(src)-1] != '\n') || (len(accepted) > 0 && accepted[len(accepted)-1] != '\n') {
		return mergeMarkerEditsFallback(src, accepted, changes, edits)
	}

	units, ok := buildMergeUnits(src, changes)
	if !ok {
		return mergeMarkerEditsFallback(src, accepted, changes, edits)
	}
	accLen := 0
	if n := len(units); n > 0 {
		accLen = units[n-1].accEnd
	}
	if accLen != len(accepted) {
		return mergeMarkerEditsFallback(src, accepted, changes, edits)
	}

	expanded, ok := expandEdits(src, units, edits, accepted)
	if !ok {
		return mergeMarkerEditsFallback(src, accepted, changes, edits)
	}
	// A replacement without a trailing newline can pull the following line
	// into its hunk. If that now reaches into another edit's span, the fixed
	// per-hunk layout is no longer valid.
	for i := 1; i < len(expanded); i++ {
		if expanded[i].accStart < expanded[i-1].accEnd {
			return mergeMarkerEditsFallback(src, accepted, changes, edits)
		}
	}
	if ambiguousLineAlignment(src, accepted, changes, units, expanded, edits) {
		return mergeMarkerEditsFallback(src, accepted, changes, edits)
	}

	// Split untouched units at every edit boundary so each unit belongs
	// wholly to one edit or to no edit.
	type splitPoints map[int]struct{}
	splits := make(splitPoints)
	for _, e := range expanded {
		splits[e.accStart] = struct{}{}
		splits[e.accEnd] = struct{}{}
	}

	ordered := make([]mergeUnit, 0, len(units)+2*len(expanded))
	zeroAt := make(map[int][]int)
	for i, e := range expanded {
		if e.accStart == e.accEnd {
			zeroAt[e.accStart] = append(zeroAt[e.accStart], i)
		}
	}

	appendZeroes := func(at int) {
		idxs := zeroAt[at]
		if len(idxs) == 0 {
			return
		}
		sort.Ints(idxs)
		for _, i := range idxs {
			ordered = append(ordered, mergeUnit{
				accStart: at, accEnd: at, edit: i,
			})
		}
		delete(zeroAt, at)
	}

	for _, u := range units {
		if !u.pending {
			points := make([]int, 0, 2)
			for p := range splits {
				if p > u.accStart && p < u.accEnd {
					points = append(points, p)
				}
			}
			if len(points) == 0 {
				appendZeroes(u.accStart)
				ordered = append(ordered, u)
				continue
			}
			sort.Ints(points)
			curAcc, curSrc := u.accStart, u.srcStart
			for _, p := range points {
				if p <= curAcc {
					continue
				}
				appendZeroes(curAcc)
				ordered = append(ordered, mergeUnit{
					srcStart: curSrc, srcEnd: curSrc + (p - curAcc),
					accStart: curAcc, accEnd: p,
					edit: -1,
				})
				curSrc += p - curAcc
				curAcc = p
			}
			appendZeroes(curAcc)
			ordered = append(ordered, mergeUnit{
				srcStart: curSrc, srcEnd: u.srcEnd,
				accStart: curAcc, accEnd: u.accEnd,
				edit: -1,
			})
			continue
		}

		appendZeroes(u.accStart)
		ordered = append(ordered, u)
	}
	appendZeroes(len(accepted))
	if len(zeroAt) != 0 {
		// Every edit boundary should have been visited; if not, fall back
		// rather than emit hunks out of order.
		return mergeMarkerEditsFallback(src, accepted, changes, edits)
	}

	// Assign each non-insertion unit to the edit covering it.
	for i := range ordered {
		u := &ordered[i]
		if u.edit >= 0 {
			continue
		}
		for ei := range expanded {
			e := &expanded[ei]
			if u.accStart >= e.accStart && u.accEnd <= e.accEnd {
				u.edit = ei
				break
			}
		}
	}

	// Emit equal text and raw hunk pieces in order. Adjacent hunk pieces are
	// coalesced before diffing so two edits with no unchanged line between
	// them still produce a single marker block, just like the old full-file
	// diff did.
	type mergePiece struct {
		equal   []byte
		oldText []byte
		newText []byte
		hunk    bool
	}
	pieces := make([]mergePiece, 0, len(ordered)+len(expanded))
	emitted := make([]bool, len(expanded))

	for _, u := range ordered {
		if u.edit >= 0 {
			if !emitted[u.edit] {
				emitted[u.edit] = true
				pieces = append(pieces, mergePiece{
					oldText: expanded[u.edit].oldText,
					newText: expanded[u.edit].newText,
					hunk:    true,
				})
			}
			continue
		}
		if u.pending {
			pieces = append(pieces, mergePiece{
				oldText: src[u.oldStart:u.oldEnd],
				newText: src[u.newStart:u.newEnd],
				hunk:    true,
			})
			continue
		}
		if u.srcEnd > u.srcStart {
			pieces = append(pieces, mergePiece{equal: src[u.srcStart:u.srcEnd]})
		}
	}
	for i, e := range expanded {
		if !emitted[i] {
			pieces = append(pieces, mergePiece{oldText: e.oldText, newText: e.newText, hunk: true})
		}
	}

	var out strings.Builder
	out.Grow(len(src))
	var hunkOld, hunkNew []byte
	inHunk := false
	flush := func() {
		if !inHunk {
			return
		}
		out.Write(MergeMarkerBytes(hunkOld, hunkNew))
		hunkOld = hunkOld[:0]
		hunkNew = hunkNew[:0]
		inHunk = false
	}

	for _, p := range pieces {
		if !p.hunk {
			if len(p.equal) == 0 {
				continue
			}
			flush()
			out.Write(p.equal)
			continue
		}
		if len(p.oldText) == 0 && len(p.newText) == 0 {
			continue
		}
		if bytes.Equal(p.oldText, p.newText) {
			// Nothing changed in this span; the old diff kept it as equal
			// text (relevant for no-op edits and malformed marker blocks).
			if len(p.oldText) > 0 {
				flush()
				out.Write(p.oldText)
			}
			continue
		}
		hunkOld = append(hunkOld, p.oldText...)
		hunkNew = append(hunkNew, p.newText...)
		inHunk = true
	}
	flush()

	return []byte(out.String())
}

// mergeMarkerEditsFallback is the old whole-file implementation, used for
// malformed offsets, text that does not end in a newline, and changed lines
// whose duplicates make global alignment ambiguous.
func mergeMarkerEditsFallback(src, accepted []byte, changes []Change, edits []Edit) []byte {
	rejected := RejectAllChangesBytes(src, changes)
	updated := applyEdits(accepted, edits)
	return MergeMarkerBytes(rejected, updated)
}

// applyEdits applies non-overlapping edits to data in order.
func applyEdits(data []byte, edits []Edit) []byte {
	if len(edits) == 0 {
		return data
	}
	ordered := append([]Edit(nil), edits...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })

	var out bytes.Buffer
	out.Grow(len(data))
	pos := 0
	for _, e := range ordered {
		if e.Start < pos || e.Start > e.End || e.End > len(data) {
			return data
		}
		out.Write(data[pos:e.Start])
		out.Write(e.New)
		pos = e.End
	}
	out.Write(data[pos:])
	return out.Bytes()
}

// MergeMarkerFiles reads filepath1 (original) and filepath2 (modified),
// merges them with MergeMarkerBytes, and writes the marked-up result back
// to filepath1.
func MergeMarkerFiles(filepath1 string, filepath2 string) {
	data1, err := os.ReadFile(filepath1)
	if err != nil {
		return
	}

	data2, err := os.ReadFile(filepath2)
	if err != nil {
		return
	}

	merged := MergeMarkerBytes(data1, data2)

	if err := os.WriteFile(filepath1, merged, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "writing %s: %v\n", filepath1, err)
		return
	}
}
