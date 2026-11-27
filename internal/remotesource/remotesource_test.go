package remotesource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"GoWork/internal/netfetch"
)

type fakeCall struct {
	URL    string
	Header http.Header
}

type fakeFetcher struct {
	mu      sync.Mutex
	calls   []fakeCall
	handler func(context.Context, string, http.Header) (*netfetch.Response, error)
}

func newFakeFetcher(handler func(context.Context, string, http.Header) (*netfetch.Response, error)) *fakeFetcher {
	return &fakeFetcher{handler: handler}
}

func (f *fakeFetcher) Fetch(ctx context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{URL: rawURL, Header: headers.Clone()})
	f.mu.Unlock()
	if f.handler == nil {
		return nil, fmt.Errorf("unexpected fetch: %s", rawURL)
	}
	return f.handler(ctx, rawURL, headers)
}

func (f *fakeFetcher) snapshot() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func jsonResponse(rawURL, body string) (*netfetch.Response, error) {
	return &netfetch.Response{
		URL:        rawURL,
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(body),
	}, nil
}

func rawResponse(rawURL, body string) (*netfetch.Response, error) {
	return &netfetch.Response{
		URL:        rawURL,
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/octet-stream"}},
		Body:       []byte(body),
	}, nil
}

func statusResponse(code int, header http.Header) (*netfetch.Response, error) {
	if header == nil {
		header = make(http.Header)
	}
	return nil, &netfetch.StatusError{Code: code, Status: http.StatusText(code), Header: header}
}

func TestParseSources(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		kind    SpecKind
		owner   string
		repo    string
		ref     string
		path    string
		url     string
		local   string
		wantErr error
	}{
		{
			name: "raw URL", input: "https://example.com/files/SKILL.md",
			kind: SpecRawURL, url: "https://example.com/files/SKILL.md",
		},
		{
			name: "raw GitHub URL", input: "https://raw.githubusercontent.com/acme/skills/main/skills/demo/SKILL.md",
			kind: SpecRawURL, owner: "acme", repo: "skills", ref: "main", path: "skills/demo/SKILL.md",
			url: "https://raw.githubusercontent.com/acme/skills/main/skills/demo/SKILL.md",
		},
		{
			name: "GitHub repository", input: "https://github.com/acme/skills",
			kind: SpecGitHubRepo, owner: "acme", repo: "skills",
		},
		{
			name: "GitHub repository dot git", input: "https://github.com/acme/skills.git",
			kind: SpecGitHubRepo, owner: "acme", repo: "skills",
		},
		{
			name: "GitHub blob", input: "https://github.com/acme/skills/blob/main/skills/demo/SKILL.md",
			kind: SpecGitHubBlob, owner: "acme", repo: "skills", ref: "main", path: "skills/demo/SKILL.md",
		},
		{
			name: "GitHub raw path", input: "https://github.com/acme/skills/raw/main/mcp.json",
			kind: SpecGitHubBlob, owner: "acme", repo: "skills", ref: "main", path: "mcp.json",
		},
		{
			name: "GitHub tree with subdirectory", input: "https://github.com/acme/skills/tree/main/skills",
			kind: SpecGitHubRepo, owner: "acme", repo: "skills", ref: "main", path: "skills",
		},
		{
			name: "GitHub tree root", input: "https://github.com/acme/skills/tree/dev",
			kind: SpecGitHubRepo, owner: "acme", repo: "skills", ref: "dev",
		},
		{
			name: "owner repo shorthand", input: "acme/skills",
			kind: SpecGitHubRepo, owner: "acme", repo: "skills",
		},
		{
			name: "owner repo ref shorthand", input: "acme/skills@feature",
			kind: SpecGitHubRepo, owner: "acme", repo: "skills", ref: "feature",
		},
		{
			name: "dot relative local", input: "./skills/demo",
			kind: SpecLocal, local: filepath.Clean("./skills/demo"),
		},
		{
			name: "nested local path", input: "skills/demo/SKILL.md",
			kind: SpecLocal, local: filepath.Clean("skills/demo/SKILL.md"),
		},
		{
			name: "single local file", input: "SKILL.md",
			kind: SpecLocal, local: "SKILL.md",
		},
		{
			name: "unsupported scheme", input: "ftp://example.com/file",
			wantErr: ErrInvalidSource,
		},
		{
			name: "embedded credentials", input: "https://user:pass@example.com/file",
			wantErr: ErrInvalidSource,
		},
		{
			name: "GitHub owner only", input: "https://github.com/acme",
			wantErr: ErrInvalidSource,
		},
		{
			name: "GitHub unsupported subpage", input: "https://github.com/acme/skills/issues",
			wantErr: ErrInvalidSource,
		},
		{
			name: "empty", input: "  ",
			wantErr: ErrInvalidSource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := Parse(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Parse(%q) error = %v, want %v", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}
			if spec.Kind != tt.kind {
				t.Fatalf("Kind = %q, want %q", spec.Kind, tt.kind)
			}
			if spec.Owner != tt.owner || spec.Repo != tt.repo || spec.Ref != tt.ref || spec.Path != tt.path {
				t.Fatalf("Spec = %+v, want owner=%q repo=%q ref=%q path=%q", spec, tt.owner, tt.repo, tt.ref, tt.path)
			}
			if tt.url != "" && spec.URL != tt.url {
				t.Fatalf("URL = %q, want %q", spec.URL, tt.url)
			}
			if tt.local != "" && spec.LocalPath != tt.local {
				t.Fatalf("LocalPath = %q, want %q", spec.LocalPath, tt.local)
			}
		})
	}
}

func TestResolveRawURLReturnsExactBytesWithoutTokenLeak(t *testing.T) {
	body := "line one\r\nline two\x00no trailing newline"
	fetcher := newFakeFetcher(func(_ context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
		if got := headers.Get("Authorization"); got != "" {
			t.Fatalf("token leaked to non-GitHub host: %q", got)
		}
		return rawResponse(rawURL, body)
	})
	resolver := New(Options{Token: "ghp_secret", Fetcher: fetcher})

	source, err := resolver.Resolve(context.Background(), "https://example.com/files/SKILL.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source.Kind != SpecRawURL {
		t.Fatalf("Kind = %q, want raw URL", source.Kind)
	}
	if len(source.Candidates) != 1 {
		t.Fatalf("candidates = %+v, want one", source.Candidates)
	}
	if source.Candidates[0].Path != "SKILL.md" || source.Candidates[0].Kind != KindSkill {
		t.Fatalf("candidate = %+v, want SKILL.md skill", source.Candidates[0])
	}

	files, err := source.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(files) != 1 || !bytes.Equal(files[0].Data, []byte(body)) {
		t.Fatalf("files = %+v, want exact body %q", files, body)
	}
	for _, call := range fetcher.snapshot() {
		if strings.Contains(call.Header.Get("Authorization"), "ghp_secret") {
			t.Fatal("fake recorded token for a non-GitHub request")
		}
	}
}

func TestResolveGitHubBlobUsesTokenAndRawMediaType(t *testing.T) {
	content := "exact\r\nbytes"
	wantURL := "https://api.test/repos/acme/skills/contents/skills/demo/SKILL.md?ref=main"
	fetcher := newFakeFetcher(func(_ context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
		if rawURL != wantURL {
			t.Fatalf("fetch URL = %q, want %q", rawURL, wantURL)
		}
		if got := headers.Get("Authorization"); got != "Bearer ghp_secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := headers.Get("Accept"); got != githubAcceptRaw {
			t.Fatalf("Accept = %q, want %q", got, githubAcceptRaw)
		}
		return rawResponse(rawURL, content)
	})
	resolver := New(Options{
		Fetcher:    fetcher,
		Token:      "ghp_secret",
		APIBaseURL: "https://api.test",
	})

	source, err := resolver.Resolve(context.Background(), "https://github.com/acme/skills/blob/main/skills/demo/SKILL.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source.Ref != "main" || len(source.Candidates) != 1 {
		t.Fatalf("source = %+v", source)
	}
	if source.Candidates[0].URL != wantURL {
		t.Fatalf("candidate URL = %q, want %q", source.Candidates[0].URL, wantURL)
	}
	files, err := source.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(files) != 1 || string(files[0].Data) != content {
		t.Fatalf("files = %+v, want exact content", files)
	}
}

func TestResolveGitHubRepoDiscoversDeterministicCandidates(t *testing.T) {
	tree := `{
		"truncated": false,
		"tree": [
			{"path":"README.md","type":"blob","sha":"readme"},
			{"path":"skills/z/SKILL.md","type":"blob","sha":"z","size":11},
			{"path":"mcp/mcp.json","type":"blob","sha":"mcp","size":12},
			{"path":"skills/a/SKILL.md","type":"blob","sha":"a","size":13},
			{"path":"docs/SKILL.md","type":"blob","sha":"docs","size":14},
			{"path":"skills/a/SKILL.md","type":"blob","sha":"a","size":13},
			{"path":"skills/config.mcp.json","type":"blob","sha":"cfg","size":15},
			{"path":"node_modules/pkg/SKILL.md","type":"blob","sha":"ignore"},
			{"path":"vendor/pkg/mcp.json","type":"blob","sha":"ignore2"},
			{"path":"skills","type":"tree","sha":"tree-skills"}
		]
	}`
	fetcher := newFakeFetcher(func(_ context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
		switch rawURL {
		case "https://api.test/repos/acme/skills":
			if got := headers.Get("Accept"); got != githubAcceptJSON {
				t.Fatalf("metadata Accept = %q", got)
			}
			if got := headers.Get("Authorization"); got != "Bearer ghp_secret" {
				t.Fatalf("metadata Authorization = %q", got)
			}
			return jsonResponse(rawURL, `{"default_branch":"main"}`)
		case "https://api.test/repos/acme/skills/git/trees/main?recursive=1":
			if got := headers.Get("Authorization"); got != "Bearer ghp_secret" {
				t.Fatalf("tree Authorization = %q", got)
			}
			return jsonResponse(rawURL, tree)
		}
		if strings.Contains(rawURL, "/contents/") {
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse content URL: %v", err)
			}
			contentPath := strings.TrimPrefix(parsed.Path, "/repos/acme/skills/contents/")
			if parsed.Query().Get("ref") != "main" {
				t.Fatalf("content ref = %q, want main", parsed.Query().Get("ref"))
			}
			if got := headers.Get("Accept"); got != githubAcceptRaw {
				t.Fatalf("content Accept = %q", got)
			}
			if got := headers.Get("Authorization"); got != "Bearer ghp_secret" {
				t.Fatalf("content Authorization = %q", got)
			}
			return rawResponse(rawURL, "body:"+contentPath+"\r\n")
		}
		t.Fatalf("unexpected fetch URL %q", rawURL)
		return nil, nil
	})
	resolver := New(Options{
		Fetcher:    fetcher,
		Token:      "ghp_secret",
		APIBaseURL: "https://api.test",
	})

	source, err := resolver.Resolve(context.Background(), "acme/skills")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source.Ref != "main" {
		t.Fatalf("Ref = %q, want main", source.Ref)
	}
	wantPaths := []string{
		"docs/SKILL.md",
		"mcp/mcp.json",
		"skills/a/SKILL.md",
		"skills/config.mcp.json",
		"skills/z/SKILL.md",
	}
	gotPaths := make([]string, len(source.Candidates))
	for i, candidate := range source.Candidates {
		gotPaths[i] = candidate.Path
	}
	if strings.Join(gotPaths, "\n") != strings.Join(wantPaths, "\n") {
		t.Fatalf("candidates = %v, want %v", gotPaths, wantPaths)
	}
	if source.Candidates[0].Kind != KindSkill || source.Candidates[1].Kind != KindMCP {
		t.Fatalf("candidate kinds = %q/%q", source.Candidates[0].Kind, source.Candidates[1].Kind)
	}
	if source.Candidates[3].SHA != "cfg" || source.Candidates[3].Size != 15 {
		t.Fatalf("provenance = %+v", source.Candidates[3])
	}

	files, err := source.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(files) != len(wantPaths) {
		t.Fatalf("files = %d, want %d", len(files), len(wantPaths))
	}
	for _, file := range files {
		want := "body:" + file.Path + "\r\n"
		if string(file.Data) != want {
			t.Fatalf("file %s data = %q, want %q", file.Path, file.Data, want)
		}
	}
}

func TestResolveGitHubRepoTracksSubdirectory(t *testing.T) {
	tree := `{"truncated":false,"tree":[
		{"path":"skills/a/SKILL.md","type":"blob","sha":"a"},
		{"path":"docs/SKILL.md","type":"blob","sha":"docs"}
	]}`
	fetcher := newFakeFetcher(func(_ context.Context, rawURL string, _ http.Header) (*netfetch.Response, error) {
		if rawURL != "https://api.test/repos/acme/skills/git/trees/main?recursive=1" {
			t.Fatalf("unexpected URL %q", rawURL)
		}
		return jsonResponse(rawURL, tree)
	})
	resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test"})

	source, err := resolver.Resolve(context.Background(), "https://github.com/acme/skills/tree/main/skills")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(source.Candidates) != 1 || source.Candidates[0].Path != "skills/a/SKILL.md" {
		t.Fatalf("candidates = %+v, want only skills/a/SKILL.md", source.Candidates)
	}
}

func TestResolveGitHubTreeFilePathSkipsDiscovery(t *testing.T) {
	fetcher := newFakeFetcher(func(context.Context, string, http.Header) (*netfetch.Response, error) {
		t.Fatal("Resolve should not call GitHub for a direct file path with an explicit ref")
		return nil, nil
	})
	resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test"})

	source, err := resolver.Resolve(context.Background(), "https://github.com/acme/skills/tree/main/skills/demo/SKILL.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(source.Candidates) != 1 || source.Candidates[0].Kind != KindSkill {
		t.Fatalf("candidates = %+v", source.Candidates)
	}
	if !strings.Contains(source.Candidates[0].URL, "/contents/skills/demo/SKILL.md?ref=main") {
		t.Fatalf("candidate URL = %q", source.Candidates[0].URL)
	}
}

func TestResolveGitHubTruncatedTreeFallsBackToDirectoryWalk(t *testing.T) {
	fetcher := newFakeFetcher(func(_ context.Context, rawURL string, _ http.Header) (*netfetch.Response, error) {
		switch rawURL {
		case "https://api.test/repos/acme/skills":
			return jsonResponse(rawURL, `{"default_branch":"main"}`)
		case "https://api.test/repos/acme/skills/git/trees/main?recursive=1":
			return jsonResponse(rawURL, `{"truncated":true,"tree":[
				{"path":"partial/SKILL.md","type":"blob","sha":"partial"}
			]}`)
		case "https://api.test/repos/acme/skills/git/trees/main":
			return jsonResponse(rawURL, `{"tree":[
				{"path":"skills","type":"tree","sha":"d1"},
				{"path":"other","type":"tree","sha":"d2"},
				{"path":"top/SKILL.md","type":"blob","sha":"top"}
			]}`)
		case "https://api.test/repos/acme/skills/git/trees/d1":
			return jsonResponse(rawURL, `{"tree":[
				{"path":"demo/SKILL.md","type":"blob","sha":"demo"},
				{"path":"deep","type":"tree","sha":"d3"}
			]}`)
		case "https://api.test/repos/acme/skills/git/trees/d2":
			return jsonResponse(rawURL, `{"tree":[
				{"path":"mcp.json","type":"blob","sha":"mcp"}
			]}`)
		case "https://api.test/repos/acme/skills/git/trees/d3":
			return jsonResponse(rawURL, `{"tree":[
				{"path":"SKILL.md","type":"blob","sha":"deep"}
			]}`)
		}
		t.Fatalf("unexpected URL %q", rawURL)
		return nil, nil
	})
	resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test"})

	source, err := resolver.Resolve(context.Background(), "https://github.com/acme/skills")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{
		"other/mcp.json",
		"skills/deep/SKILL.md",
		"skills/demo/SKILL.md",
		"top/SKILL.md",
	}
	got := make([]string, len(source.Candidates))
	for i, candidate := range source.Candidates {
		got[i] = candidate.Path
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveGitHubTruncatedFallbackStillTruncated(t *testing.T) {
	fetcher := newFakeFetcher(func(_ context.Context, rawURL string, _ http.Header) (*netfetch.Response, error) {
		switch rawURL {
		case "https://api.test/repos/acme/skills":
			return jsonResponse(rawURL, `{"default_branch":"main"}`)
		case "https://api.test/repos/acme/skills/git/trees/main?recursive=1",
			"https://api.test/repos/acme/skills/git/trees/main":
			return jsonResponse(rawURL, `{"truncated":true,"tree":[]}`)
		}
		t.Fatalf("unexpected URL %q", rawURL)
		return nil, nil
	})
	resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test"})

	_, err := resolver.Resolve(context.Background(), "acme/skills")
	if !errors.Is(err, ErrTreeTruncated) {
		t.Fatalf("error = %v, want ErrTreeTruncated", err)
	}
}

func TestResolveGitHubRateLimitAndAuthErrors(t *testing.T) {
	t.Run("rate limit", func(t *testing.T) {
		reset := time.Now().Add(time.Hour).Unix()
		fetcher := newFakeFetcher(func(_ context.Context, rawURL string, _ http.Header) (*netfetch.Response, error) {
			header := make(http.Header)
			header.Set("X-RateLimit-Remaining", "0")
			header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", reset))
			return statusResponse(http.StatusForbidden, header)
		})
		resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test", Token: "ghp_secret"})

		_, err := resolver.Resolve(context.Background(), "acme/skills")
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("error = %v, want ErrRateLimited", err)
		}
		var rate *RateLimitError
		if !errors.As(err, &rate) {
			t.Fatalf("error type = %T, want *RateLimitError", err)
		}
		if rate.Reset.Unix() != reset {
			t.Fatalf("reset = %v, want %v", rate.Reset.Unix(), reset)
		}
		if strings.Contains(err.Error(), "ghp_secret") {
			t.Fatalf("error leaked token: %q", err)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		fetcher := newFakeFetcher(func(_ context.Context, _ string, _ http.Header) (*netfetch.Response, error) {
			return statusResponse(http.StatusUnauthorized, nil)
		})
		resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test", Token: "ghp_secret"})
		_, err := resolver.Resolve(context.Background(), "acme/skills")
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("error = %v, want ErrUnauthorized", err)
		}
		if strings.Contains(err.Error(), "ghp_secret") {
			t.Fatalf("error leaked token: %q", err)
		}
	})

	t.Run("not found without token suggests private", func(t *testing.T) {
		fetcher := newFakeFetcher(func(_ context.Context, _ string, _ http.Header) (*netfetch.Response, error) {
			return statusResponse(http.StatusNotFound, nil)
		})
		resolver := New(Options{Fetcher: fetcher, APIBaseURL: "https://api.test"})
		_, err := resolver.Resolve(context.Background(), "acme/skills")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
		message := err.Error()
		if !strings.Contains(message, "private") || !strings.Contains(message, "GITHUB_TOKEN") {
			t.Fatalf("not-found error does not explain private repositories: %q", message)
		}
	})
}

func TestResolveLocalPathAndEscapeProtection(t *testing.T) {
	root := t.TempDir()
	skillBody := "local skill\r\nbytes"
	writeFile(t, filepath.Join(root, "skills", "demo", "SKILL.md"), skillBody)
	writeFile(t, filepath.Join(root, "mcp", "mcp.json"), `{"mcpServers":{}}`)
	writeFile(t, filepath.Join(root, "node_modules", "x", "SKILL.md"), "ignored")

	resolver := New(Options{Root: root})

	source, err := resolver.Resolve(context.Background(), "skills/demo")
	if err != nil {
		t.Fatalf("Resolve local directory: %v", err)
	}
	if source.Kind != SpecLocal {
		t.Fatalf("Kind = %q, want local", source.Kind)
	}
	if len(source.Candidates) != 1 || source.Candidates[0].Path != "skills/demo/SKILL.md" {
		t.Fatalf("directory candidates = %+v", source.Candidates)
	}
	files, err := source.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(files) != 1 || string(files[0].Data) != skillBody {
		t.Fatalf("files = %+v, want exact local bytes", files)
	}

	// A project-relative directory discovers both skills and MCP JSON.
	source, err = resolver.Resolve(context.Background(), ".")
	if err != nil {
		t.Fatalf("Resolve project root: %v", err)
	}
	want := []string{"mcp/mcp.json", "skills/demo/SKILL.md"}
	got := make([]string, len(source.Candidates))
	for i, candidate := range source.Candidates {
		got[i] = candidate.Path
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("project candidates = %v, want %v", got, want)
	}

	if _, err := resolver.Resolve(context.Background(), "../outside"); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("escape error = %v, want ErrInvalidSource", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, filepath.Join(outside, "SKILL.md"), "outside")
	if _, err := resolver.Resolve(context.Background(), outside); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("absolute escape error = %v, want ErrInvalidSource", err)
	}
}

func TestResolveLocalDirectoryWithoutCandidates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "nothing here")
	resolver := New(Options{Root: root})
	_, err := resolver.Resolve(context.Background(), ".")
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("error = %v, want ErrNoCandidates", err)
	}
}

func TestClassifyPath(t *testing.T) {
	tests := []struct {
		path string
		kind FileKind
		ok   bool
	}{
		{"skills/demo/SKILL.md", KindSkill, true},
		{"SKILL.md", KindSkill, true},
		{`skills\demo\SKILL.md`, KindSkill, true},
		{"mcp.json", KindMCP, true},
		{".mcp.json", KindMCP, true},
		{"mcp-config.json", KindMCP, true},
		{"mcp_config.json", KindMCP, true},
		{"mcp-servers.json", KindMCP, true},
		{"acme.mcp.json", KindMCP, true},
		{"README.md", KindOther, false},
		{"skill.md", KindOther, false},
		{"other.json", KindOther, false},
	}
	for _, tt := range tests {
		kind, ok := ClassifyPath(tt.path)
		if kind != tt.kind || ok != tt.ok {
			t.Errorf("ClassifyPath(%q) = (%q, %v), want (%q, %v)", tt.path, kind, ok, tt.kind, tt.ok)
		}
	}
}

func TestCandidatesAreSorted(t *testing.T) {
	candidates := []Candidate{{Path: "z"}, {Path: "a"}, {Path: "m"}}
	sortCandidates(candidates)
	got := []string{candidates[0].Path, candidates[1].Path, candidates[2].Path}
	want := []string{"a", "m", "z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sorted = %v, want %v", got, want)
	}
	if !sort.SliceIsSorted(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path }) {
		t.Fatal("candidates are not sorted")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
