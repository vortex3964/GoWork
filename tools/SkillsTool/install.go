package skillstool

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Provenance is the content of the sidecar written next to an installed
// SKILL.md. Source and Ref are sanitized (no credentials, query strings or
// fragments) and SHA256 hashes the exact bytes that were installed.
type Provenance struct {
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	SHA256 string `json:"sha256"`
}

// ParseSkillContent validates an in-memory SKILL.md and returns its metadata.
// The file must be UTF-8 without NUL bytes, at most MaxSkillBytes, and carry
// a closed frontmatter block with valid name and non-empty description.
func ParseSkillContent(content string) (Skill, error) {
	if content == "" {
		return Skill{}, fmt.Errorf("skill content is empty")
	}
	if len(content) > MaxSkillBytes {
		return Skill{}, fmt.Errorf("skill content is %d bytes; the limit is %d bytes", len(content), MaxSkillBytes)
	}
	if !utf8.ValidString(content) {
		return Skill{}, fmt.Errorf("skill content is not valid UTF-8")
	}
	if strings.IndexByte(content, 0) >= 0 {
		return Skill{}, fmt.Errorf("skill content contains a NUL byte")
	}

	fm, ok := parseFrontmatter(bufio.NewReader(strings.NewReader(content)), true)
	if !ok {
		return Skill{}, fmt.Errorf("skill frontmatter must start with --- and end with a closing --- line")
	}
	if !fm.hasName || strings.TrimSpace(fm.name) == "" {
		return Skill{}, fmt.Errorf("skill frontmatter is missing a name")
	}
	if !NameValid(fm.name) {
		return Skill{}, fmt.Errorf("skill frontmatter name %q is invalid", fm.name)
	}
	if !fm.hasDescription || strings.TrimSpace(fm.description) == "" {
		return Skill{}, fmt.Errorf("skill frontmatter is missing a description")
	}
	return Skill{Name: fm.name, Description: fm.description}, nil
}

// ValidateInstall runs every check Install performs without touching disk
// (apart from the overwrite conflict check). It is what dry-run uses.
func (m *Manager) ValidateInstall(name, content string, overwrite bool) (Skill, error) {
	if !NameValid(name) {
		return Skill{}, fmt.Errorf("invalid skill name %q: use lowercase letters, digits and hyphens only", name)
	}
	parsed, err := ParseSkillContent(content)
	if err != nil {
		return Skill{}, err
	}
	if parsed.Name != name {
		return Skill{}, fmt.Errorf("frontmatter name %q does not match skill directory %q", parsed.Name, name)
	}
	path := filepath.Join(m.skillsDir(), name, SkillFileName)
	if !overwrite && m.Exists(name) {
		return Skill{}, fmt.Errorf("%w: %s", ErrExists, path)
	}
	parsed.Path = path
	return parsed, nil
}

// Install writes a complete SKILL.md under the manager's skills directory.
// The write is atomic (temp file + rename), fsynced, and refuses symlinked
// skill directories or target files. Unless overwrite is true an existing
// skill is never replaced. A provenance sidecar with the content hash is
// written alongside the skill.
//
// Production managers should be created with NewUserManager or NewManagerAt
// pointed at appdir.SkillsDir; NewManager remains the legacy project layout
// used by the TUI and older tests.
func (m *Manager) Install(name, content string, overwrite bool) (Skill, error) {
	return m.InstallWithProvenance(name, content, overwrite, Provenance{})
}

// InstallWithProvenance installs content and records the supplied origin in
// the sidecar. Empty provenance fields are filled from the content hash, and
// source/ref are sanitized again before they reach disk.
func (m *Manager) InstallWithProvenance(name, content string, overwrite bool, prov Provenance) (Skill, error) {
	skill, err := m.ValidateInstall(name, content, overwrite)
	if err != nil {
		return Skill{}, err
	}
	if m.skillsDir() == "" {
		return Skill{}, fmt.Errorf("skill directory is not configured")
	}
	if err := os.MkdirAll(m.skillsDir(), 0o700); err != nil {
		return Skill{}, fmt.Errorf("creating skills directory: %w", err)
	}

	root, err := os.OpenRoot(m.skillsDir())
	if err != nil {
		return Skill{}, fmt.Errorf("opening skills directory: %w", err)
	}
	defer root.Close()

	dirCreated := false
	info, err := root.Lstat(name)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return Skill{}, fmt.Errorf("skill directory %q is a symlink", name)
		}
		if !info.IsDir() {
			return Skill{}, fmt.Errorf("skill path %q exists and is not a directory", name)
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(name, 0o755); err != nil {
			return Skill{}, fmt.Errorf("creating skill directory: %w", err)
		}
		dirCreated = true
	default:
		return Skill{}, fmt.Errorf("inspecting skill directory: %w", err)
	}

	target := filepath.Join(name, SkillFileName)
	provTarget := filepath.Join(name, ProvenanceFileName)
	if existing, err := root.Lstat(target); err == nil {
		if existing.Mode()&os.ModeSymlink != 0 {
			return Skill{}, fmt.Errorf("skill file %q is a symlink", target)
		}
		if !existing.Mode().IsRegular() {
			return Skill{}, fmt.Errorf("skill file %q is not a regular file", target)
		}
		if !overwrite {
			return Skill{}, fmt.Errorf("%w: %s", ErrExists, filepath.Join(m.skillsDir(), target))
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Skill{}, fmt.Errorf("inspecting skill file: %w", err)
	}

	// Snapshot both files before the first write. If the second write fails,
	// both are restored (or removed when new) so a failed install cannot leave
	// a half-written skill or a stale provenance sidecar.
	cleanupNewDir := func() {
		if dirCreated {
			_ = root.Remove(name)
		}
	}
	skillSnapshot, err := snapshotRootFile(root, target)
	if err != nil {
		cleanupNewDir()
		return Skill{}, err
	}
	provSnapshot, err := snapshotRootFile(root, provTarget)
	if err != nil {
		cleanupNewDir()
		return Skill{}, err
	}
	rollback := func(cause error) error {
		var rollbackErrs []error
		if err := restoreRootFile(root, provTarget, provSnapshot); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restoring provenance: %w", err))
		}
		if err := restoreRootFile(root, target, skillSnapshot); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restoring SKILL.md: %w", err))
		}
		if dirCreated {
			if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("removing new skill directory: %w", err))
			}
		}
		if len(rollbackErrs) > 0 {
			return fmt.Errorf("%w; rollback also failed: %v", cause, errors.Join(rollbackErrs...))
		}
		return fmt.Errorf("%w (install rolled back; no files were changed)", cause)
	}

	if err := writeRootFileAtomic(root, target, []byte(content), 0o644); err != nil {
		return Skill{}, rollback(fmt.Errorf("writing skill file: %w", err))
	}

	prov = normalizeProvenance(prov, content)
	provData, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return Skill{}, rollback(fmt.Errorf("encoding skill provenance: %w", err))
	}
	provData = append(provData, '\n')
	if err := writeRootFileAtomic(root, provTarget, provData, 0o644); err != nil {
		return Skill{}, rollback(fmt.Errorf("writing skill provenance: %w", err))
	}

	// Best-effort directory fsync: the file itself was synced before rename,
	// and some platforms do not allow syncing a directory handle.
	if dir, err := root.Open(name); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	m.Discover()
	skill.Path = filepath.Join(m.skillsDir(), target)
	return skill, nil
}

// writeRootFileAtomic writes through an os.Root so every path component stays
// inside the skills tree. The target is replaced in one rename, and the
// temporary file is removed on every failure path.
func writeRootFileAtomic(root *os.Root, name string, data []byte, perm os.FileMode) error {
	if root == nil {
		return fmt.Errorf("skills root is not open")
	}
	if info, err := root.Lstat(name); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("target %q is a symlink", name)
	}

	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	dir := filepath.Dir(name)
	tmp := filepath.Join(dir, "."+filepath.Base(name)+".tmp-"+suffix)

	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = root.Remove(tmp)
		}
	}()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		return err
	}
	renamed = true
	return nil
}

// rootFileSnapshot is the prior state of one file inside the skills root.
type rootFileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func snapshotRootFile(root *os.Root, name string) (rootFileSnapshot, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return rootFileSnapshot{}, nil
	}
	if err != nil {
		return rootFileSnapshot{}, fmt.Errorf("inspecting skill file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return rootFileSnapshot{}, fmt.Errorf("skill file %q is a symlink", name)
	}
	if !info.Mode().IsRegular() {
		return rootFileSnapshot{}, fmt.Errorf("skill file %q is not a regular file", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return rootFileSnapshot{}, fmt.Errorf("reading skill file: %w", err)
	}
	data, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return rootFileSnapshot{}, fmt.Errorf("reading skill file: %w", readErr)
	}
	if closeErr != nil {
		return rootFileSnapshot{}, fmt.Errorf("closing skill file: %w", closeErr)
	}
	return rootFileSnapshot{exists: true, data: data, mode: info.Mode().Perm()}, nil
}

func restoreRootFile(root *os.Root, name string, snapshot rootFileSnapshot) error {
	if !snapshot.exists {
		if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeRootFileAtomic(root, name, snapshot.data, snapshot.mode)
}

func randomSuffix() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generating temporary file name: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func normalizeProvenance(prov Provenance, content string) Provenance {
	prov.Source = SanitizeSource(prov.Source)
	prov.Ref = sanitizeField(prov.Ref)
	sum := sha256.Sum256([]byte(content))
	prov.SHA256 = hex.EncodeToString(sum[:])
	return prov
}

// SanitizeSource removes credentials, query strings and fragments from a
// provenance source. Query strings commonly carry tokens, and a sidecar must
// never become a place where a secret is persisted.
func SanitizeSource(source string) string {
	source = sanitizeField(source)
	if source == "" {
		return ""
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return source
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func sanitizeField(value string) string {
	value = strings.TrimSpace(value)
	return strings.Map(func(r rune) rune {
		if r == 0 || r == '\r' || r == '\n' || r == '\t' {
			return -1
		}
		return r
	}, value)
}
