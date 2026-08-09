package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppDataRootIsExecutableDir(t *testing.T) {
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Dir(want)

	// Whatever directory the user is working in, GoWork's own data folder
	// must sit next to the executable, not in the project.
	got := appDataRoot("/some/random/project")
	if got != want {
		t.Fatalf("appDataRoot = %q, want %q", got, want)
	}
}