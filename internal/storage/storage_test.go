package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsDefaultsToUserHomeCairn(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CAIRN_DATA_DIR", "")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(home, ".cairn")
	if paths.Root != wantRoot {
		t.Fatalf("default root = %q, want %q", paths.Root, wantRoot)
	}
	if paths.Database != filepath.Join(wantRoot, "cairn.db") {
		t.Fatalf("default database = %q", paths.Database)
	}
}

func TestResolvePathsHonorsExplicitDataDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	t.Chdir(workingDirectory)
	override := filepath.Join(workingDirectory, "custom-data")
	t.Setenv("CAIRN_DATA_DIR", override)

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != override {
		t.Fatalf("override root = %q, want %q", paths.Root, override)
	}
}
