package test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const posixWrapperStub = "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GOWORK_STUB_ARGS\"\nexit 42\n"

const powershellWrapperStub = `param(
    [switch]$Update,
    [switch]$Uninstall,
    [string]$Version = "",
    [string]$Dir = "",
    [switch]$NoModifyPath
)
$lines = @(
    "Update=$($Update.IsPresent)"
    "Uninstall=$($Uninstall.IsPresent)"
    "Version=$Version"
    "Dir=$Dir"
    "NoModifyPath=$($NoModifyPath.IsPresent)"
)
[System.IO.File]::WriteAllLines($env:GOWORK_STUB_ARGS, [string[]]$lines)
exit 42
`

// wrapperScriptPath returns the absolute path to a POSIX wrapper and skips
// tests when the host cannot run it.
func wrapperScriptPath(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrappers require a POSIX shell")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	path, err := filepath.Abs(filepath.Join("..", "scripts", name))
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("wrapper missing: %v", err)
	}
	return path
}

func wrapperPowerShellPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "scripts", name))
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("wrapper missing: %v", err)
	}
	return path
}

// powershellExecutable returns a PowerShell host or skips the test.
func powershellExecutable(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell is not available")
	return ""
}

func TestInstallerWrappersAreThin(t *testing.T) {
	posix := []struct {
		name string
		mode string
	}{
		{name: "update.sh", mode: "--update"},
		{name: "uninstall.sh", mode: "--uninstall"},
	}
	for _, tc := range posix {
		t.Run(tc.name, func(t *testing.T) {
			path := wrapperScriptPath(t, tc.name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.name, err)
			}
			text := string(data)
			for _, want := range []string{"install.sh", tc.mode, "exec"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s is missing %q; wrappers must delegate to install.sh", tc.name, want)
				}
			}
			for _, forbidden := range []string{
				"curl ", "wget ", "sha256sum", "shasum", "tar -",
				"parse_args", "validate_install_dir",
			} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%s duplicates installer logic (%q); keep install.sh as the single source of truth", tc.name, forbidden)
				}
			}
		})
	}

	powershell := []struct {
		name string
		mode string
	}{
		{name: "update.ps1", mode: "-Update"},
		{name: "uninstall.ps1", mode: "-Uninstall"},
	}
	for _, tc := range powershell {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(wrapperPowerShellPath(t, tc.name))
			if err != nil {
				t.Fatalf("read %s: %v", tc.name, err)
			}
			text := string(data)
			for _, want := range []string{"install.ps1", tc.mode, "@args", "exit"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s is missing %q; wrappers must delegate to install.ps1", tc.name, want)
				}
			}
			for _, forbidden := range []string{
				"Invoke-WebRequest", "Get-FileHash", "Expand-Archive",
				"Assert-SafeInstallDirectory", "Add-ToUserPath", "Remove-FromUserPath",
			} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%s duplicates installer logic (%q); keep install.ps1 as the single source of truth", tc.name, forbidden)
				}
			}
		})
	}
}

func TestShellWrapperSyntax(t *testing.T) {
	scripts := []string{
		wrapperScriptPath(t, "update.sh"),
		wrapperScriptPath(t, "uninstall.sh"),
	}
	for _, shell := range []string{"bash", "sh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		for _, script := range scripts {
			out, err := exec.Command(shell, "-n", script).CombinedOutput()
			if err != nil {
				t.Errorf("%s -n %s failed: %v\n%s", shell, filepath.Base(script), err, out)
			}
		}
	}
}

func TestInstallerWrappersForwardArgumentsAndExitCodes(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{name: "update.sh", mode: "--update"},
		{name: "uninstall.sh", mode: "--uninstall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := wrapperScriptPath(t, tc.name)
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatalf("read wrapper: %v", err)
			}

			dir := t.TempDir()
			wrapper := filepath.Join(dir, tc.name)
			if err := os.WriteFile(wrapper, data, 0o755); err != nil {
				t.Fatalf("write wrapper: %v", err)
			}
			argsPath := filepath.Join(t.TempDir(), "args.txt")
			if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(posixWrapperStub), 0o755); err != nil {
				t.Fatalf("write stub installer: %v", err)
			}

			args := []string{
				"--version", "v9.9.9",
				"--dir", filepath.Join(dir, "apps", "gowork"),
				"--no-modify-path",
				"--future-flag", "value",
			}
			cmd := exec.Command("bash", append([]string{wrapper}, args...)...)
			// Run from an unrelated directory to prove the wrapper resolves
			// install.sh relative to itself, not the caller's working directory.
			cmd.Dir = t.TempDir()
			cmd.Env = installerEnv(t.TempDir(), map[string]string{"GOWORK_STUB_ARGS": argsPath})
			var stderr bytes.Buffer
			cmd.Stderr = &stderr

			err = cmd.Run()
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("wrapper exit = %v, want exit code 42; stderr=%q", err, stderr.String())
			}
			if exitErr.ExitCode() != 42 {
				t.Fatalf("wrapper exit code = %d, want 42; stderr=%q", exitErr.ExitCode(), stderr.String())
			}

			recorded, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("stub did not run: %v", err)
			}
			got := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
			want := append([]string{tc.mode}, args...)
			if !slices.Equal(got, want) {
				t.Fatalf("install.sh arguments = %q, want %q", got, want)
			}
		})
	}
}

func TestInstallerWrappersResolveSymlinks(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{name: "update.sh", mode: "--update"},
		{name: "uninstall.sh", mode: "--uninstall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := wrapperScriptPath(t, tc.name)
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatalf("read wrapper: %v", err)
			}

			root := t.TempDir()
			realDir := filepath.Join(root, "real")
			linkDir := filepath.Join(root, "bin")
			runDir := filepath.Join(root, "elsewhere")
			for _, dir := range []string{realDir, linkDir, runDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", dir, err)
				}
			}

			realWrapper := filepath.Join(realDir, tc.name)
			if err := os.WriteFile(realWrapper, data, 0o755); err != nil {
				t.Fatalf("write wrapper: %v", err)
			}
			argsPath := filepath.Join(root, "args.txt")
			if err := os.WriteFile(filepath.Join(realDir, "install.sh"), []byte(posixWrapperStub), 0o755); err != nil {
				t.Fatalf("write stub installer: %v", err)
			}

			// A relative symlink in a different directory exercises both the
			// readlink loop and resolution of a relative link target.
			link := filepath.Join(linkDir, "gowork-"+strings.TrimSuffix(tc.name, ".sh"))
			relativeTarget, err := filepath.Rel(linkDir, realWrapper)
			if err != nil {
				t.Fatalf("relative symlink target: %v", err)
			}
			if err := os.Symlink(relativeTarget, link); err != nil {
				t.Skipf("symlink not supported: %v", err)
			}

			args := []string{
				"--version", "v9.9.9",
				"--dir", filepath.Join(root, "apps", "gowork"),
				"--no-modify-path",
			}
			cmd := exec.Command("bash", append([]string{link}, args...)...)
			cmd.Dir = runDir
			cmd.Env = installerEnv(t.TempDir(), map[string]string{"GOWORK_STUB_ARGS": argsPath})
			var stderr bytes.Buffer
			cmd.Stderr = &stderr

			err = cmd.Run()
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("symlinked wrapper exit = %v, want exit code 42; stderr=%q", err, stderr.String())
			}
			if exitErr.ExitCode() != 42 {
				t.Fatalf("symlinked wrapper exit code = %d, want 42; stderr=%q", exitErr.ExitCode(), stderr.String())
			}

			recorded, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("stub did not run through the symlink: %v", err)
			}
			got := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
			want := append([]string{tc.mode}, args...)
			if !slices.Equal(got, want) {
				t.Fatalf("install.sh arguments = %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateAndUninstallWrappersWithMockedRelease(t *testing.T) {
	install := installerScript(t)
	update := wrapperScriptPath(t, "update.sh")
	uninstall := wrapperScriptPath(t, "uninstall.sh")
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
	launcher := filepath.Join(home, ".local", "bin", "gowork")

	if _, stderr, err := runInstaller(t, install, home, env,
		"--version", "v1.0.0", "--dir", installDir, "--no-modify-path"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, stderr)
	}
	if _, err := os.Lstat(launcher); err != nil {
		t.Fatalf("installer did not create the launcher: %v", err)
	}

	legacySkill := filepath.Join(installDir, ".GoWork", "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacySkill), 0o700); err != nil {
		t.Fatalf("mkdir legacy skill: %v", err)
	}
	const legacyBody = "legacy skill body"
	if err := os.WriteFile(legacySkill, []byte(legacyBody), 0o600); err != nil {
		t.Fatalf("write legacy skill: %v", err)
	}

	output, stderr, err := runInstaller(t, update, home, env,
		"--version", "v1.0.1", "--dir", installDir, "--no-modify-path")
	if err != nil {
		t.Fatalf("update wrapper failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(output, "Updating GoWork") {
		t.Fatalf("update wrapper output = %q, want an update message", output)
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

	// Running the update wrapper again only changes the installer mode; the
	// forwarded --version/--dir arguments make it a no-op.
	output, stderr, err = runInstaller(t, update, home, env,
		"--version", "v1.0.1", "--dir", installDir, "--no-modify-path")
	if err != nil {
		t.Fatalf("second update wrapper failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(output, "already up to date") {
		t.Fatalf("second update output = %q, want an up-to-date message", output)
	}

	output, stderr, err = runInstaller(t, uninstall, home, env,
		"--dir", installDir, "--no-modify-path")
	if err != nil {
		t.Fatalf("uninstall wrapper failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(output, "Preserved user data") {
		t.Fatalf("uninstall wrapper output = %q, want preserved user data", output)
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
	if _, err := os.Lstat(launcher); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the launcher behind: %v", err)
	}
}

func TestInstallerWrappersRejectDangerousDirectories(t *testing.T) {
	for _, name := range []string{"update.sh", "uninstall.sh"} {
		t.Run(name, func(t *testing.T) {
			wrapper := wrapperScriptPath(t, name)
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
				{name: "root", dir: "/", want: "refusing to use /"},
				{name: "home", dir: home, want: "home directory"},
				{name: "user config directory", dir: filepath.Join(home, ".config", "GoWork"), want: "configuration directory"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					_, stderr, err := runInstaller(t, wrapper, home, nil, "--dir", tc.dir)
					if err == nil {
						t.Fatalf("%s accepted dangerous directory %q", name, tc.dir)
					}
					exitErr, ok := err.(*exec.ExitError)
					if !ok || exitErr.ExitCode() != 1 {
						t.Fatalf("%s exit = %v, want code 1; stderr=%q", name, err, stderr)
					}
					if !strings.Contains(stderr, tc.want) {
						t.Fatalf("%s stderr = %q, want it to contain %q", name, stderr, tc.want)
					}
				})
			}

			if _, err := os.Stat(sentinel); err != nil {
				t.Fatalf("sentinel was removed by a rejected wrapper: %v", err)
			}
		})
	}
}

func TestPowerShellInstallerWrappersParse(t *testing.T) {
	shell := powershellExecutable(t)
	for _, name := range []string{"update.ps1", "uninstall.ps1"} {
		t.Run(name, func(t *testing.T) {
			path := wrapperPowerShellPath(t, name)
			const parser = `$tokens = $null; $errors = $null; ` +
				`[System.Management.Automation.Language.Parser]::ParseFile($env:GOWORK_PARSE_TARGET, [ref]$tokens, [ref]$errors) | Out-Null; ` +
				`if ($errors.Count -gt 0) { $errors | ForEach-Object { Write-Error $_ }; exit 1 }`
			cmd := exec.Command(shell, "-NoProfile", "-Command", parser)
			cmd.Env = append(os.Environ(), "GOWORK_PARSE_TARGET="+path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PowerShell could not parse %s: %v\n%s", name, err, out)
			}
		})
	}
}

func TestPowerShellInstallerWrappersForwardArgumentsAndExitCodes(t *testing.T) {
	shell := powershellExecutable(t)
	cases := []struct {
		name string
		mode string
	}{
		{name: "update.ps1", mode: "Update"},
		{name: "uninstall.ps1", mode: "Uninstall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(wrapperPowerShellPath(t, tc.name))
			if err != nil {
				t.Fatalf("read wrapper: %v", err)
			}

			dir := t.TempDir()
			wrapper := filepath.Join(dir, tc.name)
			if err := os.WriteFile(wrapper, data, 0o644); err != nil {
				t.Fatalf("write wrapper: %v", err)
			}
			argsPath := filepath.Join(t.TempDir(), "args.txt")
			if err := os.WriteFile(filepath.Join(dir, "install.ps1"), []byte(powershellWrapperStub), 0o644); err != nil {
				t.Fatalf("write stub installer: %v", err)
			}

			targetDir := filepath.Join(dir, "apps", "gowork")
			cmd := exec.Command(shell,
				"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", wrapper,
				"-Version", "v9.9.9", "-Dir", targetDir, "-NoModifyPath")
			cmd.Env = installerEnv(t.TempDir(), map[string]string{"GOWORK_STUB_ARGS": argsPath})
			out, err := cmd.CombinedOutput()
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("wrapper exit = %v, want exit code 42; output=%q", err, out)
			}
			if exitErr.ExitCode() != 42 {
				t.Fatalf("wrapper exit code = %d, want 42; output=%q", exitErr.ExitCode(), out)
			}

			recorded, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("stub did not run: %v", err)
			}
			text := strings.ReplaceAll(string(recorded), "\r\n", "\n")
			for _, want := range []string{
				"Update=" + boolLabel(tc.mode == "Update"),
				"Uninstall=" + boolLabel(tc.mode == "Uninstall"),
				"Version=v9.9.9",
				"Dir=" + targetDir,
				"NoModifyPath=True",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("install.ps1 arguments = %q, want it to contain %q", text, want)
				}
			}
		})
	}
}

func TestPowerShellInstallerWrappersResolveSymlinks(t *testing.T) {
	shell := powershellExecutable(t)
	cases := []struct {
		name string
		mode string
	}{
		{name: "update.ps1", mode: "Update"},
		{name: "uninstall.ps1", mode: "Uninstall"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(wrapperPowerShellPath(t, tc.name))
			if err != nil {
				t.Fatalf("read wrapper: %v", err)
			}

			root := t.TempDir()
			realDir := filepath.Join(root, "real")
			linkDir := filepath.Join(root, "bin")
			runDir := filepath.Join(root, "elsewhere")
			for _, dir := range []string{realDir, linkDir, runDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", dir, err)
				}
			}

			realWrapper := filepath.Join(realDir, tc.name)
			if err := os.WriteFile(realWrapper, data, 0o644); err != nil {
				t.Fatalf("write wrapper: %v", err)
			}
			argsPath := filepath.Join(root, "args.txt")
			if err := os.WriteFile(filepath.Join(realDir, "install.ps1"), []byte(powershellWrapperStub), 0o644); err != nil {
				t.Fatalf("write stub installer: %v", err)
			}

			link := filepath.Join(linkDir, "gowork-"+strings.TrimSuffix(tc.name, ".ps1")+".ps1")
			relativeTarget, err := filepath.Rel(linkDir, realWrapper)
			if err != nil {
				t.Fatalf("relative symlink target: %v", err)
			}
			if err := os.Symlink(relativeTarget, link); err != nil {
				t.Skipf("symlink not supported: %v", err)
			}

			targetDir := filepath.Join(root, "apps", "gowork")
			cmd := exec.Command(shell,
				"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", link,
				"-Version", "v9.9.9", "-Dir", targetDir, "-NoModifyPath")
			cmd.Dir = runDir
			cmd.Env = installerEnv(t.TempDir(), map[string]string{"GOWORK_STUB_ARGS": argsPath})
			out, err := cmd.CombinedOutput()
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("symlinked wrapper exit = %v, want exit code 42; output=%q", err, out)
			}
			if exitErr.ExitCode() != 42 {
				t.Fatalf("symlinked wrapper exit code = %d, want 42; output=%q", exitErr.ExitCode(), out)
			}

			recorded, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("stub did not run through the symlink: %v", err)
			}
			text := strings.ReplaceAll(string(recorded), "\r\n", "\n")
			for _, want := range []string{
				"Update=" + boolLabel(tc.mode == "Update"),
				"Uninstall=" + boolLabel(tc.mode == "Uninstall"),
				"Version=v9.9.9",
				"Dir=" + targetDir,
				"NoModifyPath=True",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("install.ps1 arguments = %q, want it to contain %q", text, want)
				}
			}
		})
	}
}

func boolLabel(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

// runPowerShellScript runs a PowerShell script in an isolated home directory.
func runPowerShellScript(t *testing.T, shell, script, home string, args ...string) (string, error) {
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

func TestWindowsInstallerWrappersRejectDangerousDirectories(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows wrappers delegate to install.ps1, which only runs on Windows")
	}
	shell := powershellExecutable(t)
	home := t.TempDir()
	sentinel := filepath.Join(home, "keep-me.txt")
	if err := os.WriteFile(sentinel, []byte("user data"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	for _, name := range []string{"update.ps1", "uninstall.ps1"} {
		t.Run(name, func(t *testing.T) {
			wrapper := wrapperPowerShellPath(t, name)
			output, err := runPowerShellScript(t, shell, wrapper, home, "-Dir", home)
			if err == nil {
				t.Fatalf("%s accepted the home directory: %s", name, output)
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("%s exit = %v, want code 1; output=%s", name, err, output)
			}
			if !strings.Contains(strings.ToLower(output), "home directory") {
				t.Fatalf("%s output = %q, want it to mention the home directory", name, output)
			}
		})
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel was removed by a rejected wrapper: %v", err)
	}
}
