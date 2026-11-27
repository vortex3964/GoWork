package gowork

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"GoWork/tools"
)

const sys_prompt_path = "prompts/system_prompt.md"

const compaction_prompt_path = "prompts/compaction_prompt.md"

// inject_vars_in_sys_prompt reads prompts/system_prompt.md, substitutes the
// ${...} environment placeholders, and returns the rendered prompt.
// root is the already-resolved project root (only its last dir is used).
func inject_vars_in_sys_prompt(root string) (string, error) {
	data, err := readAsset(sys_prompt_path)
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

// buildProjectTree lists the project's source files (preferring rg --files,
// with a portable Go walker when ripgrep is unavailable) so the model sees
// exactly what exists and where, and stops guessing paths. Ignore files and
// hidden paths are honored so secrets such as .env stay out of the prompt.
// Capped so a huge repo can't blow up the context.
func buildProjectTree(root string) string {
	if root == "" {
		return ""
	}

	var lines []string
	haveTree := false

	if rgPath, err := exec.LookPath("rg"); err == nil {
		fileExists := func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		}
		rgArgs := []string{"--files", "--color=never", "--no-messages"}
		if ignorePath := filepath.Join(root, ".agentignore"); fileExists(ignorePath) {
			rgArgs = append(rgArgs, "--ignore-file", ignorePath)
		}
		rgArgs = append(rgArgs, root)

		if out, err := exec.Command(rgPath, rgArgs...).Output(); err == nil {
			lines = strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			haveTree = true
		}
	}

	if !haveTree {
		walked, err := walkProjectFiles(root)
		if err != nil {
			return fmt.Sprintf("(project tree unavailable: %v - use list_directory, file_search, and grep_search instead)", err)
		}
		lines = walked
	}

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

// walkProjectFiles is the portable buildProjectTree fallback. It walks root in
// lexical order, skips hidden paths and anything matched by .gitignore or
// .agentignore, and returns absolute file paths like rg --files does.
func walkProjectFiles(root string) ([]string, error) {
	ignores := tools.LoadIgnores(root)

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d == nil {
				return walkErr // the root itself is unreadable/missing
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}

		name := d.Name()
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		ignored := tools.IsIgnored(ignores, rel) || tools.IsIgnored(ignores, rel+"/")

		if d.IsDir() {
			if isHiddenTreeName(name) || ignored {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || isHiddenTreeName(name) || ignored {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func isHiddenTreeName(name string) bool {
	return len(name) > 1 && name[0] == '.'
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
