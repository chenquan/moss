package safety

import (
	"testing"
	"time"
)

func TestSafetyHelperDeterminism(t *testing.T) {
	if got := uniqueStrings([]string{" privacy_impact ", "rollback", "rollback", ""}); len(got) != 2 || got[0] != "privacy_impact" || got[1] != "rollback" {
		t.Fatalf("uniqueStrings = %#v", got)
	}
	if got := placeholders(3); got != "?,?,?" {
		t.Fatalf("placeholders = %q", got)
	}
	if within("/var/lib/moss/wiki/articles/a.md", "/var/lib/moss/wiki/articles") != true {
		t.Fatal("within rejected a managed child")
	}
	if within("/var/lib/moss/wiki/articles-other/a.md", "/var/lib/moss/wiki/articles") {
		t.Fatal("within accepted a sibling directory")
	}
}

func TestSafetyExpiry(t *testing.T) {
	if !expired("not-a-timestamp") {
		t.Fatal("invalid timestamp should be expired")
	}
	if expired(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)) {
		t.Fatal("future timestamp should not be expired")
	}
	if !expired(time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)) {
		t.Fatal("past timestamp should be expired")
	}
}
