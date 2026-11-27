package skillstool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"GoWork/internal/appdir"
	"GoWork/internal/netfetch"
	"GoWork/providers"
	"GoWork/tools"
)

func skillMarkdown(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body
}

func useTestManager(t *testing.T, mgr *Manager) {
	t.Helper()
	prev := GetManager()
	SetManager(mgr)
	t.Cleanup(func() { SetManager(prev) })
}

func dispatchArgs(root string) tools.DispatchArgs {
	return tools.DispatchArgs{RootPath: &root}
}

func runTool(t *testing.T, tool tools.AgentTool, args tools.DispatchArgs, input any) tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Run(context.Background(), args, raw)
	if err != nil {
		t.Fatalf("%s returned error: %v", tool.Name(), err)
	}
	return result
}

// httpTestFetcher adapts a test HTTP client to remotesource.Fetcher. The
// production netfetch guard intentionally blocks loopback addresses, so
// httptest servers are reached through this narrow test-only seam.
type httpTestFetcher struct {
	client *http.Client
	mu     sync.Mutex
	calls  []string
}

func (f *httpTestFetcher) Fetch(ctx context.Context, rawURL string, headers http.Header) (*netfetch.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, rawURL)
	f.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &netfetch.StatusError{Code: resp.StatusCode, Status: resp.Status, Header: resp.Header.Clone()}
	}
	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return &netfetch.Response{
		URL:        finalURL,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func TestInstallRawURLAndLoadExactBody(t *testing.T) {
	content := "---\nname: remote-skill\ndescription: >-\n  Remote folded\n  description.\n---\n\nExact body line one\r\nline two without trailing newline"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/SKILL.md" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = io.WriteString(w, content)
	}))
	defer server.Close()

	mgr := NewManagerAt(t.TempDir())
	useTestManager(t, mgr)
	tool := NewInstallToolWithOptions(InstallToolOptions{Fetcher: &httpTestFetcher{client: server.Client()}})
	sourceURL := server.URL + "/SKILL.md?token=supersecret"
	result := runTool(t, tool, dispatchArgs(t.TempDir()), map[string]any{
		"source": sourceURL,
		"load":   true,
	})

	if result.IsError {
		t.Fatalf("install failed: %s", result.Content)
	}
	if strings.Contains(result.Content, "supersecret") {
		t.Fatalf("tool output leaked the source token: %s", result.Content)
	}

	path := filepath.Join(mgr.Dir(), "remote-skill", SkillFileName)
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	if string(onDisk) != content {
		t.Fatalf("installed bytes differ from source:\n got %q\nwant %q", onDisk, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat installed skill: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("installed mode = %04o, want 0644", perm)
	}

	if entries := mgr.Discover(); len(entries) != 1 || entries[0].Name != "remote-skill" {
		t.Fatalf("install did not re-discover the skill: %+v", entries)
	}
	if !mgr.IsLoaded("remote-skill") {
		t.Fatal("load=true did not load the installed skill")
	}
	description := ToolDescription()
	if !strings.Contains(description, "<name>remote-skill</name>") ||
		!strings.Contains(description, "Remote folded description.") {
		t.Fatalf("tool description did not pick up the installed skill:\n%s", description)
	}

	skillTool := New()
	loaded := runTool(t, skillTool, tools.DispatchArgs{}, map[string]string{"name": "remote-skill"})
	if loaded.IsError || loaded.Content != content {
		t.Fatalf("skill tool did not return the exact content: %+v", loaded)
	}

	provData, err := os.ReadFile(filepath.Join(mgr.Dir(), "remote-skill", ProvenanceFileName))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	var prov Provenance
	if err := json.Unmarshal(provData, &prov); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	wantSHA := hex.EncodeToString(sum[:])
	if prov.SHA256 != wantSHA {
		t.Errorf("provenance sha = %q, want %q", prov.SHA256, wantSHA)
	}
	if prov.Source != server.URL+"/SKILL.md" {
		t.Errorf("provenance source = %q, want sanitized URL without query", prov.Source)
	}
	if strings.Contains(string(provData), "supersecret") {
		t.Fatalf("provenance stored the query token: %s", provData)
	}
}

func TestInstallGitHubMultipleCandidatesRequirePath(t *testing.T) {
	alpha := skillMarkdown("alpha", "Alpha skill.", "alpha body")
	beta := skillMarkdown("beta", "Beta skill.", "beta body")

	var authMu sync.Mutex
	var authSeen []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		authSeen = append(authSeen, r.Header.Get("Authorization"))
		authMu.Unlock()
		switch {
		case r.URL.Path == "/repos/acme/skills":
			_, _ = io.WriteString(w, `{"default_branch":"main"}`)
		case r.URL.Path == "/repos/acme/skills/git/trees/main":
			_, _ = io.WriteString(w, `{"sha":"main","truncated":false,"tree":[`+
				`{"path":"skills/alpha/SKILL.md","mode":"100644","type":"blob","sha":"a","size":10},`+
				`{"path":"skills/beta/SKILL.md","mode":"100644","type":"blob","sha":"b","size":10}`+
				`]}`)
		case r.URL.Path == "/repos/acme/skills/contents/skills/alpha/SKILL.md":
			_, _ = io.WriteString(w, alpha)
		case r.URL.Path == "/repos/acme/skills/contents/skills/beta/SKILL.md":
			_, _ = io.WriteString(w, beta)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	mgr := NewManagerAt(t.TempDir())
	useTestManager(t, mgr)
	opts := InstallToolOptions{
		Fetcher:    &httpTestFetcher{client: server.Client()},
		APIBaseURL: server.URL,
		Token:      "ghp_verysecret",
	}
	tool := NewInstallToolWithOptions(opts)
	root := t.TempDir()

	ambiguous := runTool(t, tool, dispatchArgs(root), map[string]any{"source": "acme/skills"})
	if !ambiguous.IsError {
		t.Fatalf("multiple candidates were accepted without path: %s", ambiguous.Content)
	}
	if !strings.Contains(ambiguous.Content, "specify path") {
		t.Fatalf("ambiguity error should ask for path: %s", ambiguous.Content)
	}
	if strings.Contains(ambiguous.Content, "ghp_verysecret") {
		t.Fatalf("ambiguity error leaked the GitHub token: %s", ambiguous.Content)
	}
	if _, err := os.Stat(filepath.Join(mgr.Dir(), "alpha", SkillFileName)); !os.IsNotExist(err) {
		t.Fatal("ambiguous install wrote a skill despite no path")
	}

	installed := runTool(t, tool, dispatchArgs(root), map[string]any{
		"source": "acme/skills",
		"ref":    "main",
		"path":   "skills/beta/SKILL.md",
	})
	if installed.IsError {
		t.Fatalf("install with path failed: %s", installed.Content)
	}
	data, err := os.ReadFile(filepath.Join(mgr.Dir(), "beta", SkillFileName))
	if err != nil {
		t.Fatalf("read beta skill: %v", err)
	}
	if string(data) != beta {
		t.Fatalf("installed beta bytes = %q, want %q", data, beta)
	}
	var prov Provenance
	provData, err := os.ReadFile(filepath.Join(mgr.Dir(), "beta", ProvenanceFileName))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if err := json.Unmarshal(provData, &prov); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	if prov.Source != "acme/skills" || prov.Ref != "main" {
		t.Fatalf("provenance = %+v, want source acme/skills ref main", prov)
	}
	if strings.Contains(string(provData), "ghp_verysecret") {
		t.Fatal("provenance leaked the GitHub token")
	}
	if len(authSeen) == 0 {
		t.Fatal("fake GitHub API was never called")
	}
	for _, got := range authSeen {
		if got != "Bearer ghp_verysecret" {
			t.Errorf("GitHub Authorization = %q, want bearer token", got)
		}
	}
}

func TestInstallValidationRejections(t *testing.T) {
	valid := skillMarkdown("good-skill", "Good skill.", "body")

	cases := []struct {
		name    string
		skill   string
		content string
		over    bool
		want    string
	}{
		{name: "traversal name", skill: "../escape", content: valid, over: true, want: "invalid skill name"},
		{name: "slash name", skill: "nested/skill", content: valid, over: true, want: "invalid skill name"},
		{name: "uppercase name", skill: "BadName", content: valid, over: true, want: "invalid skill name"},
		{name: "frontmatter mismatch", skill: "good-skill", content: skillMarkdown("other-name", "Other.", "body"), over: true, want: "does not match"},
		{name: "missing closing", skill: "good-skill", content: "---\nname: good-skill\ndescription: missing close\nbody", over: true, want: "closing"},
		{name: "no frontmatter", skill: "good-skill", content: "just a body", over: true, want: "frontmatter"},
		{name: "missing description", skill: "good-skill", content: "---\nname: good-skill\n---\nbody", over: true, want: "description"},
		{name: "oversize", skill: "good-skill", content: "---\nname: good-skill\ndescription: Big.\n---\n" + strings.Repeat("x", MaxSkillBytes), over: true, want: "limit"},
		{name: "invalid UTF-8", skill: "good-skill", content: valid + string([]byte{0xff}), over: true, want: "UTF-8"},
		{name: "NUL byte", skill: "good-skill", content: valid + "\x00tail", over: true, want: "NUL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManagerAt(t.TempDir())
			_, err := mgr.Install(tc.skill, tc.content, tc.over)
			if err == nil {
				t.Fatalf("Install(%q) succeeded, want rejection", tc.skill)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
			if entries := mgr.Discover(); len(entries) != 0 {
				t.Fatalf("rejected install left a discoverable skill: %+v", entries)
			}
		})
	}
}

func TestInstallRejectsSymlinkedSkillDirectoryAndFile(t *testing.T) {
	mgr := NewManagerAt(t.TempDir())
	if err := os.MkdirAll(mgr.Dir(), 0o700); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	content := skillMarkdown("linked", "Linked.", "body")
	outsideDir := t.TempDir()

	if err := os.Symlink(outsideDir, filepath.Join(mgr.Dir(), "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := mgr.Install("linked", content, true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked skill directory error = %v, want symlink rejection", err)
	}

	fileDir := filepath.Join(mgr.Dir(), "linked-file")
	if err := os.MkdirAll(fileDir, 0o755); err != nil {
		t.Fatalf("mkdir linked-file: %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(fileDir, SkillFileName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := mgr.Install("linked-file", skillMarkdown("linked-file", "Linked file.", "body"), true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked SKILL.md error = %v, want symlink rejection", err)
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "outside" {
		t.Fatalf("symlink target was modified: data=%q err=%v", data, err)
	}
}

func TestInstallNoOverwriteUnlessRequested(t *testing.T) {
	mgr := NewManagerAt(t.TempDir())
	first := skillMarkdown("stable", "First.", "first body")
	if _, err := mgr.Install("stable", first, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	second := skillMarkdown("stable", "Second.", "second body")
	if _, err := mgr.Install("stable", second, false); !errors.Is(err, ErrExists) {
		t.Fatalf("second install without overwrite error = %v, want ErrExists", err)
	}
	data, err := os.ReadFile(filepath.Join(mgr.Dir(), "stable", SkillFileName))
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if string(data) != first {
		t.Fatalf("existing skill changed without overwrite: %q", data)
	}

	updated, err := mgr.Install("stable", second, true)
	if err != nil {
		t.Fatalf("overwrite install: %v", err)
	}
	if updated.Description != "Second." {
		t.Fatalf("overwrite description = %q", updated.Description)
	}
	data, err = os.ReadFile(filepath.Join(mgr.Dir(), "stable", SkillFileName))
	if err != nil {
		t.Fatalf("read overwritten skill: %v", err)
	}
	if string(data) != second {
		t.Fatalf("overwrite did not replace the skill: %q", data)
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	project := t.TempDir()
	sourcePath := filepath.Join(project, "skills", "dry", SkillFileName)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	content := skillMarkdown("dry-skill", "Dry skill.", "dry body")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	mgr := NewManagerAt(filepath.Join(t.TempDir(), "skills"))
	useTestManager(t, mgr)
	tool := NewInstallTool()
	result := runTool(t, tool, dispatchArgs(project), map[string]any{
		"source":  "skills/dry/SKILL.md",
		"dry_run": true,
	})
	if result.IsError {
		t.Fatalf("dry run failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "would install") || !strings.Contains(result.Content, "nothing was written") {
		t.Fatalf("dry run output = %q", result.Content)
	}
	if _, err := os.Stat(mgr.Dir()); !os.IsNotExist(err) {
		t.Fatalf("dry run created the skills directory %q: %v", mgr.Dir(), err)
	}
}

func TestInstallRejectsLocalSymlinkSource(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "real.md")
	content := skillMarkdown("linked-source", "Linked source.", "body")
	if err := os.WriteFile(outside, []byte(content), 0o644); err != nil {
		t.Fatalf("write outside skill: %v", err)
	}
	linkDir := filepath.Join(project, "skills", "linked")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatalf("mkdir link dir: %v", err)
	}
	linkPath := filepath.Join(linkDir, SkillFileName)
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	mgr := NewManagerAt(t.TempDir())
	useTestManager(t, mgr)
	tool := NewInstallTool()
	result := runTool(t, tool, dispatchArgs(project), map[string]any{
		"source": "skills/linked/SKILL.md",
	})
	if !result.IsError || !strings.Contains(result.Content, "symlink") {
		t.Fatalf("symlink source result = %+v, want symlink rejection", result)
	}
	if entries := mgr.Discover(); len(entries) != 0 {
		t.Fatalf("symlink source was installed: %+v", entries)
	}
}

func TestParseSkillContentDescriptionScalars(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "plain continuation",
			text: "---\nname: plain\ndescription: First line\n  second line.\n---\nbody\n",
			want: "First line second line.",
		},
		{
			name: "quoted",
			text: "---\nname: quoted\ndescription: \"A quoted: description.\"\n---\nbody\n",
			want: "A quoted: description.",
		},
		{
			name: "folded",
			text: "---\nname: folded\ndescription: >-\n  First line\n  second line.\n\n  New paragraph.\n---\nbody\n",
			want: "First line second line.\n\nNew paragraph.",
		},
		{
			name: "literal",
			text: "---\nname: literal\ndescription: |\n  First line\n  second line.\n---\nbody\n",
			want: "First line\nsecond line.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseSkillContent(tc.text)
			if err != nil {
				t.Fatalf("ParseSkillContent: %v", err)
			}
			if parsed.Description != tc.want {
				t.Fatalf("description = %q, want %q", parsed.Description, tc.want)
			}
		})
	}
}

func TestSkillLoadListLoadUnloadChangesDescription(t *testing.T) {
	mgr := NewManagerAt(t.TempDir())
	alphaDir := filepath.Join(mgr.Dir(), "alpha")
	betaDir := filepath.Join(mgr.Dir(), "beta")
	for _, entry := range []struct {
		dir  string
		name string
		desc string
	}{
		{alphaDir, "alpha", "Alpha description."},
		{betaDir, "beta", "Beta description."},
	} {
		if err := os.MkdirAll(entry.dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", entry.dir, err)
		}
		content := skillMarkdown(entry.name, entry.desc, entry.name+" body")
		if err := os.WriteFile(filepath.Join(entry.dir, SkillFileName), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", entry.name, err)
		}
	}
	useTestManager(t, mgr)
	tool := NewLoadTool()

	list := runTool(t, tool, tools.DispatchArgs{}, map[string]any{"action": "list"})
	if list.IsError {
		t.Fatalf("list failed: %s", list.Content)
	}
	for _, want := range []string{"alpha (available)", "beta (available)", "Alpha description."} {
		if !strings.Contains(list.Content, want) {
			t.Errorf("list output missing %q:\n%s", want, list.Content)
		}
	}

	if strings.Contains(ToolDescription(), "<name>alpha</name>") {
		t.Fatal("alpha advertised before it was loaded")
	}
	loaded := runTool(t, tool, tools.DispatchArgs{}, map[string]any{"action": "load", "name": "alpha"})
	if loaded.IsError {
		t.Fatalf("load failed: %s", loaded.Content)
	}
	if !mgr.IsLoaded("alpha") || !strings.Contains(ToolDescription(), "<name>alpha</name>") {
		t.Fatalf("load did not update session/tool description:\n%s", ToolDescription())
	}

	listed := runTool(t, tool, tools.DispatchArgs{}, map[string]any{"action": "list"})
	if !strings.Contains(listed.Content, "alpha (loaded)") || !strings.Contains(listed.Content, "beta (available)") {
		t.Fatalf("list after load = %s", listed.Content)
	}

	unloaded := runTool(t, tool, tools.DispatchArgs{}, map[string]any{"action": "unload", "name": "alpha"})
	if unloaded.IsError {
		t.Fatalf("unload failed: %s", unloaded.Content)
	}
	if mgr.IsLoaded("alpha") || strings.Contains(ToolDescription(), "<name>alpha</name>") {
		t.Fatalf("unload did not update session/tool description:\n%s", ToolDescription())
	}

	missing := runTool(t, tool, tools.DispatchArgs{}, map[string]any{"action": "load", "name": "missing"})
	if !missing.IsError {
		t.Fatalf("load of missing skill succeeded: %+v", missing)
	}
	invalid := runTool(t, tool, tools.DispatchArgs{}, map[string]any{"action": "explode"})
	if !invalid.IsError {
		t.Fatalf("invalid action succeeded: %+v", invalid)
	}
	if got := tool.Kind(); got != tools.KindAllowed {
		t.Fatalf("skill_load Kind = %v, want KindAllowed", got)
	}
	if got := NewInstallTool().Kind(); got != tools.KindNotAllowed {
		t.Fatalf("skill_install Kind = %v, want KindNotAllowed", got)
	}
}

func TestInstallErrorsDoNotLeakTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream said: ghp_verysecret", http.StatusInternalServerError)
	}))
	defer server.Close()

	const token = "ghp_verysecret"
	mgr := NewManagerAt(t.TempDir())
	useTestManager(t, mgr)
	tool := NewInstallToolWithOptions(InstallToolOptions{
		Fetcher:    &httpTestFetcher{client: server.Client()},
		APIBaseURL: server.URL,
		Token:      token,
	})

	for _, input := range []map[string]any{
		{"source": server.URL + "/SKILL.md?access_token=" + token},
		{"source": "https://example.com/SKILL.md?access_token=" + token + "%zz"},
		{"source": "acme/skills", "ref": "main"},
	} {
		result := runTool(t, tool, dispatchArgs(t.TempDir()), input)
		if !result.IsError {
			t.Fatalf("expected error for %v, got %+v", input, result)
		}
		if strings.Contains(result.Content, token) {
			t.Fatalf("error leaked token: %s", result.Content)
		}
	}
}

func TestNewUserManagerUsesAppdirSkillsDir(t *testing.T) {
	config := t.TempDir()
	t.Setenv(appdir.EnvConfigDir, config)
	mgr, err := NewUserManager()
	if err != nil {
		t.Fatalf("NewUserManager: %v", err)
	}
	want := filepath.Join(config, appdir.SkillsDirName)
	if mgr.Dir() != want {
		t.Fatalf("Dir = %q, want %q", mgr.Dir(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("appdir skills directory was not created: %v", err)
	}

	content := skillMarkdown("durable", "Durable skill.", "durable body")
	if _, err := mgr.Install("durable", content, false); err != nil {
		t.Fatalf("Install through the user manager: %v", err)
	}
	path := filepath.Join(want, "durable", SkillFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read durable skill: %v", err)
	}
	if string(data) != content {
		t.Fatalf("durable skill bytes = %q, want %q", data, content)
	}
}

func TestInstallRefHandling(t *testing.T) {
	tests := []struct {
		source  string
		ref     string
		want    string
		wantErr bool
	}{
		{source: "acme/skills", ref: "main", want: "acme/skills@main"},
		{source: "https://github.com/acme/skills", ref: "dev", want: "https://github.com/acme/skills/tree/dev"},
		{source: "https://github.com/acme/skills/tree/main", ref: "dev", wantErr: true},
		{source: "acme/skills@main", ref: "dev", wantErr: true},
		{source: "./local-skill", ref: "main", wantErr: true},
		{source: "https://example.com/SKILL.md", ref: "main", wantErr: true},
		{source: "acme/skills", ref: "bad ref", wantErr: true},
	}
	for _, tc := range tests {
		got, err := applyInstallRef(tc.source, tc.ref, "")
		if tc.wantErr {
			if err == nil {
				t.Errorf("applyInstallRef(%q, %q) = %q, want error", tc.source, tc.ref, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("applyInstallRef(%q, %q): %v", tc.source, tc.ref, err)
			continue
		}
		if got != tc.want {
			t.Errorf("applyInstallRef(%q, %q) = %q, want %q", tc.source, tc.ref, got, tc.want)
		}
	}
}

func TestInstallToolSchemasAndNames(t *testing.T) {
	install := NewInstallTool()
	load := NewLoadTool()
	if install.Name() != InstallToolName || load.Name() != LoadToolName {
		t.Fatalf("tool names = %q/%q", install.Name(), load.Name())
	}
	for _, tool := range []tools.AgentTool{install, load} {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
			t.Fatalf("%s schema: %v", tool.Name(), err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s schema type = %v", tool.Name(), schema["type"])
		}
	}
	var loadSchema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(load.InputSchema(), &loadSchema); err != nil {
		t.Fatalf("decode load schema: %v", err)
	}
	if got := strings.Join(loadSchema.Properties["action"].Enum, ","); got != "list,load,unload" {
		t.Fatalf("action enum = %q", got)
	}
}

// Keep providers imported in this test file so the tool-definition rebuild
// path is exercised through the real registry as well.
func TestRebuildSkillToolDefUpdatesRegisteredDefinition(t *testing.T) {
	t.Cleanup(func() { providers.InitToolsDef(nil) })
	preserve := providers.ToolDef{Name: ToolName, Description: "old", InputSchema: ToolSchema()}
	providers.InitToolsDef([]providers.ToolDef{preserve})
	mgr := NewManagerAt(t.TempDir())
	alphaDir := filepath.Join(mgr.Dir(), "alpha")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, SkillFileName), []byte(skillMarkdown("alpha", "Alpha.", "body")), 0o644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	useTestManager(t, mgr)

	if !mgr.Load("alpha") {
		t.Fatal("Load(alpha)")
	}
	rebuildSkillToolDef()
	if !providers.UpdateToolDef(ToolName, ToolDef()) {
		t.Fatal("UpdateToolDef did not find the skill tool")
	}
	if !strings.Contains(ToolDef().Description, "<name>alpha</name>") {
		t.Fatalf("tool definition was not rebuilt:\n%s", ToolDef().Description)
	}
}
