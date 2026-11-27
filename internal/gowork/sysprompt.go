package gowork

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const sys_prompt_path = "prompts/system_prompt.md"

const compaction_prompt_path = "prompts/compaction_prompt.md"

// inject_vars_in_sys_prompt reads prompts/system_prompt.md, substitutes the
// ${...} environment placeholders, and returns the rendered prompt.
// root is the already-resolved project root (only its last dir is used).
func inject_vars_in_sys_prompt(root string) (string, error) {
	data, err := os.ReadFile(sys_prompt_path)
	if err != nil {
		return "", err
	}

	prompt := string(data)

	projectRoot := ""
	if root != "" {
		projectRoot = filepath.Base(root)
	}
	prompt = strings.ReplaceAll(prompt, "${PROJECT_ROOT}", projectRoot)

	prompt = strings.ReplaceAll(prompt, "${PLATFORM}", shellOut("", "uname", "-s"))
	if shellOut(root, "git", "rev-parse", "--is-inside-work-tree") == "true" {
		prompt = strings.ReplaceAll(prompt, "${IS_GIT_REPO}", "yes")
	} else {
		prompt = strings.ReplaceAll(prompt, "${IS_GIT_REPO}", "no")
	}

	prompt = strings.ReplaceAll(prompt, "${PROJECT_TREE}", buildProjectTree(root))

	return prompt, nil
}

// buildProjectTree lists the project's source files (via rg --files, which
// respects .gitignore and .agentignore and skips hidden files like .env with
// their secrets) so the model sees exactly what exists and where, and stops
// guessing paths. Capped so a huge repo can't blow up the context.
func buildProjectTree(root string) string {
	if root == "" {
		return ""
	}
	fileExists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return "(ripgrep (rg) not found - use list_directory, file_search, and grep_search instead)"
	}
	rgArgs := []string{"--files", "--color=never", "--no-messages"}
	if ignore := filepath.Join(root, ".agentignore"); fileExists(ignore) {
		rgArgs = append(rgArgs, "--ignore-file", ignore)
	}
	rgArgs = append(rgArgs, root)

	out, err := exec.Command(rgPath, rgArgs...).Output()
	if err != nil {
		// exit 1 = no files; other errors just leave the tree empty.
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}

	const (
		maxFiles = 400
		maxBytes = 16 * 1024
	)
	var b strings.Builder
	truncated := false
	for i, l := range lines {
		if i >= maxFiles {
			truncated = true
			break
		}
		rel, err := filepath.Rel(root, l)
		if err != nil {
			rel = l
		}
		line := filepath.ToSlash(rel) + "\n"
		if b.Len()+len(line) > maxBytes {
			truncated = true
			break
		}
		b.WriteString(line)
	}
	if truncated {
		b.WriteString("... (tree truncated)")
	}
	return strings.TrimRight(b.String(), "\n")
}

// shellOut runs a command in dir (the current directory when empty) and
// returns its trimmed stdout, or "" on failure.
func shellOut(dir string, arg ...string) string {
	cmd := exec.Command(arg[0], arg[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
