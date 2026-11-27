package gowork

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"GoWork/internal/approot"
)

// appRootDir resolves the directory the running binary lives in. The
// installed bundle keeps prompts/, logo/ and tools/ there, so asset lookups
// use it before any checkout paths.
func appRootDir() string {
	if dir, err := approot.ExecutableDir(); err == nil {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// appDataRoot is where GoWork keeps its own data (the .GoWork skills
// folder). This is the directory the app binary lives in, NOT the project
// the user happens to be working on, so a user's project is never touched
// by the app's internal data. Falls back to the working directory, then the
// project root, when the executable path can't be resolved.
func appDataRoot(projectRoot string) string {
	if dir, err := approot.ExecutableDir(); err == nil {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	if projectRoot != "" {
		return projectRoot
	}
	return "."
}

// findAsset resolves a bundled file against the app root first, then the
// working directory. The installed location wins so a stray prompts/ folder
// in the user's project can't replace the shipped prompt.
func findAsset(rel string) (string, error) {
	if path, ok := approot.FindFile(rel, approot.Roots()...); ok {
		return path, nil
	}
	return "", fmt.Errorf("%s not found next to the executable or in the working directory", filepath.ToSlash(rel))
}

// readAsset reads a bundled asset found by findAsset.
func readAsset(rel string) ([]byte, error) {
	path, err := findAsset(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func loadLogo() []string {
	data, err := readAsset("logo/logo.txt")
	if err != nil {
		return nil
	}
	// Trim a single trailing newline so we don't count a phantom blank
	// line when centering vertically.
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
