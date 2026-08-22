package knowledge

import "testing"

func TestNewBackfillIDIsDistinct(t *testing.T) {
	a, b := newBackfillID(), newBackfillID()
	if a == b {
		t.Fatalf("backfill IDs collided: %q", a)
	}
}
