package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	installerPathStart = "# >>> GoWork installer >>>"
	installerPathEnd   = "# <<< GoWork installer <<<"
)

// installerScript returns the absolute path to the POSIX installer and skips
// tests when the host cannot run it.
func installerScript(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("scripts/install.sh requires a POSIX shell")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	path, err := filepath.Abs(filepath.Join("..", "scripts", "install.sh"))
	if err != nil {
		t.Fatalf("resolve installer path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("installer missing: %v", err)
	}
	return path
}

func windowsInstallerScript(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("scripts/install.ps1 requires Windows")
	}
	shell, err := exec.LookPath("powershell")
	if err != nil {
		shell, err = exec.LookPath("pwsh")
	}
	if err != nil {
		t.Skip("PowerShell is not available")
	}
	path, err := filepath.Abs(filepath.Join("..", "scripts", "install.ps1"))
	if err != nil {
		t.Fatalf("resolve installer path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("installer missing: %v", err)
	}
	return shell, path
}

func runWindowsInstaller(t *testing.T, shell, script, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(shell, append([]string{
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
	}, args...)...)
	env := map[string]string{
		"USERPROFILE":       home,
		"HOME":              home,
		"LOCALAPPDATA":      filepath.Join(home, "AppData", "Local"),
		"APPDATA":           filepath.Join(home, "AppData", "Roaming"),
		"TEMP":              filepath.Join(home, "AppData", "Local", "Temp"),
		"TMP":               filepath.Join(home, "AppData", "Local", "Temp"),
		"GOWORK_CONFIG_DIR": "",
	}
	blocked := make(map[string]bool, len(env))
	for key := range env {
		blocked[key] = true
	}
	for _, entry := range os.Environ() {
		key := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key = entry[:i]
		}
		if !blocked[key] {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func installerEnv(home string, extra map[string]string) []string {
	overrides := map[string]string{
		"HOME":              home,
		"XDG_DATA_HOME":     filepath.Join(home, ".local", "share"),
		"XDG_BIN_HOME":      filepath.Join(home, ".local", "bin"),
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"GOWORK_CONFIG_DIR": "",
		"SHELL":             "/bin/bash",
	}
	for key, value := range extra {
		overrides[key] = value
	}

	blocked := make(map[string]bool, len(overrides))
	for key := range overrides {
		blocked[key] = true
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key = entry[:i]
		}
		if !blocked[key] {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func runInstaller(t *testing.T, script, home string, extra map[string]string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = installerEnv(home, extra)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestInstallerRejectsDangerousDirectories(t *testing.T) {
	script := installerScript(t)

	home := t.TempDir()
	sentinel := filepath.Join(home, "keep-me.txt")
	if err := os.WriteFile(sentinel, []byte("user data"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	cases := []struct {
		name string
		dir  string
		want string
	}{
		{name: "empty", dir: "", want: "must not be empty"},
		{name: "root", dir: "/", want: "refusing to use /"},
		{name: "home", dir: home, want: "home directory"},
		{name: "home with trailing slash", dir: home + string(filepath.Separator), want: "home directory"},
		{name: "home parent", dir: filepath.Join(home, ".."), want: "contains your home directory"},
		{name: "current directory", dir: ".", want: "dedicated subdirectory"},
		{name: "parent directory", dir: "..", want: "dedicated subdirectory"},
		{name: "user config directory", dir: filepath.Join(home, ".config", "GoWork"), want: "configuration directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runInstaller(t, script, home, nil, "--uninstall", "--dir", tc.dir)
			if err == nil {
				t.Fatalf("installer accepted dangerous directory %q", tc.dir)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}

	// A symlink to /tmp resolves to a one-component path and must be rejected
	// without following or deleting the target.
	linkRoot := t.TempDir()
	link := filepath.Join(linkRoot, "root-link")
	if err := os.Symlink("/tmp", link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, stderr, err := runInstaller(t, script, home, nil, "--uninstall", "--dir", link); err == nil {
		t.Fatalf("installer accepted symlink to /tmp; stderr=%q", stderr)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("installer removed the symlink before validating it: %v", err)
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel was removed by a rejected uninstall: %v", err)
	}
}

func TestWindowsInstallerRejectsHomeAndPreservesLegacySkills(t *testing.T) {
	shell, script := windowsInstallerScript(t)

	home := t.TempDir()
	sentinel := filepath.Join(home, "keep-me.txt")
	if err := os.WriteFile(sentinel, []byte("user data"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	for _, tc := range []struct {
		name string
		dir  string
		want string
	}{
		{name: "empty", dir: "", want: "empty"},
		{name: "home", dir: home, want: "home directory"},
		{name: "user config directory", dir: filepath.Join(home, "AppData", "Roaming", "GoWork"), want: "configuration directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := runWindowsInstaller(t, shell, script, home, "-Uninstall", "-Dir", tc.dir)
			if err == nil {
				t.Fatalf("installer accepted dangerous directory %q: %s", tc.dir, output)
			}
			if !strings.Contains(strings.ToLower(output), tc.want) {
				t.Fatalf("output = %q, want it to contain %q", output, tc.want)
			}
		})
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel was removed by a rejected uninstall: %v", err)
	}

	installDir := filepath.Join(home, "apps", "GoWork")
	legacySkill := filepath.Join(installDir, ".GoWork", "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacySkill), 0o700); err != nil {
		t.Fatalf("mkdir legacy skill: %v", err)
	}
	if err := os.WriteFile(legacySkill, []byte("legacy skill body"), 0o600); err != nil {
		t.Fatalf("write legacy skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "gowork.exe"), []byte("fake"), 0o600); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	output, err := runWindowsInstaller(t, shell, script, home, "-Uninstall", "-Dir", installDir)
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(legacySkill); err != nil {
		t.Fatalf("legacy skill did not survive uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "gowork.exe")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the gowork binary behind: %v", err)
	}
}

func installerReleaseArchive(t *testing.T, version, asset string) ([]byte, []byte) {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)

	script := fmt.Sprintf("#!/bin/sh\nprintf 'gowork version %s\\n'\n", version)
	header := &tar.Header{
		Name:     "gowork",
		Mode:     0o755,
		Size:     int64(len(script)),
		ModTime:  time.Unix(0, 0),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	sum := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	return archive.Bytes(), checksums
}

func installerAssetName(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skipf("installer does not support %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		t.Skipf("installer does not support %s", runtime.GOARCH)
	}
	return fmt.Sprintf("gowork_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

func requireInstallerTools(t *testing.T) {
	t.Helper()
	for _, name := range []string{"tar"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is required by the installer test", name)
		}
	}
	if _, err := exec.LookPath("curl"); err == nil {
		return
	}
	if _, err := exec.LookPath("wget"); err == nil {
		return
	}
	t.Skip("curl or wget is required by the installer test")
}

func TestInstallerPreservesLegacySkillsAcrossUpdateAndUninstall(t *testing.T) {
	script := installerScript(t)
	requireInstallerTools(t)

	asset := installerAssetName(t)
	v1Archive, v1Checksums := installerReleaseArchive(t, "v1.0.0", asset)
	v2Archive, v2Checksums := installerReleaseArchive(t, "v1.0.1", asset)

	mux := http.NewServeMux()
	serveRelease := func(version string, archive, checksums []byte) {
		prefix := "/download/" + version + "/"
		mux.HandleFunc(prefix+asset, func(w http.ResponseWriter, r *http.Request) {
			w.Write(archive)
		})
		mux.HandleFunc(prefix+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
			w.Write(checksums)
		})
	}
	serveRelease("v1.0.0", v1Archive, v1Checksums)
	serveRelease("v1.0.1", v2Archive, v2Checksums)
	server := httptest.NewServer(mux)
	defer server.Close()

	home := t.TempDir()
	tmpDir := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	env := map[string]string{
		"GOWORK_RELEASES_URL": server.URL,
		"TMPDIR":              tmpDir,
	}
	installDir := filepath.Join(home, "apps", "gowork")

	if _, stderr, err := runInstaller(t, script, home, env,
		"--version", "v1.0.0", "--dir", installDir, "--no-modify-path"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, stderr)
	}

	legacySkill := filepath.Join(installDir, ".GoWork", "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacySkill), 0o700); err != nil {
		t.Fatalf("mkdir legacy skill: %v", err)
	}
	const legacyBody = "legacy skill body"
	if err := os.WriteFile(legacySkill, []byte(legacyBody), 0o600); err != nil {
		t.Fatalf("write legacy skill: %v", err)
	}

	if _, stderr, err := runInstaller(t, script, home, env,
		"--update", "--version", "v1.0.1", "--dir", installDir, "--no-modify-path"); err != nil {
		t.Fatalf("update failed: %v\n%s", err, stderr)
	}

	versionOutput, err := exec.Command(filepath.Join(installDir, "gowork"), "--version").Output()
	if err != nil {
		t.Fatalf("run updated binary: %v", err)
	}
	if !strings.Contains(string(versionOutput), "v1.0.1") {
		t.Fatalf("updated version output = %q, want v1.0.1", versionOutput)
	}
	body, err := os.ReadFile(legacySkill)
	if err != nil {
		t.Fatalf("legacy skill did not survive update: %v", err)
	}
	if string(body) != legacyBody {
		t.Fatalf("legacy skill body = %q, want %q", body, legacyBody)
	}

	if _, stderr, err := runInstaller(t, script, home, env,
		"--uninstall", "--dir", installDir, "--no-modify-path"); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, stderr)
	}

	body, err = os.ReadFile(legacySkill)
	if err != nil {
		t.Fatalf("legacy skill did not survive uninstall: %v", err)
	}
	if string(body) != legacyBody {
		t.Fatalf("legacy skill body after uninstall = %q, want %q", body, legacyBody)
	}
	if _, err := os.Stat(filepath.Join(installDir, "gowork")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the gowork binary behind: %v", err)
	}
}

func TestInstallerPathBlockRemovalIsBounded(t *testing.T) {
	script := installerScript(t)

	t.Run("missing end marker leaves the file untouched", func(t *testing.T) {
		home := t.TempDir()
		config := filepath.Join(home, ".bashrc")
		content := "export USER_SETTING=keep\n" +
			installerPathStart + "\n" +
			"export PATH='/opt/gowork':\"$PATH\"\n" +
			"export ANOTHER_USER_SETTING=also-keep\n"
		if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
			t.Fatalf("write bashrc: %v", err)
		}

		if _, stderr, err := runInstaller(t, script, home, nil,
			"--uninstall", "--dir", filepath.Join(home, "apps", "gowork"), "--no-modify-path"); err != nil {
			t.Fatalf("uninstall failed on malformed block: %v\n%s", err, stderr)
		}
		got, err := os.ReadFile(config)
		if err != nil {
			t.Fatalf("read bashrc: %v", err)
		}
		if string(got) != content {
			t.Fatalf("malformed PATH block was modified:\n got: %q\nwant: %q", got, content)
		}
	})

	t.Run("complete block is removed without touching neighbours", func(t *testing.T) {
		home := t.TempDir()
		config := filepath.Join(home, ".bashrc")
		content := "export BEFORE=1\n" +
			installerPathStart + "\n" +
			"# Added by the GoWork installer. Remove this block to undo the PATH change.\n" +
			"export PATH='/opt/gowork':\"$PATH\"\n" +
			installerPathEnd + "\n" +
			"export AFTER=2\n"
		if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
			t.Fatalf("write bashrc: %v", err)
		}

		if _, stderr, err := runInstaller(t, script, home, nil,
			"--uninstall", "--dir", filepath.Join(home, "apps", "gowork"), "--no-modify-path"); err != nil {
			t.Fatalf("uninstall failed: %v\n%s", err, stderr)
		}
		got, err := os.ReadFile(config)
		if err != nil {
			t.Fatalf("read bashrc: %v", err)
		}
		text := string(got)
		if strings.Contains(text, installerPathStart) || strings.Contains(text, installerPathEnd) {
			t.Fatalf("PATH block markers remain after uninstall: %q", text)
		}
		if !strings.Contains(text, "export BEFORE=1") || !strings.Contains(text, "export AFTER=2") {
			t.Fatalf("uninstall removed neighbouring lines: %q", text)
		}
	})
}
