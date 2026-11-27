// Package approot locates the directory that contains the running GoWork
// executable and resolves bundled assets (prompts, logo, helper scripts)
// relative to it.
//
// An installed GoWork keeps its assets next to the binary. A source checkout
// keeps them at the repository root instead, so lookups also fall back to the
// project directory and the current working directory.
package approot

import (
	"os"
	"path/filepath"
)

// ExecutableDir returns the real directory containing the running executable.
// Symlinks are resolved so a launcher such as ~/.local/bin/gowork still finds
// the bundle it points at.
func ExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Some platforms cannot evaluate symlinks for an executable that is
		// still perfectly usable. Keep the path os.Executable returned rather
		// than losing the only location we have.
		real = exe
	}
	return filepath.Dir(real), nil
}

// Roots returns the directories searched for bundled assets in priority
// order: the executable's directory, extra roots supplied by the caller, and
// the working directory. Empty and duplicate entries are dropped.
func Roots(extra ...string) []string {
	roots := make([]string, 0, len(extra)+2)
	if dir, err := ExecutableDir(); err == nil {
		roots = append(roots, dir)
	}
	roots = append(roots, extra...)
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	return dedupe(roots)
}

// FindFile returns the first regular file at rel under one of roots.
// Directories are skipped, so a bundle folder can never be mistaken for an
// asset. Callers normally pass Roots() so the app directory wins over the
// project and the working directory.
func FindFile(rel string, roots ...string) (string, bool) {
	for _, root := range roots {
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, rel)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// dedupe cleans paths and keeps the first occurrence of each.
func dedupe(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
