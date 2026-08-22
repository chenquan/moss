package system

import (
	"path/filepath"
	"testing"
)

func TestBackupPathAndArchiveEntryGuards(t *testing.T) {
	root := t.TempDir()
	if path, err := resolveBackupPath(root, "daily.moss-backup.zip", "backup-id"); err != nil || path != filepath.Join(root, "daily.moss-backup.zip") {
		t.Fatalf("valid backup path = %q, %v", path, err)
	}
	for _, value := range []string{"../outside.zip", filepath.Join(root, "..", "outside.zip"), "daily.tar", "nested/daily.zip"} {
		if _, err := resolveBackupPath(root, value, "backup-id"); err == nil {
			t.Fatalf("unsafe backup path accepted: %q", value)
		}
	}
	for _, value := range []string{"../database/moss.db", "/absolute", "raw/../wiki/a.md", "raw\\a"} {
		if !hasParentPath(value) && !filepath.IsAbs(value) && value != "raw\\a" {
			t.Fatalf("parent path was not detected: %q", value)
		}
	}
	for _, value := range []string{"database/moss.db", "raw/blobs/a.raw", "wiki/articles/a.md", "jobs/j/output.json", "trash/old.md"} {
		if !allowedBackupEntry(value) {
			t.Fatalf("allowed entry rejected: %q", value)
		}
	}
	for _, value := range []string{"manifest.json", "responses/a.json", "database/other.db", "raw"} {
		if allowedBackupEntry(value) {
			t.Fatalf("unsupported entry accepted: %q", value)
		}
	}
}
