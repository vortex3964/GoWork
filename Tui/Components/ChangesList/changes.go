package changeslist

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

//TODO: add a function the write edit create tools will use to write dif markers in files
// they will follow the format bellow:
//<<<<< old
// old code
//====
// new code
//>>>> Ai change
// they will put the file on a watch list and will return the position in the file of the change (fp maybe or maybe not)

//TODO: a function to reject , accept a change (will take file pointer pos to locate the start of the change)
//TODO: a function to reject all , accept all to clear the file for read tools and edit tools
//TODO: a function to detect the difs in the file and get their pos with regex usage

//this will be used to track the changes
//we will have a watcher for write events on the os level
//and on every write we will run the dif func on the file to scan
//for accepts rejects intenaly or externaly
type Change struct {
	Id    string
	Start int // offset of "<<<<< old"
	Mid   int // offset of "===="
	End   int // offset of ">>>> ..."
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
var hunkRe = regexp.MustCompile(`(?ms)^<{3,}[ \t]*old[ \t]*$\n.*?^(={3,}[ \t]*)$\n.*?^(>{3,}[ \t]*.*)$`)
 
// GetDiffs scans the file at path for diff hunks and returns a Change
// for each one found, in order of appearance, with the byte offset of
// each of the three markers (start, mid separator, end).
//NOTE: a lamformed marker means we will fail silently
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
