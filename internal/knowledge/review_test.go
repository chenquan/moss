package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

func TestReviewScanReportsUnavailableFactEvidence(t *testing.T) {
	store := openReviewTestStore(t)
	defer store.Close()
	now := "2026-08-23T00:00:00Z"
	insertReviewFact(t, store, "source-a", "normal", now)
	if _, err := store.DB.Exec(`UPDATE sources SET forgotten_at = ? WHERE source_id = 'source-a'`, now); err != nil {
		t.Fatal(err)
	}

	value, codedErr := ReviewScan(context.Background(), store, reviewRequest())
	if codedErr != nil {
		t.Fatalf("review scan failed: %+v", codedErr)
	}
	data := value.(reviewData)
	if len(data.Items) != 1 || data.Items[0].Kind != "evidence_unavailable" || len(data.Items[0].SourceIDs) != 0 {
		t.Fatalf("unavailable evidence item = %+v", data.Items)
	}
}

func TestReviewScanOmitsSensitiveEvidenceWithoutPermission(t *testing.T) {
	store := openReviewTestStore(t)
	defer store.Close()
	insertReviewFact(t, store, "source-sensitive", "sensitive", "2026-08-23T00:00:00Z")

	value, codedErr := ReviewScan(context.Background(), store, reviewRequest())
	if codedErr != nil {
		t.Fatalf("review scan failed: %+v", codedErr)
	}
	data := value.(reviewData)
	if len(data.Items) != 0 {
		t.Fatalf("sensitive evidence leaked into review scan = %+v", data.Items)
	}
}

func TestReviewScanReportsCandidateTruncation(t *testing.T) {
	store := openReviewTestStore(t)
	defer store.Close()

	tx, err := store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxReviewCandidates; index++ {
		factID := fmt.Sprintf("fact-%04d", index)
		factKey, text := fmt.Sprintf("other:key-%04d", index), "other text"
		if index == maxReviewCandidates {
			factKey, text = "target:key", "target text"
		}
		if _, err := tx.Exec(`INSERT INTO facts(fact_id, fact_key, kind, current_version, status, freshness, created_at, updated_at) VALUES (?, ?, 'decision', 1, 'active', 'stale', ?, ?)`, factID, factKey, "2026-08-23T00:00:00Z", "2026-08-23T00:00:00Z"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO fact_versions(fact_id, version, text, status, created_at) VALUES (?, 1, ?, 'active', ?)`, factID, text, "2026-08-23T00:00:00Z"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	req := reviewRequest()
	req.Arguments = map[string]json.RawMessage{"topic": json.RawMessage(`"target"`)}
	value, codedErr := ReviewScan(context.Background(), store, req)
	if codedErr != nil {
		t.Fatalf("review scan failed: %+v", codedErr)
	}
	data := value.(reviewData)
	if len(data.Items) != 0 || !data.Truncated {
		t.Fatalf("review scan truncation = items=%d truncated=%v total=%d", len(data.Items), data.Truncated, data.TotalCount)
	}

	bundleValue, codedErr := ContextBundle(context.Background(), store, protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		Arguments:       map[string]json.RawMessage{"topic": json.RawMessage(`"target"`)},
	})
	if codedErr != nil {
		t.Fatalf("context bundle failed: %+v", codedErr)
	}
	if bundle := bundleValue.(contextBundleData); !bundle.Truncated {
		t.Fatal("context bundle did not preserve review scan truncation")
	}
}

func openReviewTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	t.Setenv("MOSS_DATA_DIR", t.TempDir())
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func insertReviewFact(t *testing.T, store *storage.Storage, sourceID, sensitivity, now string) {
	t.Helper()
	hash := "sha256:" + sourceID
	if _, err := store.DB.Exec(`INSERT INTO blobs(content_hash, byte_size, raw_path, created_at) VALUES (?, 1, ?, ?)`, hash, "raw/"+sourceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO sources(source_id, content_hash, source_type, sensitivity, byte_size, created_at) VALUES (?, ?, 'text', ?, 1, ?)`, sourceID, hash, sensitivity, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO facts(fact_id, fact_key, kind, current_version, status, freshness, created_at, updated_at) VALUES ('fact-review', 'review:key', 'decision', 1, 'active', 'stale', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO fact_versions(fact_id, version, text, status, created_at) VALUES ('fact-review', 1, 'review text', 'active', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO fact_citations(fact_id, version, source_id, locator) VALUES ('fact-review', 1, ?, 'line 1')`, sourceID); err != nil {
		t.Fatal(err)
	}
}

func reviewRequest() protocol.Request {
	return protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		RequestID:       "review-test",
		Operation:       "knowledge.review.scan",
		Arguments:       map[string]json.RawMessage{},
	}
}
