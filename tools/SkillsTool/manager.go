// Package skillstool is the domain logic behind the skills tab and the
// model-facing skill tool. Skills live in the GoWork layout
// `.GoWork/skills/<name>/SKILL.md` with YAML frontmatter (name +
// description required). The package keeps a single Manager (set at
// startup, like the todo list) that owns the session's loaded-set: the
// skill tool only offers the skills the user loaded in the current session.
package skillstool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SkillsDir is the skills folder relative to the project root, in the
// layout: "<SkillsDir>/<name>/SKILL.md".
const SkillsDir = ".GoWork/skills"

// SkillFileName is the only file name discovery scans.
const SkillFileName = "SKILL.md"

// nameRe is the skill-name rule: lowercase alphanumeric with single hyphen
// separators, 1-64 characters.
var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill is one discovered skill: identity + frontmatter + full body.
type Skill struct {
	Name        string // frontmatter name (must match the dir name)
	Description string // frontmatter description
	Body        string // everything after the frontmatter separator
	Path        string // absolute SKILL.md path
}

// ErrExists is returned by Create when the skill file already exists.
var ErrExists = fmt.Errorf("skill already exists")

// Manager is the session-wide skills registry. The loaded set is what the
// model's skill tool sees; discovery snapshots are what the tab renders.
type Manager struct {
	mu      sync.RWMutex
	root    string
	loaded  map[string]string // skill name -> loaded-at timestamp
	entries []Skill           // last Discover() snapshot
}

var mgr *Manager

// SetManager installs the global manager (called once at startup). The
// skill tool and the tab both read through it.
func SetManager(m *Manager) {
	mgr = m
}

// GetManager returns the installed manager, or nil before SetManager ran.
func GetManager() *Manager {
	return mgr
}

// NewManager builds a manager rooted at the project directory.
func NewManager(root string) *Manager {
	if root == "" {
		root = "."
	}
	return &Manager{root: root, loaded: make(map[string]string)}
}

func (m *Manager) Root() string { return m.root }

// skillsDir returns the absolute path of the skills folder.
func (m *Manager) skillsDir() string {
	return filepath.Join(m.root, SkillsDir)
}

// Exists reports whether a skill with the given (already valid) name exists
// on disk.
func (m *Manager) Exists(name string) bool {
	_, err := os.Stat(filepath.Join(m.skillsDir(), name, SkillFileName))
	return err == nil
}

// Discover scans the skills folder for every <name>/SKILL.md and parses its
// frontmatter. Malformed files are skipped, never crashing the tab. The
// snapshot is cached for the tab and refreshed on every call.
func (m *Manager) Discover() []Skill {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = m.scanLocked()
	return m.entries
}

// scanLocked performs the directory walk; callers hold m.mu.
func (m *Manager) scanLocked() []Skill {
	dirs, err := os.ReadDir(m.skillsDir())
	if err != nil {
		return nil
	}
	out := make([]Skill, 0, len(dirs))
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		path := filepath.Join(m.skillsDir(), d.Name(), SkillFileName)
		s, ok := parseSkill(path)
		if !ok {
			continue
		}
		// name must equal the dir name; keep only valid names so the
		// skill tool can't be confused by a misfiled SKILL.md.
		if s.Name == "" || !NameValid(s.Name) || s.Name != d.Name() {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entries returns the last snapshot (or rescans if discovery never ran).
func (m *Manager) Entries() []Skill {
	m.mu.RLock()
	if m.entries != nil {
		e := m.entries
		m.mu.RUnlock()
		return e
	}
	m.mu.RUnlock()
	return m.Discover()
}

// Content returns the raw markdown of a skill, or "" if the file can't be
// read. Names are validated so a weird directory name can't be used to
// escape the skills folder.
func (m *Manager) Content(name string) string {
	if !validName(name) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(m.skillsDir(), name, SkillFileName))
	if err != nil {
		return ""
	}
	return string(b)
}

// --- loaded set ---------------------------------------------------------

// LoadedNames returns the session's loaded skill names, sorted.
func (m *Manager) LoadedNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.loaded))
	for name := range m.loaded {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) IsLoaded(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.loaded[name]
	return ok
}

// Load marks a skill as loaded for the session. Returns false when the name
// isn't a real skill on disk.
func (m *Manager) Load(name string) bool {
	if !validName(name) || !m.Exists(name) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.loaded[name]; !ok {
		m.loaded[name] = time.Now().Format(time.RFC3339)
	}
	return true
}

// Unload removes a skill from the session's loaded set. The file stays on
// disk; only the session's exposure to the model changes.
func (m *Manager) Unload(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.loaded, name)
}

// LoadedSummary pairs a loaded skill's name with its description, the two
// fields the <available_skills> block shows the model.
type LoadedSummary struct {
	Name        string
	Description string
}

// Available returns name+description of every loaded skill (from the
// discovery snapshot), sorted by name.
func (m *Manager) Available() []LoadedSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LoadedSummary, 0, len(m.loaded))
	for _, e := range m.entries {
		if _, ok := m.loaded[e.Name]; ok {
			out = append(out, LoadedSummary{Name: e.Name, Description: e.Description})
		}
	}
	return out
}

// --- creating -----------------------------------------------------------

// Slugify normalizes free text into a valid skill name: lowercase
// alphanumerics joined by single hyphens, max 64 chars.
func Slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func validName(name string) bool {
	return name != "" && len(name) <= 64 && nameRe.MatchString(name)
}

// NameValid is the exported validity check.
func NameValid(name string) bool {
	return validName(name)
}

// Create writes a new skill to disk: mkdir -p the folder, then SKILL.md with
// generated frontmatter and the given body. It refuses to overwrite an
// existing skill (ErrExists). name should come from Slugify.
func (m *Manager) Create(name, desc, body string) (Skill, error) {
	if !validName(name) {
		return Skill{}, fmt.Errorf("invalid skill name %q: use lowercase letters, digits and hyphens only", name)
	}
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return Skill{}, fmt.Errorf("skill %q needs a description", name)
	}
	dir := filepath.Join(m.skillsDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("creating skill folder: %w", err)
	}
	path := filepath.Join(dir, SkillFileName)
	if _, err := os.Stat(path); err == nil {
		return Skill{}, fmt.Errorf("%w: %s", ErrExists, path)
	}
	content := buildSkillMarkdown(name, desc, body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Skill{}, fmt.Errorf("writing skill file: %w", err)
	}
	skill, _ := parseSkill(path)
	return skill, nil
}

// buildSkillMarkdown assembles the SKILL.md file: the frontmatter block with
// name/description, then the user's prompt text verbatim as the body.
func buildSkillMarkdown(name, desc, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	b.WriteString("description: " + strings.ReplaceAll(strings.TrimSpace(desc), "\n", " ") + "\n")
	b.WriteString("---\n\n")
	if t := strings.TrimSpace(body); t != "" {
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String()
}

// --- frontmatter parsing -------------------------------------------------

// parseSkill reads and parses a SKILL.md. Unreadable/malformed files return
// ok=false. The frontmatter is a minimal YAML subset (only name and
// description matter; everything else is ignored). Indented continuation
// lines fold into the previous value, so multi-line descriptions work.
func parseSkill(path string) (Skill, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, false
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		// No frontmatter: the whole file is body; name stays empty and the
		// caller decides whether a name-less skill is usable.
		return Skill{Path: path, Body: strings.TrimSpace(text)}, true
	}

	name := ""
	descParts := []string{}
	foundName := false
	foundDesc := false
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimSpace(raw)
		if line == "---" {
			closeIdx = i
			break
		}
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			// Continuation line (indented): fold into the active value.
			if foundDesc {
				descParts = append(descParts, strings.TrimSpace(line))
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			if !foundName {
				name = strings.TrimSpace(val)
				foundName = true
			}
		case "description":
			if !foundDesc {
				foundDesc = true
				descParts = []string{strings.TrimSpace(val)}
			}
		}
	}

	skill := Skill{Path: path, Name: name, Description: strings.Join(descParts, " ")}
	if closeIdx < 0 {
		// Unterminated frontmatter: treat everything as body.
		skill.Body = strings.TrimSpace(text)
		return skill, true
	}
	bodyStart := closeIdx + 1
	if bodyStart < len(lines) {
		skill.Body = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	}
	return skill, true
}
