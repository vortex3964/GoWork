package changeslist

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

//TODO: add a function the write edit create tools will use to write dif markers in files
// they will follow the format bellow:
//<<<<<<< old
// old code
//=======
// new code
//>>>>>>> Ai change
// they will put the file on a watch list and will return the position in the file of the change (fp maybe or maybe not)

//this will be used to track the changes
//we will have a watcher for write events on the os level
//and on every write we will run the dif func on the file to scan
//for accepts rejects intenaly or externaly
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
	WatchedFiles map[string]struct{}//this is go's way of having a set
	Changeslist  map[string]ChangeList
	//tracks the ai
	aiThink   *bool
	Watcher   *fsnotify.Watcher
	WatchedDirs map[string]struct{}
}

func NewWatchList(aiThink *bool) (*WatchList , error) {
	w , err := fsnotify.NewWatcher()
	
	if err != nil {
		return nil , err
	}

	return &WatchList{
		WatchedFiles: make(map[string]struct{}),
		Changeslist:  make(map[string]ChangeList),
		aiThink:      aiThink,
		Watcher:      w,
		WatchedDirs:  make(map[string]struct{}),
	},nil
}

func (w *WatchList) SetThink(t *bool) {
	w.aiThink = t
}

// Add adds a file to the watch list.
func (w *WatchList) Add(path string) {
	w.WatchedFiles[path] = struct{}{}
}

// Has reports whether path is currently watched.
func (w *WatchList) Has(path string) bool {
	_, ok := w.WatchedFiles[path]
	return ok
}

// Remove stops watching path.
func (w *WatchList) Remove(path string) {
	delete(w.WatchedFiles, path)
}

// Files returns all currently watched file paths.
func (w *WatchList) Files() []string {
	files := make([]string, 0, len(w.WatchedFiles))
	for f := range w.WatchedFiles {
		files = append(files, f)
	}
	return files
}

func (w *WatchList) GetChanges(filepath string) []Change {
	return w.Changeslist[filepath].Changes
}

func (w *WatchList) addDirToWatcher(path string) {
	if w.Watcher == nil {
		return
	}
	dir := absPath(filepath.Dir(path))
	if _, ok := w.WatchedDirs[dir]; !ok {
		_ = w.Watcher.Add(dir)
		w.WatchedDirs[dir] = struct{}{}
	}
}

func (w *WatchList) hasWatchedFile(eventPath string) (string, bool) {
	absEvent := absPath(eventPath)
	for watched := range w.WatchedFiles {
		if absPath(watched) == absEvent {
			return watched, true
		}
	}
	return "", false
}

func (w *WatchList) removeWatchedDirs() {
	if w.Watcher == nil {
		return
	}
	for dir := range w.WatchedDirs {
		_ = w.Watcher.Remove(dir)
	}
	clear(w.WatchedDirs)
}

// hunkRe matches a whole diff hunk in one shot and captures the mid
// (separator) and end marker lines as groups 1 and 2. The start marker
// is the overall match start, so no separate group is needed for it.
var hunkRe = regexp.MustCompile(`(?ms)^<{3,}[ \t]*old[ \t]*$\n.*?^(={3,}[ \t]*)$\n.*?^(>{3,}[ \t]*[^\n]*)$`)
 
// GetDiffsBytes scans data for diff hunks and returns a Change for each one
// found, in order of appearance, with the byte offset of each of the three
// markers (start, mid separator, end). filename is only used to build
// Change.Id ("filename/Cn"). No disk I/O.
//NOTE: a malformed marker means we will fail silently
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
//WARN: a malformed marker means we will fail silently
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
