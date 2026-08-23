package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

func TestActionResultConfirmationAndReadLoop(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	create := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-create", Operation: "action.create.plan", Actor: actor, Arguments: map[string]json.RawMessage{"kind": json.RawMessage(`"task"`), "title": json.RawMessage(`"Record outcome"`)}, IdempotencyKey: "idem-result-create"})
	if !create.OK {
		t.Fatalf("create plan failed: %+v", create.Error)
	}
	createData := create.Data.(map[string]any)
	actionID := createData["action_id"].(string)
	apply := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-create-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(createData["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-result-create-apply"})
	if !apply.OK {
		t.Fatalf("create apply failed: %+v", apply.Error)
	}
	resultPlan := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-plan", Operation: "action.result.plan", Actor: actor, Arguments: map[string]json.RawMessage{"action_id": json.RawMessage(mustJSON(actionID)), "status": json.RawMessage(`"succeeded"`), "summary": json.RawMessage(`"The work completed"`), "metadata": json.RawMessage(`{"attempt":1}`)}, IdempotencyKey: "idem-result-plan"})
	if !resultPlan.OK {
		t.Fatalf("result plan failed: %+v", resultPlan.Error)
	}
	resultPlanData := resultPlan.Data.(map[string]any)
	store := openTestStore(t, dir)
	var before int
	if err := store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM action_results`).Scan(&before); err != nil || before != 0 {
		t.Fatalf("result mutated at plan time: count=%d err=%v", before, err)
	}
	_ = store.Close()

	missingConfirmation := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-missing-confirm", Operation: "action.result.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(resultPlanData["plan_id"].(string)))}, IdempotencyKey: "idem-result-missing-confirm"})
	if missingConfirmation.OK || missingConfirmation.Error == nil || missingConfirmation.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("missing result confirmation = %+v", missingConfirmation)
	}
	resultApply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-apply", Operation: "action.result.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(resultPlanData["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-result-apply"}
	applied := runRequest(t, dir, resultApply)
	if !applied.OK {
		t.Fatalf("result apply failed: %+v", applied.Error)
	}
	if applied.Data.(map[string]any)["action_status"] != "open" {
		t.Fatalf("result changed action lifecycle: %+v", applied.Data)
	}
	retry := resultApply
	retry.RequestID = "req-result-apply-retry"
	if response := runRequest(t, dir, retry); !response.OK || response.Data.(map[string]any)["relation_id"] != applied.Data.(map[string]any)["relation_id"] {
		t.Fatalf("result retry = %+v", response)
	}

	store = openTestStore(t, dir)
	defer store.Close()
	var status string
	var revision, resultCount, producesCount int
	if err := store.DB.QueryRow(`SELECT status, revision FROM actions WHERE action_id = ?`, actionID).Scan(&status, &revision); err != nil {
		t.Fatal(err)
	}
	if status != "open" || revision != 1 {
		t.Fatalf("action lifecycle after result = %s/%d", status, revision)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM action_results WHERE action_id = ?`, actionID).Scan(&resultCount); err != nil || resultCount != 1 {
		t.Fatalf("result count = %d err=%v", resultCount, err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM relations WHERE relation_type = 'produces' AND from_id = ?`, actionID).Scan(&producesCount); err != nil || producesCount != 1 {
		t.Fatalf("produces count = %d err=%v", producesCount, err)
	}

	review := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-review", Operation: "knowledge.review.scan", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !review.OK || review.Data.(map[string]any)["count"] != float64(1) || review.Data.(map[string]any)["items"].([]any)[0].(map[string]any)["kind"] != "result_feedback_gap" {
		t.Fatalf("result review = %+v", review)
	}
	bundle := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-context", Operation: "knowledge.context.bundle", Actor: actor, Arguments: map[string]json.RawMessage{"limit": json.RawMessage(`10`)}})
	if !bundle.OK {
		t.Fatalf("context bundle failed: %+v", bundle.Error)
	}
	bundleData := bundle.Data.(map[string]any)
	if len(bundleData["actions"].([]any)) != 1 || len(bundleData["results"].([]any)) != 1 || len(bundleData["relations"].([]any)) != 1 {
		t.Fatalf("context bundle entities = %+v", bundleData)
	}
	secondPlan := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-plan-2", Operation: "action.result.plan", Actor: actor, Arguments: map[string]json.RawMessage{"action_id": json.RawMessage(mustJSON(actionID)), "status": json.RawMessage(`"partial"`), "summary": json.RawMessage(`"A follow-up attempt was partial"`)}, IdempotencyKey: "idem-result-plan-2"})
	if !secondPlan.OK || secondPlan.Data.(map[string]any)["result_version"] != float64(2) {
		t.Fatalf("second result plan version = %+v", secondPlan)
	}
	secondPlanData := secondPlan.Data.(map[string]any)
	secondApply := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-result-apply-2", Operation: "action.result.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(secondPlanData["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-result-apply-2"})
	if !secondApply.OK || secondApply.Data.(map[string]any)["result"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("second result apply = %+v", secondApply)
	}
}

func openTestStore(t *testing.T, dir string) *storage.Storage {
	t.Helper()
	t.Setenv("MOSS_DATA_DIR", dir+"/data")
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
