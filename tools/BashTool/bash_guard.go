// DESC: blocked-command checks and project-root confinement for the bash tool.
// A shell subprocess gets a real filesystem path, so unlike the other tools
// (which read/write through os.Root) we have to validate everything ourselves
// before exec.Command ever sees it.
package bashtool

import (
	"fmt"
	"path/filepath"
	"strings"
)

// bannedCommands are the commands the model may never run. They are checked as
// whole tokens so short names ("ip", "nc", "su") don't false-positive on
// harmless words that merely contain them.
var bannedCommands = map[string]bool{
	// Network / download tools
	"alias":       true,
	"aria2c":      true,
	"axel":        true,
	"chrome":      true,
	"curl":        true,
	"curlie":      true,
	"firefox":     true,
	"http-prompt": true,
	"httpie":      true,
	"links":       true,
	"lynx":        true,
	"nc":          true,
	"safari":      true,
	"scp":         true,
	"ssh":         true,
	"telnet":      true,
	"w3m":         true,
	"wget":        true,
	"xh":          true,

	// System administration
	"doas": true,
	"su":   true,
	"sudo": true,

	// Package managers
	"apk":          true,
	"apt":          true,
	"apt-cache":    true,
	"apt-get":      true,
	"dnf":          true,
	"dpkg":         true,
	"emerge":       true,
	"home-manager": true,
	"makepkg":      true,
	"opkg":         true,
	"pacman":       true,
	"paru":         true,
	"pkg":          true,
	"pkg_add":      true,
	"pkg_delete":   true,
	"portage":      true,
	"rpm":          true,
	"yay":          true,
	"yum":          true,
	"zypper":       true,

	// System modification
	"at":        true,
	"batch":     true,
	"chkconfig": true,
	"crontab":   true,
	"fdisk":     true,
	"mkfs":      true,
	"mount":     true,
	"parted":    true,
	"service":   true,
	"systemctl": true,
	"umount":    true,

	// Network configuration
	"firewall-cmd": true,
	"ifconfig":     true,
	"ip":           true,
	"iptables":     true,
	"netstat":      true,
	"pfctl":        true,
	"route":        true,
	"ufw":          true,
}

// bannedSubstrings is a belt-and-suspenders pass over the raw command line.
// These names are distinctive enough that a plain substring match can't be a
// false positive, and it catches obfuscated calls the tokenizer misses, e.g.
// python -c "import os; os.system('sudo rm -rf /')".
var bannedSubstrings = []string{
	"aria2c", "apt-get", "curl", "firewall-cmd", "ifconfig", "iptables",
	"mkfs", "pacman", "systemctl", "sudo", "wget",
}

// blockRule is a phrase-level ban: head must appear as tokens in order, and
// if flags is non-empty at least one flag token must be present too. This
// pins package-manager installs while leaving the rest of the tool usable.
type blockRule struct {
	head  []string
	flags []string
}

// blockRules blocks command signatures we can't allow even though the bare
// command name is fine: installing packages or using `go test -exec` (which
// runs arbitrary commands).
var blockRules = []blockRule{
	{head: []string{"apk", "add"}},
	{head: []string{"apt", "install"}},
	{head: []string{"apt-get", "install"}},
	{head: []string{"brew", "install"}},
	{head: []string{"cargo", "install"}},
	{head: []string{"dnf", "install"}},
	{head: []string{"gem", "install"}},
	{head: []string{"go", "install"}},
	{head: []string{"go", "test"}, flags: []string{"-exec"}},
	{head: []string{"npm", "install"}, flags: []string{"-g", "--global"}},
	{head: []string{"pacman", "-S"}},
	{head: []string{"pip", "install"}, flags: []string{"--user"}},
	{head: []string{"pip3", "install"}, flags: []string{"--user"}},
	{head: []string{"pkg", "install"}},
	{head: []string{"pnpm", "add"}, flags: []string{"-g", "--global"}},
	{head: []string{"yarn", "global", "add"}},
	{head: []string{"yum", "install"}},
	{head: []string{"zypper", "install"}},
}

// tokenize turns a shell command line into bare words. It's deliberately lossy:
// we only need enough fidelity to recognize banned commands and install
// phrases, not to parse the shell.
func tokenize(command string) []string {
	replacer := strings.NewReplacer(
		"\n", " ",
		"\t", " ",
		";", " ",
		"|", " ",
		"&&", " ",
		"||", " ",
		"&", " ",
		"(", " ",
		")", " ",
		">", " ",
		"<", " ",
		"`", " ",
		"$", " ",
		"{", " ",
		"}", " ",
	)
	return strings.Fields(replacer.Replace(command))
}

func cleanToken(w string) string {
	return strings.ToLower(strings.Trim(w, `"'`))
}

func matchWord(tok, want string) bool {
	actual := cleanToken(tok)
	if strings.HasPrefix(want, "-") {
		return strings.HasPrefix(actual, want)
	}
	return actual == want
}

func matchesRule(tokens []string, rule blockRule) bool {
	start := 0
	for _, want := range rule.head {
		found := -1
		for i := start; i < len(tokens); i++ {
			if matchWord(tokens[i], want) {
				found = i
				break
			}
		}
		if found == -1 {
			return false
		}
		start = found + 1
	}
	if len(rule.flags) == 0 {
		return true
	}
	for _, tok := range tokens {
		for _, flag := range rule.flags {
			if matchWord(tok, flag) {
				return true
			}
		}
	}
	return false
}

// blockedIn returns a short description of why command isn't allowed, or ""
// if it may run. The caller turns any non-empty result into an error.
func blockedIn(command string) string {
	lower := strings.ToLower(command)
	for _, sub := range bannedSubstrings {
		if strings.Contains(lower, sub) {
			return sub
		}
	}

	tokens := tokenize(command)
	for _, tok := range tokens {
		if bannedCommands[cleanToken(tok)] {
			return cleanToken(tok)
		}
	}
	for _, rule := range blockRules {
		if matchesRule(tokens, rule) {
			return strings.Join(rule.head, " ")
		}
	}
	return ""
}

// resolveWorkingDir validates input.WorkingDir and returns the real, clean
// absolute directory the command should run in. Commands always run inside
// the project root: absolute paths, ".." traversal, and symlinks that point
// outside the root are all rejected.
func resolveWorkingDir(root, wd string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving project root: %w", err)
	}

	if wd == "" {
		return root, nil
	}
	if filepath.IsAbs(wd) {
		return "", fmt.Errorf("working_dir must stay inside the project root, absolute paths are not allowed: %q", wd)
	}
	clean := filepath.Clean(wd)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working_dir must stay inside the project root: %q", wd)
	}

	full := filepath.Join(root, clean)

	// Symlink escape check: a link inside the root pointing at a directory
	// outside it would hand bash an unrestricted filesystem. Resolve and
	// verify the real path is still beneath the root's real path.
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("working_dir %q: %w", wd, err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving project root: %w", err)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working_dir %q resolves outside the project root (%q)", wd, real)
	}
	return real, nil
}
