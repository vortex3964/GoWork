// Package remotesource resolves skill and MCP sources into deterministic
// candidate files and fetches their exact bytes.
//
// Supported inputs are raw HTTP(S) URLs, GitHub blob URLs, GitHub repository
// URLs (optionally with a branch and subdirectory), owner/repo shorthand, and
// project-relative local paths. GitHub repositories are discovered through
// the tree API, including a directory walk fallback when GitHub marks the
// recursive tree as truncated. Private repositories use an optional token.
package remotesource

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SpecKind identifies how an input string was interpreted.
type SpecKind string

const (
	// SpecRawURL is a direct HTTP(S) file URL.
	SpecRawURL SpecKind = "raw-url"

	// SpecGitHubBlob is a GitHub file URL (/blob/, /raw/).
	SpecGitHubBlob SpecKind = "github-blob"

	// SpecGitHubRepo is a GitHub repository URL or owner/repo shorthand.
	SpecGitHubRepo SpecKind = "github-repo"

	// SpecLocal is a project-relative (or project-contained absolute) path.
	SpecLocal SpecKind = "local"
)

// Spec is the parsed form of a source input.
type Spec struct {
	Kind      SpecKind
	Input     string
	URL       string // exact URL for SpecRawURL
	Owner     string
	Repo      string
	Ref       string // branch, tag or commit from the input; empty means default
	Path      string // repository-relative path or directory prefix
	LocalPath string // absolute path for SpecLocal
}

var githubNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Parse interprets input without touching the network. A two-segment value
// such as "owner/repo" is treated as GitHub shorthand; Resolver additionally
// checks whether a matching local path exists before using that shorthand.
func Parse(input string) (Spec, error) {
	return parseInput(input, "")
}

func parseInput(input, root string) (Spec, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Spec{}, fmt.Errorf("%w: source is empty", ErrInvalidSource)
	}

	if strings.Contains(raw, "://") {
		return parseURLSpec(raw)
	}

	if looksLikeLocal(raw, root) {
		return localSpec(raw, root)
	}
	if owner, repo, ref, ok := parseOwnerRepo(raw); ok {
		return Spec{
			Kind:  SpecGitHubRepo,
			Input: raw,
			Owner: owner,
			Repo:  repo,
			Ref:   ref,
		}, nil
	}
	return localSpec(raw, root)
}

func parseURLSpec(raw string) (Spec, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return Spec{}, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return Spec{}, fmt.Errorf("%w: unsupported URL scheme %q", ErrInvalidSource, parsed.Scheme)
	}
	if parsed.User != nil {
		return Spec{}, fmt.Errorf("%w: URLs with embedded credentials are not allowed", ErrInvalidSource)
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	switch host {
	case "github.com", "www.github.com":
		return parseGitHubURL(raw, parsed)
	case "raw.githubusercontent.com":
		segments := splitPath(parsed.Path)
		if len(segments) < 4 {
			return Spec{}, fmt.Errorf("%w: raw GitHub URL needs owner, repo, ref and path", ErrInvalidSource)
		}
		return Spec{
			Kind:  SpecRawURL,
			Input: raw,
			URL:   raw,
			Owner: segments[0],
			Repo:  strings.TrimSuffix(segments[1], ".git"),
			Ref:   segments[2],
			Path:  strings.Join(segments[3:], "/"),
		}, nil
	default:
		return Spec{Kind: SpecRawURL, Input: raw, URL: raw}, nil
	}
}

func parseGitHubURL(raw string, parsed *url.URL) (Spec, error) {
	segments := splitPath(parsed.Path)
	if len(segments) < 2 {
		return Spec{}, fmt.Errorf("%w: GitHub URL needs an owner and repository", ErrInvalidSource)
	}
	owner := segments[0]
	repo := strings.TrimSuffix(segments[1], ".git")
	if !validGitHubName(owner) || !validGitHubName(repo) {
		return Spec{}, fmt.Errorf("%w: invalid GitHub owner or repository", ErrInvalidSource)
	}
	if len(segments) == 2 {
		return Spec{Kind: SpecGitHubRepo, Input: raw, Owner: owner, Repo: repo}, nil
	}

	action := segments[2]
	rest := segments[3:]
	switch action {
	case "blob", "raw":
		if len(rest) < 2 {
			return Spec{}, fmt.Errorf("%w: GitHub file URL needs a ref and path", ErrInvalidSource)
		}
		return Spec{
			Kind:  SpecGitHubBlob,
			Input: raw,
			Owner: owner,
			Repo:  repo,
			Ref:   rest[0],
			Path:  strings.Join(rest[1:], "/"),
		}, nil
	case "tree":
		ref := ""
		if len(rest) > 0 {
			ref = rest[0]
		}
		path := ""
		if len(rest) > 1 {
			path = strings.Join(rest[1:], "/")
		}
		return Spec{
			Kind:  SpecGitHubRepo,
			Input: raw,
			Owner: owner,
			Repo:  repo,
			Ref:   ref,
			Path:  path,
		}, nil
	default:
		return Spec{}, fmt.Errorf("%w: unsupported GitHub URL path", ErrInvalidSource)
	}
}

func localSpec(raw, root string) (Spec, error) {
	if root == "" {
		return Spec{
			Kind:      SpecLocal,
			Input:     raw,
			LocalPath: filepath.Clean(raw),
		}, nil
	}
	path, err := resolveLocalPath(raw, root)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Kind: SpecLocal, Input: raw, LocalPath: path}, nil
}

// resolveLocalPath joins a project-relative path to root and refuses to
// escape the project. Absolute paths must also stay inside root.
func resolveLocalPath(input, root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve project root: %v", ErrInvalidSource, err)
	}
	path := input
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootAbs, path)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve local path: %v", ErrInvalidSource, err)
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", fmt.Errorf("%w: resolve local path: %v", ErrInvalidSource, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: local path escapes the project root", ErrInvalidSource)
	}
	return filepath.Clean(pathAbs), nil
}

func looksLikeLocal(input, root string) bool {
	if filepath.IsAbs(input) || filepath.VolumeName(input) != "" {
		return true
	}
	if strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") ||
		strings.HasPrefix(input, `.\\`) || strings.HasPrefix(input, `..\\`) ||
		input == "." || input == ".." {
		return true
	}
	parts := splitPath(input)
	if len(parts) > 2 {
		return true
	}
	if root != "" && len(parts) > 0 {
		candidate := filepath.Join(root, input)
		if info, err := os.Stat(candidate); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return true
		}
	}
	return false
}

func parseOwnerRepo(input string) (owner, repo, ref string, ok bool) {
	if input == "" || strings.ContainsAny(input, " \t\r\n") {
		return "", "", "", false
	}
	if at := strings.LastIndexByte(input, '@'); at >= 0 {
		ref = strings.TrimSpace(input[at+1:])
		input = input[:at]
		if ref == "" {
			return "", "", "", false
		}
	}
	parts := splitPath(input)
	if len(parts) != 2 {
		return "", "", "", false
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	if !validGitHubName(owner) || !validGitHubName(repo) {
		return "", "", "", false
	}
	return owner, repo, ref, true
}

func validGitHubName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 100 && githubNameRE.MatchString(name)
}

func splitPath(value string) []string {
	value = strings.Trim(value, `/\`)
	if value == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' })
}
