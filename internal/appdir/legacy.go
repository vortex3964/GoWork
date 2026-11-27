package appdir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"GoWork/internal/approot"
)

// MigrationResult describes what a legacy skills migration did. Paths are
// relative to the legacy skills root and use the host separator.
type MigrationResult struct {
	Copied  []string
	Skipped []string
}

// Migrated reports whether at least one entry was copied.
func (r MigrationResult) Migrated() bool { return len(r.Copied) > 0 }

// MigrateLegacySkills copies skills from the old executable-adjacent layout
// (<exeDir>/.GoWork/skills) into the durable user skills directory.
//
// Existing destination entries are never overwritten. Files are created with
// O_EXCL, so the migration is safe to run on every startup and race-safe if
// two instances start together. A missing legacy directory is a no-op. Only
// regular files and directories are copied; symlinks and other special files
// are reported in Skipped and left alone.
func MigrateLegacySkills(exeDir string) (MigrationResult, error) {
	var result MigrationResult
	exeDir = strings.TrimSpace(exeDir)
	if exeDir == "" {
		return result, nil
	}

	legacyRoot := filepath.Join(exeDir, ".GoWork", SkillsDirName)
	info, err := os.Stat(legacyRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("appdir: inspect legacy skills directory %s: %w", legacyRoot, err)
	}
	if !info.IsDir() {
		return result, nil
	}

	destRoot, err := SkillsDir()
	if err != nil {
		return result, err
	}

	legacyAbs, err := filepath.Abs(legacyRoot)
	if err != nil {
		return result, fmt.Errorf("appdir: resolve legacy skills directory: %w", err)
	}
	destAbs, err := filepath.Abs(destRoot)
	if err != nil {
		return result, fmt.Errorf("appdir: resolve user skills directory: %w", err)
	}
	if samePath(legacyAbs, destAbs) {
		return result, nil
	}

	err = filepath.WalkDir(legacyAbs, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if samePath(sourcePath, destAbs) && entry.IsDir() {
			// Never walk the destination if it happens to live below the old
			// executable directory (for example a portable install).
			return filepath.SkipDir
		}
		if sourcePath == legacyAbs {
			return nil
		}

		relative, err := filepath.Rel(legacyAbs, sourcePath)
		if err != nil {
			return fmt.Errorf("appdir: resolve legacy entry %s: %w", sourcePath, err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("appdir: legacy entry escapes the skills directory: %s", sourcePath)
		}
		targetPath := filepath.Join(destAbs, relative)

		entryInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("appdir: inspect legacy entry %s: %w", sourcePath, err)
		}

		if entryInfo.IsDir() {
			if err := ensureCopyDir(targetPath); err != nil {
				return err
			}
			return nil
		}
		if !entryInfo.Mode().IsRegular() {
			result.Skipped = append(result.Skipped, relative)
			return nil
		}

		copied, err := copyMissingFile(sourcePath, targetPath, copyMode(entryInfo.Mode()))
		if err != nil {
			return err
		}
		if copied {
			result.Copied = append(result.Copied, relative)
		} else {
			result.Skipped = append(result.Skipped, relative)
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("appdir: migrate legacy skills: %w", err)
	}

	sort.Strings(result.Copied)
	sort.Strings(result.Skipped)
	return result, nil
}

// MigrateLegacySkillsDefault migrates from the directory containing the
// running executable. It is the convenience entry point for startup wiring.
func MigrateLegacySkillsDefault() (MigrationResult, error) {
	exeDir, err := approot.ExecutableDir()
	if err != nil {
		return MigrationResult{}, fmt.Errorf("appdir: locate executable directory: %w", err)
	}
	return MigrateLegacySkills(exeDir)
}

func ensureCopyDir(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("appdir: cannot use symlink as a skills directory: %s", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("appdir: cannot create skills directory %s: path exists and is not a directory", path)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("appdir: inspect skills directory %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("appdir: create skills directory %s: %w", path, err)
	}
	return nil
}

// copyMissingFile copies source to target only when target does not exist.
// It returns false when the target already existed.
func copyMissingFile(sourcePath, targetPath string, mode os.FileMode) (bool, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return false, fmt.Errorf("appdir: open legacy file %s: %w", sourcePath, err)
	}
	defer source.Close()

	if err := ensureCopyDir(filepath.Dir(targetPath)); err != nil {
		return false, err
	}

	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("appdir: create migrated file %s: %w", targetPath, err)
	}

	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		_ = os.Remove(targetPath)
		return false, fmt.Errorf("appdir: copy legacy file %s: %w", sourcePath, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(targetPath)
		return false, fmt.Errorf("appdir: close migrated file %s: %w", targetPath, closeErr)
	}
	return true, nil
}

// copyMode keeps helper scripts executable while making user data private.
func copyMode(source os.FileMode) os.FileMode {
	if source.Perm()&0o111 != 0 {
		return 0o700
	}
	return 0o600
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
