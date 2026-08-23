package knowledge

import (
	"context"
	"database/sql"
	"testing"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

func TestExplicitRelationValidationAndUniqueness(t *testing.T) {
	t.Setenv("MOSS_DATA_DIR", t.TempDir())
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := "2026-08-23T00:00:00Z"
	for _, hash := range []string{"sha256:source-a", "sha256:source-b"} {
		if _, err := store.DB.Exec(`INSERT INTO blobs(content_hash, byte_size, raw_path, created_at) VALUES (?, 1, ?, ?)`, hash, "raw/"+hash[len(hash)-1:], now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB.Exec(`INSERT INTO sources(source_id, content_hash, source_type, sensitivity, byte_size, created_at) VALUES ('source-a', 'sha256:source-a', 'text', 'normal', 1, ?), ('source-b', 'sha256:source-b', 'text', 'sensitive', 1, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO actions(action_id, kind, title, details, status, sensitivity, revision, created_at, updated_at) VALUES ('action-a', 'task', 'Task', '', 'open', 'normal', 1, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"source-a": true, "source-b": true}
	relation, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointSource, ID: "source-a"}, To: RelationEndpoint{Type: EndpointAction, ID: "action-a"}, SourceID: "source-a"}, allowed, false)
	if codedErr != nil {
		t.Fatalf("valid relation rejected: %+v", codedErr)
	}
	if _, err := store.DB.Exec(`INSERT INTO action_results(result_id, action_id, version, status, summary, source_id, sensitivity, created_at) VALUES ('result-a', 'action-a', 1, 'succeeded', 'done', 'source-a', 'normal', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointActionResult, ID: "result-a", Version: 1}, To: RelationEndpoint{Type: EndpointSource, ID: "source-a"}}, allowed, false); codedErr != nil {
		t.Fatalf("action result relation rejected: %+v", codedErr)
	}
	tx, err := store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, codedErr := InsertRelationTx(context.Background(), tx, relation, "test", "test-plan", now); codedErr != nil {
		t.Fatalf("insert relation failed: %+v", codedErr)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, codedErr := InsertRelationTxForTest(context.Background(), store.DB, relation, now); codedErr == nil || codedErr.Code != "RELATION_DUPLICATE" {
		t.Fatalf("duplicate relation = %+v", codedErr)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointAction, ID: "action-a", Version: 2}, To: RelationEndpoint{Type: EndpointSource, ID: "source-a"}}, allowed, true); codedErr == nil || codedErr.Code != "RELATION_VERSION_STALE" {
		t.Fatalf("stale action relation = %+v", codedErr)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointSource, ID: "source-b"}, To: RelationEndpoint{Type: EndpointAction, ID: "action-a"}}, allowed, false); codedErr == nil || codedErr.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("sensitive relation = %+v", codedErr)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointSource, ID: "source-a"}, To: RelationEndpoint{Type: EndpointAction, ID: "action-a"}, SourceID: "source-b"}, allowed, true); codedErr == nil || codedErr.Code != "SENSITIVITY_ESCALATION_REQUIRED" {
		t.Fatalf("sensitive relation provenance = %+v", codedErr)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointSource, ID: "source-b"}, To: RelationEndpoint{Type: EndpointAction, ID: "action-a"}}, map[string]bool{"source-a": true}, true); codedErr == nil || codedErr.Code != "SOURCE_REFERENCE_INVALID" {
		t.Fatalf("cross-job relation source = %+v", codedErr)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointSource, ID: "source-a"}, To: RelationEndpoint{Type: EndpointSource, ID: "source-a"}}, allowed, true); codedErr == nil || codedErr.Code != "RELATION_SELF_REFERENCE" {
		t.Fatalf("self relation = %+v", codedErr)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: "contradicts", From: RelationEndpoint{Type: "unknown", ID: "x"}, To: RelationEndpoint{Type: EndpointSource, ID: "source-a"}}, allowed, true); codedErr == nil || codedErr.Code != "RELATION_ENDPOINT_TYPE_INVALID" {
		t.Fatalf("invalid endpoint = %+v", codedErr)
	}
	if _, err := store.DB.Exec(`UPDATE sources SET forgotten_at = ? WHERE source_id = 'source-a'`, now); err != nil {
		t.Fatal(err)
	}
	if _, codedErr := ValidateRelation(context.Background(), store.DB, RelationInput{RelationType: RelationSupports, From: RelationEndpoint{Type: EndpointSource, ID: "source-a"}, To: RelationEndpoint{Type: EndpointAction, ID: "action-a"}}, allowed, true); codedErr == nil || codedErr.Code != "RELATION_ENDPOINT_NOT_FOUND" {
		t.Fatalf("forgotten endpoint = %+v", codedErr)
	}
}

func InsertRelationTxForTest(ctx context.Context, db *sql.DB, relation ResolvedRelation, now string) (string, *protocol.CodedError) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", protocol.NewCodedError("STORAGE_UNHEALTHY", "test transaction failed", true, nil)
	}
	defer tx.Rollback()
	id, codedErr := InsertRelationTx(ctx, tx, relation, "test", "test-plan-duplicate", now)
	if codedErr == nil {
		_ = tx.Commit()
	}
	return id, codedErr
}
