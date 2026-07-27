package changeslist

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

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

type WatchList struct {
	//will be used to avoid race conditions (later will consider it when the architecture is more complete)
	//mu sync.Mutex
	Filepath string
	Changes []Change
}

func InitWatchList(path string) *WatchList {
	return &WatchList{
		Filepath: path,
		Changes: []Change{},
	}
}

// hunkRe matches a whole diff hunk in one shot and captures the mid
// (separator) and end marker lines as groups 1 and 2. The start marker
// is the overall match start, so no separate group is needed for it.
var hunkRe = regexp.MustCompile(`(?ms)^<{3,}[ \t]*old[ \t]*$\n.*?^(={3,}[ \t]*)$\n.*?^(>{3,}[ \t]*[^\n]*)$`)
 
// GetDiffs scans the file at path for diff hunks and returns a Change
// for each one found, in order of appearance, with the byte offset of
// each of the three markers (start, mid separator, end).
//NOTE: a malformed marker means we will fail silently
func GetDiffs(path string) ([]Change, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}
	filename := filepath.Base(path)
 
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
	return changes, nil
}

func Accept_change(path string, start int, middle int, end int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

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

	os.WriteFile(path, result, 0644)
}

func Reject_change(path string, start int, middle int, end int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

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

	os.WriteFile(path, result, 0644)
}

func Accept_all_changes(path string, changes []Change) {
	for i := len(changes) - 1; i >= 0; i-- {
		Accept_change(path, changes[i].Start, changes[i].Mid, changes[i].End)
	}
}

func Reject_all_changes(path string, changes []Change) {
	for i := len(changes) - 1; i >= 0; i-- {
		Reject_change(path, changes[i].Start, changes[i].Mid, changes[i].End)
	}
}

func MergeMarkerFiles(filepath1 string, filepath2 string) {
	data1, err := os.ReadFile(filepath1)
	if err != nil {
		return
	}

	data2, err := os.ReadFile(filepath2)
	if err != nil {
		return
	}

	dmp := diffmatchpatch.New()
	text1 := string(data1)
	text2 := string(data2)

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

	err = os.WriteFile(filepath1, []byte(result.String()), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing %s: %v\n", filepath1, err)
		return
	}
}
