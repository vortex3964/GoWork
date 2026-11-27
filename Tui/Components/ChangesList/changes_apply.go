package changeslist

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "charm.land/bubbletea/v2"
)

// errFileChanged is returned when a file keeps changing while it is being
// read, so no stable set of change offsets can be computed.
var errFileChanged = errors.New("file changed while being read")

// isNotExist reports whether err means the path is gone (as opposed to a
// transient stat/read failure, which must keep the watched entry).
func isNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func (s fileStamp) known() bool {
	return !s.modTime.IsZero()
}

// readStableFile reads path and returns a stamp that describes exactly the
// returned bytes. Offsets computed against that stamp stay valid for data.
func readStableFile(path string) ([]byte, fileStamp, error) {
	for attempt := 0; attempt < 3; attempt++ {
		before, err := statFileStamp(path)
		if err != nil {
			return nil, fileStamp{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fileStamp{}, err
		}
		after, err := statFileStamp(path)
		if err != nil {
			return nil, fileStamp{}, err
		}
		if before == after {
			return data, after, nil
		}
	}
	return nil, fileStamp{}, errFileChanged
}

// scanFileDiffs reads path and returns its changes together with the stamp
// the offsets were computed against.
func scanFileDiffs(path string) ([]Change, fileStamp, error) {
	data, stamp, err := readStableFile(path)
	if err != nil {
		return nil, fileStamp{}, err
	}
	return GetDiffsBytes(data, filepath.Base(path)), stamp, nil
}

func (m *Model) changeList(key string) (ChangeList, bool) {
	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	cl, ok := m.Watch.Changeslist[key]
	return cl, ok
}

// storeScanned replaces key's change list with a scan of data. A file with
// no hunks left stops being watched; the watch entry is never resurrected if
// it was removed while scanning.
func (m *Model) storeScanned(key string, data []byte, stamp fileStamp) {
	difs := GetDiffsBytes(data, filepath.Base(key))

	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	if _, ok := m.Watch.WatchedFiles[key]; !ok {
		return
	}
	if m.fileStamps == nil {
		m.fileStamps = make(map[string]fileStamp)
	}
	if len(difs) == 0 {
		m.Watch.removeLocked(key)
		delete(m.fileStamps, key)
		return
	}
	m.Watch.Changeslist[key] = ChangeList{Changes: difs, Stamp: stamp}
	m.Watch.addDirToWatcherLocked(key)
	m.fileStamps[key] = stamp
}

func (m *Model) dropWatched(key string) {
	m.Watch.mu.Lock()
	defer m.Watch.mu.Unlock()
	m.Watch.removeLocked(key)
	delete(m.fileStamps, key)
}

// findChangeIndex locates the cached change that corresponds to want. The Id
// is stable across re-scans; offsets are the fallback for hand-built lists.
func findChangeIndex(changes []Change, want Change) int {
	for i := range changes {
		if changes[i].Id == want.Id {
			return i
		}
	}
	for i := range changes {
		if changes[i].Start == want.Start && changes[i].Mid == want.Mid && changes[i].End == want.End {
			return i
		}
	}
	return -1
}

type applyResult struct {
	applied bool
	stale   bool
	err     error
	kept    []string
	summary string
}

// applyChange applies selected (one hunk) or, when selected is nil, every
// hunk in key's file. The file is read through the watch root, checked
// against the stamp the offsets were computed from, and only written when
// the offsets and marker text still match the bytes on disk. A mismatch
// re-scans the file, updates the change list and reports stale so the caller
// can tell the user instead of writing or panicking.
func (m *Model) applyChange(key string, selected *Change, reject bool) applyResult {
	if m.Watch == nil {
		return applyResult{}
	}
	cl, ok := m.changeList(key)
	if !ok || len(cl.Changes) == 0 {
		return applyResult{}
	}

	path := m.watchedPath(key)
	data, stamp, err := readStableFile(path)
	if err != nil {
		if isNotExist(err) {
			m.dropWatched(key)
			return applyResult{summary: key + ": file no longer exists; removed from changes list"}
		}
		return applyResult{err: err, summary: key + ": " + err.Error()}
	}

	stale := func() applyResult {
		m.storeScanned(key, data, stamp)
		return applyResult{
			stale:   true,
			summary: key + ": changed on disk — changes list refreshed; nothing applied, review and retry",
		}
	}

	if cl.Stamp.known() && cl.Stamp != stamp {
		return stale()
	}

	idx := -1
	if selected != nil {
		idx = findChangeIndex(cl.Changes, *selected)
		if idx < 0 || !changeValid(data, cl.Changes[idx]) {
			return stale()
		}
	} else if !changesValid(data, cl.Changes) {
		return stale()
	}

	var kept []string
	if idx >= 0 {
		old, new := ChangeSections(data, cl.Changes[idx])
		if reject {
			kept = splitContentLines(old)
		} else {
			kept = splitContentLines(new)
		}
	} else {
		for _, c := range cl.Changes {
			old, new := ChangeSections(data, c)
			if reject {
				kept = append(kept, splitContentLines(old)...)
			} else {
				kept = append(kept, splitContentLines(new)...)
			}
		}
	}

	var out []byte
	switch {
	case idx >= 0 && reject:
		c := cl.Changes[idx]
		out = RejectChangeBytes(data, c.Start, c.Mid, c.End)
	case idx >= 0:
		c := cl.Changes[idx]
		out = AcceptChangeBytes(data, c.Start, c.Mid, c.End)
	case reject:
		out = RejectAllChangesBytes(data, cl.Changes)
	default:
		out = AcceptAllChangesBytes(data, cl.Changes)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return applyResult{err: err, summary: key + ": " + err.Error()}
	}

	// Re-scan the bytes now on disk so the remaining offsets are guaranteed
	// to describe what the next action reads, even if another writer raced
	// us right after the write. If the re-read fails transiently the old
	// stamp is kept, which makes the next action re-scan before applying.
	if afterData, afterStamp, readErr := readStableFile(path); readErr == nil {
		m.storeScanned(key, afterData, afterStamp)
	} else if isNotExist(readErr) {
		m.dropWatched(key)
	} else {
		m.storeScanned(key, out, stamp)
	}

	if idx >= 0 {
		return applyResult{
			applied: true,
			kept:    kept,
			summary: key + "/" + shortChangeID(cl.Changes[idx].Id),
		}
	}
	return applyResult{
		applied: true,
		kept:    kept,
		summary: key + " (" + changeCountLabel(len(cl.Changes)) + ")",
	}
}

func (m *Model) applySelected(reject bool) tea.Cmd {
	if len(m.filtered) == 0 || m.Watch == nil {
		return nil
	}
	sel := m.filtered[m.cursor]

	var selected *Change
	if !sel.isHeader {
		c := sel.change
		selected = &c
	}

	res := m.applyChange(sel.filePath, selected, reject)
	m.explorer.Invalidate()
	m.rebuildRows()
	if res.summary == "" {
		return nil
	}
	return m.eventCmd(reject, res.summary, res.kept)
}

func (m *Model) applyAll(reject bool) tea.Cmd {
	if m.Watch == nil {
		return nil
	}

	type entry struct {
		key   string
		count int
	}
	m.Watch.mu.Lock()
	entries := make([]entry, 0, len(m.Watch.Changeslist))
	for key, cl := range m.Watch.Changeslist {
		if len(cl.Changes) > 0 {
			entries = append(entries, entry{key: key, count: len(cl.Changes)})
		}
	}
	m.Watch.mu.Unlock()

	// Deterministic order keeps summaries stable and avoids surprises when
	// several files are touched in one action.
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	appliedFiles, appliedChanges, skipped := 0, 0, 0
	for _, e := range entries {
		res := m.applyChange(e.key, nil, reject)
		switch {
		case res.applied:
			appliedFiles++
			appliedChanges += e.count
		case res.summary != "":
			skipped++
		}
	}

	m.explorer.Invalidate()
	m.rebuildRows()
	if appliedChanges == 0 && skipped == 0 {
		return nil
	}

	summary := fmt.Sprintf("%s across %d files", changeCountLabel(appliedChanges), appliedFiles)
	if skipped > 0 {
		summary += fmt.Sprintf("; skipped %d file(s) that changed on disk or could not be read", skipped)
	}
	return m.eventCmd(reject, summary, nil)
}
