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
	return `Installs a skill (a SKILL.md instruction set) into the durable user skills directory. The source may be a raw HTTP(S) URL, a GitHub blob/repository URL, owner/repo shorthand, or a path relative to the current project. When a repository contains several SKILL.md files, pass path to choose one. Installation is validated and atomic; existing skills are kept unless overwrite is true. Set dry_run to inspect what would be installed without writing anything, and load to make the skill available to this session immediately.`
}

func (t *InstallTool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Raw URL, GitHub blob/repository URL, owner/repo, or project-relative local path.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Repository-relative SKILL.md path (or directory prefix) when the source has multiple candidates.",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Git branch, tag or commit. Only valid for GitHub repository sources that do not already pin one.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Expected skill name. When set it must equal the frontmatter name and the directory name.",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "Replace an existing skill with the same name. Defaults to false.",
			},
			"load": map[string]any{
				"type":        "boolean",
				"description": "Add the installed skill to this session's loaded set. Defaults to false.",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Validate and fetch the source but write nothing. Defaults to false.",
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
		return tools.Errf("skill_install: %s", safeToolError(err, t.secret())), nil
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
		return tools.Errf("skill_install: %s", safeToolError(err, t.secret())), nil
	}

	candidate, err := selectSkillCandidate(source, in.Path)
	if err != nil {
		return tools.Errf("skill_install: %s", safeToolError(err, t.secret())), nil
	}
	if candidate.LocalPath != "" {
		info, err := os.Lstat(candidate.LocalPath)
		if err != nil {
			return tools.Errf("skill_install: inspecting local skill: %s", safeToolError(err, t.secret())), nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return tools.Errf("skill_install: local skill %q is a symlink; install the real file instead", candidate.Path), nil
		}
		if !info.Mode().IsRegular() {
			return tools.Errf("skill_install: local skill %q is not a regular file", candidate.Path), nil
		}
	}

	data, err := source.Fetch(ctx, candidate)
	if err != nil {
		return tools.Errf("skill_install: fetching %s: %s", candidate.Path, safeToolError(err, t.secret())), nil
	}
	content := string(data)

	name := strings.TrimSpace(in.Name)
	if name == "" {
		parsed, err := ParseSkillContent(content)
		if err != nil {
			return tools.Errf("skill_install: invalid skill %s: %s", candidate.Path, safeToolError(err, t.secret())), nil
		}
		name = parsed.Name
	}

	skill, err := m.ValidateInstall(name, content, in.Overwrite)
	if err != nil {
		if errors.Is(err, ErrExists) {
			return tools.Errf("skill_install: %s (pass overwrite=true to replace it)", safeToolError(err, t.secret())), nil
		}
		return tools.Errf("skill_install: %s", safeToolError(err, t.secret())), nil
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	ref := sanitizeField(source.Ref)
	// Record the input the user supplied, not the internal "@ref" variant.
	provenanceSource := SanitizeSource(sourceInput)

	if in.DryRun {
		return tools.Ok(fmt.Sprintf(
			"dry run: would install skill %q from %s\npath: %s\nsha256: %s\nsize: %d bytes\nnothing was written",
			skill.Name, provenanceSource, candidate.Path, digest, len(data),
		)), nil
	}

	installed, err := m.InstallWithProvenance(name, content, in.Overwrite, Provenance{
		Source: provenanceSource,
		Ref:    ref,
		SHA256: digest,
	})
	if err != nil {
		if errors.Is(err, ErrExists) {
			return tools.Errf("skill_install: %s (pass overwrite=true to replace it)", safeToolError(err, t.secret())), nil
		}
		return tools.Errf("skill_install: %s", safeToolError(err, t.secret())), nil
	}

	if in.Load {
		if !m.Load(installed.Name) {
			return tools.Errf("skill_install: installed %q but could not load it; it may have been removed from disk", installed.Name), nil
		}
		rebuildSkillToolDef()
	} else if m.IsLoaded(installed.Name) {
		// An overwrite can change the description shown in the tool block.
		rebuildSkillToolDef()
	}

	loaded := "no"
	if m.IsLoaded(installed.Name) {
		loaded = "yes"
	}
	return tools.Ok(fmt.Sprintf(
		"installed skill %q\npath: %s\nsource: %s\nref: %s\nsha256: %s\nloaded: %s",
		installed.Name, installed.Path, provenanceSource, ref, digest, loaded,
	)), nil
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
	if len(skills) == 0 {
		return remotesource.Candidate{}, fmt.Errorf("no SKILL.md found in %s", SanitizeSource(source.Input))
	}

	want := strings.Trim(strings.ReplaceAll(strings.TrimSpace(requested), `\`, "/"), "/")
	if want != "" {
		for _, segment := range strings.Split(want, "/") {
			if segment == ".." {
				return remotesource.Candidate{}, fmt.Errorf("path %q escapes the source", requested)
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
			return remotesource.Candidate{}, fmt.Errorf("no SKILL.md matches path %q; candidates: %s", requested, candidateList(skills))
		case 1:
			return matches[0], nil
		default:
			return remotesource.Candidate{}, fmt.Errorf("path %q matches multiple SKILL.md files; be more specific: %s", requested, candidateList(matches))
		}
	}

	if len(skills) == 1 {
		return skills[0], nil
	}
	return remotesource.Candidate{}, fmt.Errorf("source contains %d SKILL.md files; specify path: %s", len(skills), candidateList(skills))
}

func candidateList(candidates []remotesource.Candidate) string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.Path)
	}
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
