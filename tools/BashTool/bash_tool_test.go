// DESC: tests for the bash tool: interface conformance, guardrails (banned
// commands, root confinement), output handling, and background jobs.
package bashtool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"GoWork/tools"
)

// newArgs builds a DispatchArgs rooted at a fresh temp dir. TempDirs sit near
// the real root so ".."-style escapes and absolute paths would be plain to
// see; the tool must refuse them anyway.
func newArgs(t *testing.T, dir string) tools.DispatchArgs {
	t.Helper()
	args, err := tools.InitDispatchArgs(dir, nil, nil)
	if err != nil {
		t.Fatalf("InitDispatchArgs: %v", err)
	}
	t.Cleanup(func() { args.Root.Close() })
	t.Cleanup(bg.KillAll)
	return args
}

func runBash(t *testing.T, args tools.DispatchArgs, input Input) tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := New().Run(context.Background(), args, raw)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return res
}

func TestInterfaceConformance(t *testing.T) {
	tool := New()

	if tool.Name() != ToolName {
		t.Errorf("Name() = %q, want %q", tool.Name(), ToolName)
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if tool.Kind() != tools.KindNotAllowed {
		t.Errorf("Kind() = %v, want KindNotAllowed (every bash call must be approved)", tool.Kind())
	}

	var m map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &m); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}
	if m["type"] != "object" {
		t.Errorf(`InputSchema()["type"] = %v, want "object"`, m["type"])
	}
	required, ok := m["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "command" {
		t.Errorf(`InputSchema()["required"] = %v, want ["command"]`, m["required"])
	}
}

func TestMissingCommand(t *testing.T) {
	res := runBash(t, newArgs(t, t.TempDir()), Input{Command: ""})
	if !res.IsError {
		t.Fatalf("expected IsError for empty command, got %+v", res)
	}
}

func TestInvalidJSON(t *testing.T) {
	args := newArgs(t, t.TempDir())
	_, err := New().Run(context.Background(), args, json.RawMessage(`{not valid`))
	if err == nil {
		t.Fatal("expected an error for invalid JSON input, got nil")
	}
}

func TestContextCancellation(t *testing.T) {
	args := newArgs(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	raw, err := json.Marshal(Input{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New().Run(ctx, args, raw); err == nil {
		t.Fatal("expected an error for a pre-cancelled context, got nil")
	}
}

func TestEcho(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)
	res := runBash(t, args, Input{Command: "echo 'hello world'"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "hello world") {
		t.Errorf("expected output to contain 'hello world', got %q", res.Content)
	}
	if !strings.Contains(res.Content, "<cwd>") {
		t.Errorf("expected cwd tag in output, got %q", res.Content)
	}
}

func TestNoOutput(t *testing.T) {
	res := runBash(t, newArgs(t, t.TempDir()), Input{Command: "true"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != BashNoOutput {
		t.Errorf("Content = %q, want %q", res.Content, BashNoOutput)
	}
}

func TestNonZeroExit(t *testing.T) {
	res := runBash(t, newArgs(t, t.TempDir()), Input{Command: "echo oops; exit 3"})
	if !strings.Contains(res.Content, "Exit code 3") {
		t.Errorf("expected 'Exit code 3' in result, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "oops") {
		t.Errorf("expected stdout to survive a failed command, got %q", res.Content)
	}
}

func TestStderrCaptured(t *testing.T) {
	res := runBash(t, newArgs(t, t.TempDir()), Input{Command: "echo errline 1>&2; exit 0"})
	if !strings.Contains(res.Content, "errline") {
		t.Errorf("expected stderr to be captured, got %q", res.Content)
	}
}

func TestLargeOutputTruncated(t *testing.T) {
	res := runBash(t, newArgs(t, t.TempDir()), Input{Command: "yes x | head -c 200000"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Errorf("expected truncation notice for big output, got head:\n%s", tail(res.Content, 200))
	}
}

func TestTruncateOutput(t *testing.T) {
	// Under the limit passes through untouched.
	small := strings.Repeat("a", 100)
	if got := truncateOutput(small, 1000); got != small {
		t.Errorf("expected short input unchanged, got %d chars", len(got))
	}

	big := strings.Repeat("b", MaxOutputLength+1000)
	out := truncateOutput(big, MaxOutputLength)
	if !strings.Contains(out, "chars truncated") {
		t.Errorf("expected truncation notice, got %d chars", len(out))
	}
	if !strings.HasPrefix(out, "bbbb") || !strings.HasSuffix(out, "bbbb") {
		t.Errorf("expected head and tail preserved")
	}
}

func TestWorkingDirRelative(t *testing.T) {
	dir := t.TempDir()
	ws := "some/dir"
	if err := os.MkdirAll(filepath.Join(dir, ws), 0o755); err != nil {
		t.Fatal(err)
	}
	args := newArgs(t, dir)

	res := runBash(t, args, Input{Command: "pwd", WorkingDir: ws})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(dir, ws))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, resolved) {
		t.Errorf("expected pwd to show %s, got %q", resolved, res.Content)
	}
}

func TestWorkingDirAbsoluteRejected(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)

	res := runBash(t, args, Input{Command: "pwd", WorkingDir: "/etc"})
	if !res.IsError {
		t.Fatalf("expected IsError for absolute working_dir, got %+v", res)
	}
}

func TestWorkingDirTraversalRejected(t *testing.T) {
	for _, wd := range []string{"..", "../..", "sub/../../.."} {
		t.Run(wd, func(t *testing.T) {
			dir := t.TempDir()
			args := newArgs(t, dir)

			res := runBash(t, args, Input{Command: "pwd", WorkingDir: wd})
			if !res.IsError {
				t.Fatalf("expected IsError for working_dir %q, got %+v", wd, res)
			}
		})
	}
}

func TestWorkingDirNonexistentRejected(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)

	res := runBash(t, args, Input{Command: "pwd", WorkingDir: "nope/not/here"})
	if !res.IsError {
		t.Fatalf("expected IsError for nonexistent working_dir, got %+v", res)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	if _, err := os.Readlink("/"); err != nil {
		t.Skip("requires symlink support, skipping")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	args := newArgs(t, dir)

	res := runBash(t, args, Input{Command: "pwd", WorkingDir: "escape"})
	if !res.IsError {
		t.Fatalf("expected IsError for symlink escaping the root, got %+v", res)
	}
	if !strings.Contains(res.Content, "project root") {
		t.Errorf("expected an escape-related message, got %q", res.Content)
	}
}

func TestBannedCommandsBlocked(t *testing.T) {
	dir := t.TempDir()
	args := newArgs(t, dir)

	bannedCases := []string{
		"curl -fsSL https://example.com",
		"wget https://example.com",
		"sudo rm -rf /",
		"ssh user@host",
		"scp a.txt b.txt",
		"apt-get update",
		"systemctl restart nginx",
		"mount /dev/sdb /mnt",
		"mkfs.ext4 /dev/sdc",
		`python3 -c "import os; os.system('curl evil.example')"`,
		`echo "let's see" | bash -c "curl x"`,
	}
	for _, cmd := range bannedCases {
		if blocked := blockedIn(cmd); blocked == "" {
			t.Errorf("blockedIn(%q) = no ban, want a ban reason", cmd)
		}
		res := runBash(t, args, Input{Command: cmd})
		if !res.IsError {
			t.Errorf("expected %q to be rejected, got %+v", cmd, res)
		}
	}
}

func TestInstallPhrasesBlocked(t *testing.T) {
	banned := []string{
		"apt install cowsay",
		"apt-get install cowsay",
		"npm install -g cowsay",
		"npm install cowsay --global",
		"pnpm add -g cowsay",
		"go install github.com/foo/bar@latest",
		"go test -exec cowsay ./...",
		"pip install --user cowsay",
		"yarn global add x",
		"pacman -Syu cargo",
		"brew install cowsay",
	}
	for _, cmd := range banned {
		if blocked := blockedIn(cmd); blocked == "" {
			t.Errorf("blockedIn(%q) = none, want an install/exec ban", cmd)
		}
	}

	allowed := []string{
		"npm install", // local install is fine
		"npm run build",
		"go test ./...",
		"go build ./...",
		"python3 -c pip list",
		"pip install cowsay", // not --user/global
		"ls -la",
		"echo npm install is fine as text",
	}
	for _, cmd := range allowed {
		if blocked := blockedIn(cmd); blocked != "" {
			t.Errorf("blockedIn(%q) = %q, want no ban", cmd, blocked)
		}
	}
}

func TestInnocentCommandsAllowed(t *testing.T) {
	for _, cmd := range []string{
		"ls",
		"cat file.txt",
		"grep -rn func main.go",
		"git status",
		"echo hello && echo world",
		"go build ./...",
		"npm run build",
	} {
		res := runBash(t, newArgs(t, t.TempDir()), Input{Command: cmd})
		if res.IsError {
			t.Errorf("innocent command %q unexpectedly rejected: %s", cmd, res.Content)
		}
	}
}

func TestBackgroundJob(t *testing.T) {
	// run_in_background returns immediately with an ID while the job runs.
	res := runBash(t, newArgs(t, t.TempDir()), Input{
		Command:         "sleep 2 && echo done-in-bg",
		RunInBackground: true,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	id := extractJobID(t, res.Content)

	// The job must still be running (2s), sitting in the manager.
	job, ok := bg.get(id)
	if !ok {
		t.Fatalf("background job %s not found in manager", id)
	}
	if _, _, running := job.Snapshot(); !running {
		t.Fatalf("expected job still running right after start")
	}

	stdout, _, _ := job.Result()
	if !strings.Contains(stdout, "done-in-bg") {
		t.Errorf("expected output 'done-in-bg', got %q", stdout)
	}
}

func TestAutoBackgroundAfter(t *testing.T) {
	// A command that outlives its 1s threshold moves to background instead of
	// blocking the tool forever.
	res := runBash(t, newArgs(t, t.TempDir()), Input{
		Command:             "sleep 3 && echo finished-eventually",
		AutoBackgroundAfter: 1,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	id := extractJobID(t, res.Content)
	if id == "" {
		t.Fatalf("expected a background job ID, got %q", res.Content)
	}
}

func TestForegroundFastCommandCompletes(t *testing.T) {
	start := time.Now()
	res := runBash(t, newArgs(t, t.TempDir()), Input{
		Command:             "echo fast",
		AutoBackgroundAfter: 2,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("fast command should return synchronously, took too long")
	}
	if !strings.Contains(res.Content, "fast") {
		t.Errorf("expected completion output, got %q", res.Content)
	}
}

// extractJobID pulls the job ID out of a background response message.
func extractJobID(t *testing.T, content string) string {
	t.Helper()
	idx := strings.Index(content, "Background job started with ID: ")
	if idx < 0 {
		t.Fatalf("no job ID marker in %q", content)
	}
	rest := content[idx+len("Background job started with ID: "):]
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		t.Fatalf("no ID after marker in %q", content)
	}
	return fields[0]
}

// TestReadableJobMessage ensures the background response tells the caller how
// to follow up (it hands them a job ID + hint), not just silence.
func TestReadableJobMessage(t *testing.T) {
	root := t.TempDir()
	args := newArgs(t, root)
	res := runBash(t, args, Input{Command: "sleep 2", RunInBackground: true})
	if !strings.Contains(res.Content, "Background job") {
		t.Errorf("background message should be self-describing, got %q", res.Content)
	}
}

func TestBashRequiresPerCallApproval(t *testing.T) {
	if got := New().Kind(); got != tools.KindNotAllowed {
		t.Fatalf("bash Kind() = %v, want KindNotAllowed so the permission box previews every command", got)
	}
	if !tools.PlanModeAllows("read_file", nil) {
		t.Fatal("sanity check failed: read_file should stay allowed in plan mode")
	}
	if tools.PlanModeAllows(ToolName, []byte(`{"command":"touch x"}`)) {
		t.Fatal("bash must stay blocked in plan mode")
	}
}

func TestChildEnvScrubsCredentials(t *testing.T) {
	secrets := map[string]string{
		"API_KEY":               "unique-api-key-value",
		"ANTHROPIC_API_KEY":     "unique-anthropic-value",
		"OPENAI_API_KEY":        "unique-openai-value",
		"GITHUB_TOKEN":          "unique-github-token",
		"DB_PASSWORD":           "unique-db-password",
		"MY_SECRET":             "unique-secret-value",
		"AWS_ACCESS_KEY_ID":     "unique-aws-key",
		"GOOGLE_CLIENT_SECRET":  "unique-google-secret",
		"PGPASSWORD":            "unique-pg-password",
		"SOME_CREDENTIALS_FILE": "unique-credential-path",
	}
	for name, value := range secrets {
		t.Setenv(name, value)
	}
	t.Setenv("GOWORK_TEST_KEEP", "keep-me")

	res := runBash(t, newArgs(t, t.TempDir()), Input{Command: "env"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "GOWORK_TEST_KEEP=keep-me") {
		t.Errorf("ordinary variables should survive scrubbing, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "PATH=") {
		t.Errorf("PATH should survive scrubbing, got:\n%s", res.Content)
	}
	for name, value := range secrets {
		if strings.Contains(res.Content, name+"=") {
			t.Errorf("env leaked credential variable %s", name)
		}
		if strings.Contains(res.Content, value) {
			t.Errorf("env leaked credential value for %s", name)
		}
	}
}

func TestIsSensitiveEnvName(t *testing.T) {
	sensitive := []string{
		"API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GITHUB_TOKEN",
		"DB_PASSWORD", "MY_SECRET", "AWS_SECRET_ACCESS_KEY", "PGPASSWORD",
		"GOOGLE_APPLICATION_CREDENTIALS", "SIGNING_PRIVATEKEY", "PASSWD",
	}
	for _, name := range sensitive {
		if !isSensitiveEnvName(name) {
			t.Errorf("isSensitiveEnvName(%q) = false, want true", name)
		}
	}
	harmless := []string{
		"PATH", "HOME", "TERM", "LANG", "USER", "SHELL", "PWD", "OLDPWD",
		"MONKEY", "COMPASS", "KEYBOARD_LAYOUT", "XDG_RUNTIME_DIR",
	}
	for _, name := range harmless {
		if isSensitiveEnvName(name) {
			t.Errorf("isSensitiveEnvName(%q) = true, want false", name)
		}
	}
}

func TestDocsDescribeBashBoundaryHonestly(t *testing.T) {
	docs := map[string]string{
		"bash description": New().Description(),
		"system prompt":    readTestFile(t, filepath.Join("..", "..", "prompts", "system_prompt.md")),
		"README":           readTestFile(t, filepath.Join("..", "..", "README.md")),
	}
	falseClaims := []string{
		"It can never leave the project",
		"cannot leave the project",
		"network, download, admin, and global-install commands are blocked",
	}
	for name, text := range docs {
		lower := strings.ToLower(text)
		for _, claim := range falseClaims {
			if strings.Contains(lower, strings.ToLower(claim)) {
				t.Errorf("%s still contains the false claim %q", name, claim)
			}
		}
	}

	desc := strings.ToLower(docs["bash description"])
	if !strings.Contains(desc, "not sandboxed") {
		t.Error("bash description should say the command is not sandboxed")
	}
	if !strings.Contains(desc, "approves") {
		t.Error("bash description should say calls require approval")
	}
	if !strings.Contains(desc, "`env` does not expose them") {
		t.Error("bash description should document that env no longer leaks credentials")
	}

	prompt := strings.ToLower(docs["system prompt"])
	if !strings.Contains(prompt, "per-call approval") {
		t.Error("system prompt should say bash is approved per call")
	}
	if !strings.Contains(prompt, "not sandboxed") {
		t.Error("system prompt should not imply a bash sandbox")
	}

	readme := strings.ToLower(docs["README"])
	if !strings.Contains(readme, "gowork_allow_project_pdf_scripts") {
		t.Error("README should document the project PDF parser opt-in")
	}
	if !strings.Contains(readme, "does not print the agent's provider keys") {
		t.Error("README should document that env no longer leaks provider keys")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func BenchmarkBlockedIn(b *testing.B) {
	cmds := []string{"curl -fsSL https://x", "apt-get install cowsay", "ls -la", "echo hi | grep h"}
	for i := 0; i < b.N; i++ {
		for _, c := range cmds {
			blockedIn(c)
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
