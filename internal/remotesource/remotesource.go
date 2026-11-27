package remotesource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"GoWork/internal/netfetch"
)

const (
	defaultAPIBase   = "https://api.github.com"
	githubAcceptJSON = "application/vnd.github+json"
	githubAcceptRaw  = "application/vnd.github.raw"
	githubAPIVersion = "2022-11-28"
)

var (
	// ErrInvalidSource is returned for malformed, unsupported or escaping
	// source inputs.
	ErrInvalidSource = errors.New("remotesource: invalid source")

	// ErrNotFound is returned when a local path, repository or file does not
	// exist (or is private and no usable token was supplied).
	ErrNotFound = errors.New("remotesource: source not found")

	// ErrUnauthorized is returned when GitHub rejects the supplied token.
	ErrUnauthorized = errors.New("remotesource: GitHub authentication failed")

	// ErrForbidden is returned when the token lacks access to a resource.
	ErrForbidden = errors.New("remotesource: access forbidden")

	// ErrRateLimited is returned when the GitHub API rate limit is exhausted.
	ErrRateLimited = errors.New("remotesource: GitHub API rate limit exceeded")

	// ErrNoCandidates is returned when discovery finds no SKILL.md or MCP JSON
	// files.
	ErrNoCandidates = errors.New("remotesource: no installable files found")

	// ErrTreeTruncated is returned when even the per-directory tree walk is
	// truncated and discovery cannot be complete.
	ErrTreeTruncated = errors.New("remotesource: repository tree is truncated")
)

// FileKind classifies a discovered file.
type FileKind string

const (
	// KindSkill is a SKILL.md file.
	KindSkill FileKind = "skill"

	// KindMCP is a recognized MCP JSON configuration file.
	KindMCP FileKind = "mcp"

	// KindOther is a directly requested file that is neither of the above.
	KindOther FileKind = "other"
)

// Candidate is one deterministic installable file found by Resolve.
type Candidate struct {
	Path      string   // project- or repository-relative slash path
	Kind      FileKind // skill, mcp or other
	URL       string   // remote fetch URL (empty for local files)
	LocalPath string   // absolute local path (empty for remote files)
	SHA       string   // git blob SHA when known
	Size      int64    // size when reported by the source, otherwise 0
}

// File is a candidate plus its exact unfiltered bytes.
type File struct {
	Candidate
	Data []byte
}

// Fetcher is the subset of netfetch.Client used by Resolver. Tests can
// substitute a fake without opening sockets.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string, headers http.Header) (*netfetch.Response, error)
}

// Options configures a Resolver.
type Options struct {
	// Root is the project directory used to resolve local paths. Empty means
	// the current working directory.
	Root string

	// Token is an optional GitHub token for private repositories and higher
	// rate limits. It is only sent to GitHub hosts.
	Token string

	// Fetcher overrides the HTTP fetcher. Nil uses netfetch.New.
	Fetcher Fetcher

	// APIBaseURL overrides https://api.github.com (GitHub Enterprise or tests).
	APIBaseURL string

	// UserAgent overrides the fetcher's default user agent.
	UserAgent string
}

// Resolver resolves and fetches skill/MCP sources.
type Resolver struct {
	fetcher    Fetcher
	token      string
	root       string
	apiBase    string
	tokenHosts map[string]struct{}
}

// New returns a Resolver for the supplied options.
func New(opts Options) *Resolver {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	apiBase := strings.TrimRight(strings.TrimSpace(opts.APIBaseURL), "/")
	if apiBase == "" {
		apiBase = defaultAPIBase
	}

	fetcher := opts.Fetcher
	if fetcher == nil {
		fetcher = netfetch.New(netfetch.Options{UserAgent: opts.UserAgent})
	}

	tokenHosts := map[string]struct{}{
		"github.com":                    {},
		"www.github.com":                {},
		"api.github.com":                {},
		"raw.githubusercontent.com":     {},
		"gist.githubusercontent.com":    {},
		"objects.githubusercontent.com": {},
	}
	if parsed, err := url.Parse(apiBase); err == nil {
		if host := normalizeHost(parsed.Hostname()); host != "" {
			tokenHosts[host] = struct{}{}
		}
	}

	return &Resolver{
		fetcher:    fetcher,
		token:      strings.TrimSpace(opts.Token),
		root:       root,
		apiBase:    apiBase,
		tokenHosts: tokenHosts,
	}
}

// Resolve parses input, resolves a default branch when needed, and discovers
// deterministic candidates. It does not download file bodies.
func Resolve(ctx context.Context, input string, opts Options) (*Source, error) {
	return New(opts).Resolve(ctx, input)
}

// FetchAll resolves input and downloads every candidate in deterministic
// order. Each File.Data is the exact source bytes.
func FetchAll(ctx context.Context, input string, opts Options) ([]File, error) {
	source, err := Resolve(ctx, input, opts)
	if err != nil {
		return nil, err
	}
	return source.FetchAll(ctx)
}

// Source is a resolved input. Candidates are sorted by path.
type Source struct {
	Input      string
	Kind       SpecKind
	Owner      string
	Repo       string
	Ref        string // resolved branch, tag or commit for GitHub sources
	Path       string // requested repository path/prefix
	LocalPath  string // local root for SpecLocal
	Candidates []Candidate

	resolver *Resolver
}

// Resolve parses input and discovers candidates.
func (r *Resolver) Resolve(ctx context.Context, input string) (*Source, error) {
	if ctx == nil {
		return nil, errors.New("remotesource: nil context")
	}
	spec, err := parseInput(input, r.root)
	if err != nil {
		return nil, err
	}

	switch spec.Kind {
	case SpecLocal:
		return r.resolveLocal(spec)
	case SpecRawURL:
		return r.resolveRawURL(spec)
	case SpecGitHubBlob:
		return r.resolveGitHubBlob(spec)
	case SpecGitHubRepo:
		return r.resolveGitHubRepo(ctx, spec)
	default:
		return nil, fmt.Errorf("%w: unknown source kind %q", ErrInvalidSource, spec.Kind)
	}
}

// Fetch downloads one candidate and returns its exact bytes.
func (r *Resolver) Fetch(ctx context.Context, candidate Candidate) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("remotesource: nil context")
	}
	if candidate.LocalPath != "" {
		data, err := os.ReadFile(candidate.LocalPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, candidate.Path)
			}
			return nil, fmt.Errorf("remotesource: read %s: %w", candidate.Path, err)
		}
		return data, nil
	}
	if strings.TrimSpace(candidate.URL) == "" {
		return nil, fmt.Errorf("%w: candidate %s has no fetch URL", ErrInvalidSource, candidate.Path)
	}
	if r.fetcher == nil {
		return nil, errors.New("remotesource: fetcher is not initialized")
	}

	headers := make(http.Header)
	if r.isGitHubAPIURL(candidate.URL) {
		headers.Set("Accept", githubAcceptRaw)
		headers.Set("X-GitHub-Api-Version", githubAPIVersion)
	}
	if r.shouldSendToken(candidate.URL) {
		netfetch.SetGitHubAuthHeader(headers, r.token)
	}
	response, err := r.fetcher.Fetch(ctx, candidate.URL, headers)
	if err != nil {
		return nil, r.classifyError(err, "fetch "+candidate.Path)
	}
	return response.Body, nil
}

// FetchAll downloads every candidate in order.
func (r *Resolver) FetchAll(ctx context.Context, source *Source) ([]File, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: source is nil", ErrInvalidSource)
	}
	files := make([]File, 0, len(source.Candidates))
	for _, candidate := range source.Candidates {
		data, err := r.Fetch(ctx, candidate)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Candidate: candidate, Data: data})
	}
	return files, nil
}

// FetchAll downloads every candidate from this source.
func (s *Source) FetchAll(ctx context.Context) ([]File, error) {
	if s == nil || s.resolver == nil {
		return nil, errors.New("remotesource: source was not created by a Resolver")
	}
	return s.resolver.FetchAll(ctx, s)
}

// Fetch downloads one candidate from this source.
func (s *Source) Fetch(ctx context.Context, candidate Candidate) ([]byte, error) {
	if s == nil || s.resolver == nil {
		return nil, errors.New("remotesource: source was not created by a Resolver")
	}
	return s.resolver.Fetch(ctx, candidate)
}

func (r *Resolver) resolveLocal(spec Spec) (*Source, error) {
	info, err := os.Stat(spec.LocalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: local path %s", ErrNotFound, spec.LocalPath)
	}
	if err != nil {
		return nil, fmt.Errorf("remotesource: inspect local path %s: %w", spec.LocalPath, err)
	}

	source := &Source{
		Input:     spec.Input,
		Kind:      SpecLocal,
		LocalPath: spec.LocalPath,
		resolver:  r,
	}

	if info.Mode().IsRegular() {
		kind, _ := ClassifyPath(spec.LocalPath)
		source.Candidates = []Candidate{{
			Path:      r.displayPath(spec.LocalPath),
			Kind:      kind,
			LocalPath: spec.LocalPath,
			Size:      info.Size(),
		}}
		return source, nil
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: local path is not a file or directory: %s", ErrInvalidSource, spec.LocalPath)
	}

	candidates, err := r.discoverLocal(spec.LocalPath)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: local path %s", ErrNoCandidates, spec.LocalPath)
	}
	source.Candidates = candidates
	return source, nil
}

func (r *Resolver) discoverLocal(root string) ([]Candidate, error) {
	var candidates []Candidate
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if entry.IsDir() {
			if skipDirName(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		kind, ok := ClassifyPath(filePath)
		if !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("remotesource: inspect %s: %w", filePath, err)
		}
		candidates = append(candidates, Candidate{
			Path:      r.displayPath(filePath),
			Kind:      kind,
			LocalPath: filePath,
			Size:      info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("remotesource: discover local path %s: %w", root, err)
	}
	sortCandidates(candidates)
	return candidates, nil
}

func (r *Resolver) resolveRawURL(spec Spec) (*Source, error) {
	parsed, err := url.Parse(spec.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	kind, _ := ClassifyPath(name)
	return &Source{
		Input: spec.Input,
		Kind:  SpecRawURL,
		Owner: spec.Owner,
		Repo:  spec.Repo,
		Ref:   spec.Ref,
		Path:  spec.Path,
		Candidates: []Candidate{{
			Path: name,
			Kind: kind,
			URL:  spec.URL,
		}},
		resolver: r,
	}, nil
}

func (r *Resolver) resolveGitHubBlob(spec Spec) (*Source, error) {
	if spec.Path == "" {
		return nil, fmt.Errorf("%w: GitHub file path is empty", ErrInvalidSource)
	}
	kind, _ := ClassifyPath(spec.Path)
	return &Source{
		Input: spec.Input,
		Kind:  SpecGitHubBlob,
		Owner: spec.Owner,
		Repo:  spec.Repo,
		Ref:   spec.Ref,
		Path:  spec.Path,
		Candidates: []Candidate{{
			Path: spec.Path,
			Kind: kind,
			URL:  r.contentsURL(spec.Owner, spec.Repo, spec.Ref, spec.Path),
		}},
		resolver: r,
	}, nil
}

func (r *Resolver) resolveGitHubRepo(ctx context.Context, spec Spec) (*Source, error) {
	ref := strings.TrimSpace(spec.Ref)
	if ref == "" {
		defaultBranch, err := r.defaultBranch(ctx, spec.Owner, spec.Repo)
		if err != nil {
			return nil, err
		}
		ref = defaultBranch
	}

	source := &Source{
		Input:    spec.Input,
		Kind:     SpecGitHubRepo,
		Owner:    spec.Owner,
		Repo:     spec.Repo,
		Ref:      ref,
		Path:     strings.Trim(spec.Path, "/"),
		resolver: r,
	}

	// A tree URL may point directly at a recognized file. Avoid a tree walk in
	// that case; the fetch itself reports a missing or private file.
	if source.Path != "" {
		if kind, ok := ClassifyPath(source.Path); ok {
			source.Candidates = []Candidate{{
				Path: source.Path,
				Kind: kind,
				URL:  r.contentsURL(spec.Owner, spec.Repo, ref, source.Path),
			}}
			return source, nil
		}
	}

	candidates, err := r.discoverGitHub(ctx, spec.Owner, spec.Repo, ref, source.Path)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: repository %s/%s", ErrNoCandidates, spec.Owner, spec.Repo)
	}
	source.Candidates = candidates
	return source, nil
}

func (r *Resolver) displayPath(filePath string) string {
	relative, err := filepath.Rel(r.root, filePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		relative = filepath.Base(filePath)
	}
	return filepath.ToSlash(relative)
}

func (r *Resolver) shouldSendToken(rawURL string) bool {
	if r.token == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := normalizeHost(parsed.Hostname())
	if host == "" {
		return false
	}
	_, ok := r.tokenHosts[host]
	return ok
}

// isGitHubAPIURL reports whether rawURL targets the configured API base.
// Only these URLs receive the raw media-type Accept header; direct raw URLs
// must keep their own semantics.
func (r *Resolver) isGitHubAPIURL(rawURL string) bool {
	return strings.HasPrefix(rawURL, strings.TrimRight(r.apiBase, "/")+"/")
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

func (r *Resolver) classifyError(err error, what string) error {
	var status *netfetch.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: check GITHUB_TOKEN or GH_TOKEN", ErrUnauthorized)
		case http.StatusForbidden:
			if isRateLimited(status) {
				return newRateLimitError(status)
			}
			return fmt.Errorf("%w: %s", ErrForbidden, what)
		case http.StatusNotFound:
			if r.token == "" {
				return fmt.Errorf("%w: %s (the repository or file may be private; set GITHUB_TOKEN or GH_TOKEN)", ErrNotFound, what)
			}
			return fmt.Errorf("%w: %s", ErrNotFound, what)
		case http.StatusTooManyRequests:
			return newRateLimitError(status)
		default:
			return fmt.Errorf("remotesource: %s: %w", what, err)
		}
	}
	return fmt.Errorf("remotesource: %s: %w", what, err)
}

// RateLimitError reports GitHub rate-limit exhaustion and, when known, when
// requests may resume. It never contains credentials.
type RateLimitError struct {
	Reset      time.Time
	RetryAfter time.Duration
}

func newRateLimitError(status *netfetch.StatusError) *RateLimitError {
	out := &RateLimitError{}
	if status == nil {
		return out
	}
	if retry := strings.TrimSpace(status.Header.Get("Retry-After")); retry != "" {
		if seconds, err := strconv.Atoi(retry); err == nil && seconds >= 0 {
			out.RetryAfter = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(retry); err == nil {
			out.RetryAfter = time.Until(when)
			if out.RetryAfter < 0 {
				out.RetryAfter = 0
			}
		}
	}
	if reset := strings.TrimSpace(status.Header.Get("X-RateLimit-Reset")); reset != "" {
		if seconds, err := strconv.ParseInt(reset, 10, 64); err == nil {
			out.Reset = time.Unix(seconds, 0).UTC()
		}
	}
	return out
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return ErrRateLimited.Error()
	}
	if !e.Reset.IsZero() {
		return fmt.Sprintf("%s (resets at %s)", ErrRateLimited, e.Reset.Format(time.RFC3339))
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry after %s)", ErrRateLimited, e.RetryAfter.Round(time.Second))
	}
	return ErrRateLimited.Error()
}

// Unwrap lets errors.Is(err, ErrRateLimited) work for typed errors.
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

func isRateLimited(status *netfetch.StatusError) bool {
	if status == nil {
		return false
	}
	if status.Code == http.StatusTooManyRequests {
		return true
	}
	if strings.TrimSpace(status.Header.Get("Retry-After")) != "" {
		return true
	}
	return strings.TrimSpace(status.Header.Get("X-RateLimit-Remaining")) == "0"
}

// ClassifyPath reports whether a path is a skill or MCP candidate. Paths may
// use either slash style.
func ClassifyPath(filePath string) (FileKind, bool) {
	normalized := strings.ReplaceAll(filePath, `\`, "/")
	base := path.Base(strings.TrimRight(normalized, "/"))
	if base == "." || base == "/" || base == "" {
		return KindOther, false
	}
	if base == "SKILL.md" {
		return KindSkill, true
	}
	switch base {
	case "mcp.json", ".mcp.json", "mcp-config.json", "mcp_config.json",
		"mcp-servers.json", "mcp_servers.json":
		return KindMCP, true
	}
	if strings.HasSuffix(base, ".mcp.json") {
		return KindMCP, true
	}
	return KindOther, false
}

func skipDirName(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Path != candidates[j].Path {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Kind < candidates[j].Kind
	})
}
