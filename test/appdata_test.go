package test

import (
	"os"
	"path/filepath"
	"testing"

	gowork "GoWork/internal/gowork"
)

func TestAppDataRootIsExecutableDir(t *testing.T) {
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The app resolves the real path so a launcher symlink still points at
	// the install directory.
	if real, err := filepath.EvalSymlinks(want); err == nil {
		want = real
	}
	want = filepath.Dir(want)

	// Whatever directory the user is working in, GoWork's own data folder
	// must sit next to the executable, not in the project.
	got := gowork.AppDataRoot("/some/random/project")
	if got != want {
		t.Fatalf("appDataRoot = %q, want %q", got, want)
	}
}
