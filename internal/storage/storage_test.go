package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsDefaultsToUserHomeMossDataRoot(t *testing.T) {
	chdirForTest(t, t.TempDir())
	t.Setenv("MOSS_DATA_DIR", "")
	t.Setenv("CAIRN_DATA_DIR", "")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(home, ".moss")
	if paths.Root != wantRoot {
		t.Fatalf("default root = %q, want %q", paths.Root, wantRoot)
	}
	if paths.Database != filepath.Join(wantRoot, "moss.db") {
		t.Fatalf("default database = %q", paths.Database)
	}
}

func TestSchemaV4UpgradeAddsKnowledgeCompilerState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOSS_DATA_DIR", dir)
	store, err := Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "moss.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET value = '4' WHERE key = 'schema_version'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var version string
	if err := upgraded.DB.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil || version != "5" {
		t.Fatalf("schema version = %q, err = %v", version, err)
	}
	var table string
	if err := upgraded.DB.QueryRow(`SELECT name FROM sqlite_master WHERE name = 'article_fts'`).Scan(&table); err != nil || table != "article_fts" {
		t.Fatalf("article index = %q, err = %v", table, err)
	}
}

func TestResolvePathsHonorsExplicitDataDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	chdirForTest(t, workingDirectory)
	override := filepath.Join(workingDirectory, "custom-data")
	t.Setenv("MOSS_DATA_DIR", override)
	t.Setenv("CAIRN_DATA_DIR", "")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != override {
		t.Fatalf("override root = %q, want %q", paths.Root, override)
	}
}

func TestResolvePathsIgnoresLegacyDataDirectory(t *testing.T) {
	workingDirectory := t.TempDir()
	chdirForTest(t, workingDirectory)
	legacy := filepath.Join(workingDirectory, "legacy-data")
	t.Setenv("MOSS_DATA_DIR", "")
	t.Setenv("CAIRN_DATA_DIR", legacy)

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(home, ".moss")
	if paths.Root != wantRoot {
		t.Fatalf("legacy override root = %q, want default %q", paths.Root, wantRoot)
	}
}

func chdirForTest(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
