package skillstool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"GoWork/internal/netfetch"
	"GoWork/internal/remotesource"
	"GoWork/providers"
	"GoWork/tools"
)

// InstallToolName is the model-facing identifier of the install tool.
const InstallToolName = "skill_install"

// InstallToolOptions lets callers (and tests) supply the same resolver
// injection points as remotesource.Options. Zero values use the production
// network client and the GITHUB_TOKEN/GH_TOKEN environment variables.
type InstallToolOptions struct {
	Fetcher    remotesource.Fetcher
	APIBaseURL string
	Token      string
	UserAgent  string
}

// InstallTool installs a SKILL.md from a URL, GitHub repository, or
// project-relative local path. It never enables a skill accidentally: load
// must be requested explicitly.
type InstallTool struct {
	opts InstallToolOptions
}

// NewInstallTool returns the install tool with production resolver settings.
func NewInstallTool() tools.AgentTool { return &InstallTool{} }

// NewInstallToolWithOptions returns the install tool with injected resolver
// options. It exists so the dispatcher and tests can point at a fake GitHub
// API without weakening the production network guard.
func NewInstallToolWithOptions(opts InstallToolOptions) tools.AgentTool {
	return &InstallTool{opts: opts}
}

func (t *InstallTool) Name() string { return InstallToolName }

func (t *InstallTool) Kind() tools.Kind { return tools.KindNotAllowed }

func (t *InstallTool) Description() string {
	return `Installs a skill (a SKILL.md instruction set) into the durable user skills directory and optionally loads it for this session. This tool is self-contained; no separate bootstrap skill is needed.

source accepts:
- "owner/repo" or "https://github.com/owner/repo" for a repository (pass path when it has several SKILL.md files)
- "https://github.com/owner/repo/blob/<ref>/path/SKILL.md", a raw https:// URL, or a gist raw URL for one file
- a project-relative path such as "skills/review/SKILL.md" (absolute paths must stay inside the project)

path selects a repository-relative SKILL.md or its directory when discovery finds several; the error lists the sorted candidates. ref applies only to owner/repo or a bare GitHub repository URL and accepts a branch, tag, or full commit SHA (prefer a full SHA). Never put a token in source; set GITHUB_TOKEN or GH_TOKEN in the environment for private repositories or GitHub rate limits.

Workflow: call with dry_run=true first to validate source/ref/path and the frontmatter, then repeat without dry_run and with load=true when the user wants to use it now. Installation requires approval and is atomic; an existing skill is kept unless overwrite=true. Success reports the path, provenance hash, on-disk verification, and loaded state; a loaded skill is immediately available through the skill tool. Failures state the exact next call.`
}

func (t *InstallTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": `"owner/repo", "https://github.com/owner/repo", a GitHub blob/raw URL, or a project-relative path to SKILL.md. Never include credentials.`,
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Repository-relative SKILL.md path or directory prefix when the source has multiple SKILL.md files. Use one of the candidate paths from the error.",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Branch, tag, or full commit SHA for an owner/repo or bare GitHub repository URL. Rejected for raw URLs, local paths, and sources that already pin a ref; prefer a full commit SHA.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Expected skill name. When set it must equal the frontmatter name and the directory name.",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "Replace an existing skill with the same name. Defaults to false; inspect the installed skill first.",
			},
			"load": map[string]any{
				"type":        "boolean",
				"description": "Also load the skill into this session so the skill tool can use it immediately. Defaults to false.",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Validate the source, ref, path, and frontmatter but write nothing. Call this first, then repeat without dry_run to install.",
			},
		},
		"required":             []string{"source"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

type installInput struct {
	Source    string `json:"source"`
	Path      string `json:"path"`
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Overwrite bool   `json:"overwrite"`
	Load      bool   `json:"load"`
	DryRun    bool   `json:"dry_run"`
}

func (t *InstallTool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var in installInput
	if err := json.Unmarshal(rawInput, &in); err != nil {
		return tools.ToolResult{}, fmt.Errorf("skill_install: invalid input: %w", err)
	}
	sourceInput := strings.TrimSpace(in.Source)
	if sourceInput == "" {
		return tools.Errf("skill_install: missing required field \"source\""), nil
	}

	m := GetManager()
	if m == nil {
		return tools.Errf("skill_install: skills are not available in this session"), nil
	}

	root := ""
	if args.RootPath != nil {
		root = strings.TrimSpace(*args.RootPath)
	}
	if root == "" {
		root = m.Root()
	}

	withRef, err := applyInstallRef(sourceInput, in.Ref, root)
	if err != nil {
		return tools.Errf("skill_install: %s\nnext call: %s", safeToolError(err, t.secret()), installRefNextCall()), nil
	}

	opts := remotesource.Options{
		Root:       root,
		Token:      t.token(),
		Fetcher:    t.opts.Fetcher,
		APIBaseURL: t.opts.APIBaseURL,
		UserAgent:  t.opts.UserAgent,
	}
	resolver := remotesource.New(opts)
	source, err := resolver.Resolve(ctx, withRef)
	if err != nil {
		return tools.Errf("skill_install: %s%s", safeToolError(err, t.secret()), installErrorNextCall(err)), nil
	}

	candidate, err := selectSkillCandidate(source, in.Path)
	if err != nil {
		return tools.Errf("skill_install: %s", safeToolError(err, t.secret())), nil
	}
	if candidate.LocalPath != "" {
		info, err := os.Lstat(candidate.LocalPath)
		if err != nil {
			return tools.Errf("skill_install: local skill %q is not readable: %s\nnext call: check the path relative to the project root (or use owner/repo) and retry", candidate.Path, safeToolError(err, t.secret())), nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return tools.Errf("skill_install: local skill %q is a symlink; install the real file instead\nnext call: pass the real SKILL.md path with source, or use owner/repo", candidate.Path), nil
		}
		if !info.Mode().IsRegular() {
			return tools.Errf("skill_install: local skill %q is not a regular file\nnext call: pass a project-relative path to one SKILL.md file, or use owner/repo", candidate.Path), nil
		}
	}

	data, err := source.Fetch(ctx, candidate)
	if err != nil {
		return tools.Errf("skill_install: fetching %s: %s%s", candidate.Path, safeToolError(err, t.secret()), installErrorNextCall(err)), nil
	}
	content := string(data)

	name := strings.TrimSpace(in.Name)
	if name != "" && !NameValid(name) {
		return tools.Errf("skill_install: invalid skill name %q: use lowercase letters, digits and hyphens only\nnext call: omit name to use the frontmatter name, or pick a valid name", name), nil
	}
	if name == "" {
		parsed, err := ParseSkillContent(content)
		if err != nil {
			return tools.Errf("skill_install: invalid skill %s: %s\nnext call: fix the SKILL.md frontmatter (name and description) in the source, or choose another path; nothing was installed", candidate.Path, safeToolError(err, t.secret())), nil
		}
		name = parsed.Name
	}

	skill, err := m.ValidateInstall(name, content, in.Overwrite)
	if err != nil {
		if errors.Is(err, ErrExists) {
			return tools.Errf("skill_install: %s\nnext call: skill_load {\"action\":\"list\"} to inspect the installed skill, or repeat skill_install with \"overwrite\":true after approval", safeToolError(err, t.secret())), nil
		}
		return tools.Errf("skill_install: %s\nnext call: fix the SKILL.md frontmatter (name and description) in the source, or choose another path; nothing was installed", safeToolError(err, t.secret())), nil
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	ref := sanitizeField(source.Ref)
	// Record the input the user supplied, not the internal "@ref" variant.
	provenanceSource := SanitizeSource(sourceInput)

	if in.DryRun {
		return tools.Ok(fmt.Sprintf(
			"dry run: would install skill %q from %s\npath: %s\nref: %s\nsha256: %s\nsize: %d bytes\nverified: source, ref, path, and frontmatter are valid; nothing was written\nnext call: repeat skill_install without \"dry_run\" and with \"load\":true to install and use it",
			skill.Name, provenanceSource, candidate.Path, displayRef(ref), digest, len(data),
		)), nil
	}

	installed, err := m.InstallWithProvenance(name, content, in.Overwrite, Provenance{
		Source: provenanceSource,
		Ref:    ref,
		SHA256: digest,
	})
	if err != nil {
		if errors.Is(err, ErrExists) {
			return tools.Errf("skill_install: %s\nnext call: skill_load {\"action\":\"list\"} to inspect the installed skill, or repeat skill_install with \"overwrite\":true after approval", safeToolError(err, t.secret())), nil
		}
		return tools.Errf("skill_install: %s\nnext call: fix the source or skill permissions and retry; the install was rolled back and no files were changed", safeToolError(err, t.secret())), nil
	}

	if in.Load {
		if !m.Load(installed.Name) {
			return tools.Errf("skill_install: installed %q but could not load it; it may have been removed from disk\nnext call: skill_load {\"action\":\"list\"} to inspect installed skills, then skill_load {\"action\":\"load\",\"name\":%q}", installed.Name, installed.Name), nil
		}
		rebuildSkillToolDef()
	} else if m.IsLoaded(installed.Name) {
		// An overwrite can change the description shown in the tool block.
		rebuildSkillToolDef()
	}

	loaded := "no"
	loadNote := fmt.Sprintf("next call: skill_load {\"action\":\"load\",\"name\":%q} to use it now, or repeat skill_install with \"load\":true", installed.Name)
	if m.IsLoaded(installed.Name) {
		loaded = "yes (available through the skill tool now)"
		loadNote = ""
	}
	message := fmt.Sprintf(
		"installed skill %q\npath: %s\nsource: %s\nref: %s\nsha256: %s\nverified: SKILL.md is on disk and discoverable\nloaded: %s",
		installed.Name, installed.Path, provenanceSource, displayRef(ref), digest, loaded,
	)
	if loadNote != "" {
		message += "\n" + loadNote
	}
	return tools.Ok(message), nil
}

func displayRef(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "(default branch)"
	}
	return ref
}

// installRefNextCall tells the model how to fix ref misuse without guessing.
func installRefNextCall() string {
	return `use ref only with owner/repo or a bare GitHub repository URL, without whitespace or "/" (prefer a full commit SHA); omit it for raw URLs, local paths, and sources that already contain @ref or /tree/<ref>`
}

// installErrorNextCall adds a concrete recovery step to resolver/fetch errors.
// It classifies the stable remotesource sentinels, so callers do not depend on
// message wording.
func installErrorNextCall(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, remotesource.ErrRateLimited):
		return "\nnext call: wait until the reset time shown above, set GITHUB_TOKEN or GH_TOKEN in the environment (never in source), or use a raw HTTPS URL or project-relative path to avoid the GitHub API"
	case errors.Is(err, remotesource.ErrUnauthorized):
		return "\nnext call: set a valid GITHUB_TOKEN or GH_TOKEN in the environment, or use a public source; do not put the token in source"
	case errors.Is(err, remotesource.ErrForbidden):
		return "\nnext call: check that the token can read this repository, or use a public source or project-relative path"
	case errors.Is(err, remotesource.ErrNotFound):
		return "\nnext call: verify source, path and ref; for a private repository set GITHUB_TOKEN or GH_TOKEN in the environment, or use a project-relative path to SKILL.md"
	case errors.Is(err, remotesource.ErrNoCandidates):
		return "\nnext call: confirm the repository contains SKILL.md, or pass path to the directory containing it; use a GitHub blob/raw URL or local path to name one file"
	case errors.Is(err, remotesource.ErrTreeTruncated):
		return "\nnext call: pass path to the repository subdirectory that contains SKILL.md so discovery does not need the full tree"
	case errors.Is(err, remotesource.ErrInvalidSource):
		return "\nnext call: use owner/repo, a GitHub repository/blob URL, a raw https:// URL, or a project-relative path to SKILL.md"
	default:
		return "\nnext call: check the source, path and ref, then retry; use dry_run=true to validate without writing"
	}
}

func (t *InstallTool) token() string {
	if token := strings.TrimSpace(t.opts.Token); token != "" {
		return token
	}
	return netfetch.GitHubTokenFromEnv()
}

func (t *InstallTool) secret() string { return t.token() }

// applyInstallRef applies the separate ref field without guessing: only
// GitHub repository sources that do not already pin a ref accept it.
func applyInstallRef(source, ref, root string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return source, nil
	}
	if strings.ContainsAny(ref, " \t\r\n") || strings.Contains(ref, "/") {
		return "", fmt.Errorf("invalid ref: must not contain whitespace or path separators")
	}
	source = strings.TrimSpace(source)

	if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil {
			return "", fmt.Errorf("invalid GitHub repository URL")
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if host != "github.com" && host != "www.github.com" {
			return "", fmt.Errorf("ref is only supported for GitHub repository sources")
		}
		segments := splitNonEmpty(parsed.Path)
		if len(segments) != 2 {
			return "", fmt.Errorf("source already pins a ref or path; remove the separate ref field")
		}
		return strings.TrimRight(source, "/") + "/tree/" + url.PathEscape(ref), nil
	}

	if filepath.IsAbs(source) || strings.HasPrefix(source, ".") {
		return "", fmt.Errorf("ref is not valid for a local source")
	}
	if root != "" {
		if info, err := os.Stat(filepath.Join(root, source)); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return "", fmt.Errorf("ref is not valid for a local source")
		}
	}
	if strings.Contains(source, "@") {
		return "", fmt.Errorf("ref was supplied both in source and in the ref field")
	}
	spec, err := remotesource.Parse(source)
	if err != nil || spec.Kind != remotesource.SpecGitHubRepo {
		return "", fmt.Errorf("ref is only supported for owner/repo or GitHub repository sources")
	}
	return source + "@" + ref, nil
}

func splitNonEmpty(value string) []string {
	value = strings.Trim(value, `/\`)
	if value == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' })
}

// selectSkillCandidate narrows a resolved source to exactly one SKILL.md.
// Multiple skills are deliberately ambiguous: the caller must say which one.
func selectSkillCandidate(source *remotesource.Source, requested string) (remotesource.Candidate, error) {
	if source == nil {
		return remotesource.Candidate{}, fmt.Errorf("source did not resolve")
	}
	skills := make([]remotesource.Candidate, 0, len(source.Candidates))
	for _, candidate := range source.Candidates {
		if candidate.Kind == remotesource.KindSkill {
			skills = append(skills, candidate)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Path < skills[j].Path })
	if len(skills) == 0 {
		return remotesource.Candidate{}, fmt.Errorf("no SKILL.md found in %s\nnext call: confirm the repository contains SKILL.md, or pass source as a GitHub blob/raw URL or project-relative path to one SKILL.md", SanitizeSource(source.Input))
	}

	want := strings.Trim(strings.ReplaceAll(strings.TrimSpace(requested), `\`, "/"), "/")
	if want != "" {
		for _, segment := range strings.Split(want, "/") {
			if segment == ".." {
				return remotesource.Candidate{}, fmt.Errorf("path %q escapes the source\nnext call: pass a repository-relative path from the candidate list", requested)
			}
		}
		matches := make([]remotesource.Candidate, 0, 1)
		for _, candidate := range skills {
			if candidate.Path == want || strings.HasPrefix(candidate.Path, want+"/") {
				matches = append(matches, candidate)
			}
		}
		switch len(matches) {
		case 0:
			return remotesource.Candidate{}, fmt.Errorf("no SKILL.md matches path %q; candidates: %s\nnext call: repeat skill_install with \"path\" set to one of the candidate paths above", requested, candidateList(skills))
		case 1:
			return matches[0], nil
		default:
			return remotesource.Candidate{}, fmt.Errorf("path %q matches multiple SKILL.md files; be more specific: %s\nnext call: repeat skill_install with \"path\" set to one exact candidate path", requested, candidateList(matches))
		}
	}

	if len(skills) == 1 {
		return skills[0], nil
	}
	return remotesource.Candidate{}, fmt.Errorf("source contains %d SKILL.md files; specify path: %s\nnext call: repeat skill_install with \"path\" set to one candidate path (or its directory) and \"load\":true", len(skills), candidateList(skills))
}

func candidateList(candidates []remotesource.Candidate) string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.Path)
	}
	sort.Strings(paths)
	return strings.Join(paths, ", ")
}

// safeToolError keeps tool failures readable without ever echoing a token.
// It redacts known secrets plus any URL userinfo or query-string value that
// a library error might have copied out of the source.
func safeToolError(err error, secrets ...string) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = userInfoInErrorRE.ReplaceAllString(message, "://[redacted]@")
	message = queryValueInErrorRE.ReplaceAllString(message, "${1}[redacted]")
	return message
}

var (
	userInfoInErrorRE   = regexp.MustCompile(`://[^/\s@]+@`)
	queryValueInErrorRE = regexp.MustCompile(`([?&][^=\s&]+=)([^&\s]+)`)
)

// rebuildSkillToolDef refreshes the registered skill tool's description so
// the model sees the current loaded set on its next turn. It only updates an
// existing registration; it never adds or removes tools.
func rebuildSkillToolDef() {
	providers.UpdateToolDef(ToolName, ToolDef())
}
