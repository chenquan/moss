package action

import (
	"testing"
)

func TestDueNormalizationAndBuckets(t *testing.T) {
	if got, err := normalizeDue("2030-01-02"); err != nil || got != "2030-01-02" {
		t.Fatalf("date normalization = %q, %v", got, err)
	}
	if got, err := normalizeDue("2030-01-02T03:04:05+08:00"); err != nil || got != "2030-01-01T19:04:05Z" {
		t.Fatalf("timestamp normalization = %q, %v", got, err)
	}
	if dueBucket("2030-01-01", "2030-01-02") != "overdue" || dueBucket("2030-01-02", "2030-01-02") != "today" {
		t.Fatal("date bucket classification is wrong")
	}
}

func TestActionValidationAndOrdering(t *testing.T) {
	if !validStatus("in_progress") || validStatus("queued") || !terminal("done") || terminal("deferred") {
		t.Fatal("status helpers are wrong")
	}
	values := []actionRecord{{ActionID: "b", DueAt: "2030-01-02"}, {ActionID: "a", DueAt: "2030-01-01"}, {ActionID: "c"}}
	sortActions(values)
	if values[0].ActionID != "a" || values[1].ActionID != "b" || values[2].ActionID != "c" {
		t.Fatalf("ordered actions = %+v", values)
	}
	if got, err := boundedLimit(0); err != nil || got != defaultQueryLimit {
		t.Fatalf("default query limit = %d, %v", got, err)
	}
	if _, err := boundedLimit(maxQueryLimit + 1); err == nil || err.Code != "REQUEST_INVALID" {
		t.Fatalf("invalid query limit = %+v", err)
	}
}
