package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

func TestKnowledgeInsightsDerivesReviewSignalsAndAssociations(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	sourceID, _, planID, articlePath, articleID := compilePreviewForTest(t, dir, actor, "insights", "Decision evidence.", "", "insights-article")
	apply := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-article-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-insights-article-apply"})
	if !apply.OK {
		t.Fatalf("article apply failed: %+v", apply.Error)
	}

	sensitiveInput := filepath.Join(dir, "insights-sensitive.md")
	if err := os.WriteFile(sensitiveInput, []byte("sensitive decision source"), 0600); err != nil {
		t.Fatal(err)
	}
	sensitiveResponse := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-sensitive-ingest", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(sensitiveInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"sensitive"`)}, IdempotencyKey: "idem-insights-sensitive-ingest"})
	if !sensitiveResponse.OK {
		t.Fatalf("sensitive ingest failed: %+v", sensitiveResponse.Error)
	}
	sensitiveSourceID := sensitiveResponse.Data.(map[string]any)["source_id"].(string)

	forgottenInput := filepath.Join(dir, "insights-forgotten.md")
	if err := os.WriteFile(forgottenInput, []byte("forgotten decision source"), 0600); err != nil {
		t.Fatal(err)
	}
	forgottenResponse := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-forgotten-ingest", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(forgottenInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-insights-forgotten-ingest"})
	if !forgottenResponse.OK {
		t.Fatalf("forgotten ingest failed: %+v", forgottenResponse.Error)
	}
	forgottenSourceID := forgottenResponse.Data.(map[string]any)["source_id"].(string)

	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := "2030-01-01T00:00:00Z"
	seedInsightFact(t, store, "fact-stale", "project:stale", 1, "active", "stale", "Stale decision", sourceID, now)
	seedInsightFact(t, store, "fact-history", "project:history", 2, "active", "current", "Evolved decision", sourceID, now)
	seedInsightFact(t, store, "fact-retracted", "project:retracted", 2, "retracted", "current", "Retracted decision", sourceID, now)
	seedInsightFact(t, store, "fact-sensitive", "project:sensitive", 1, "active", "current", "Sensitive decision", sensitiveSourceID, now)
	seedInsightFact(t, store, "fact-forgotten", "project:forgotten", 1, "active", "current", "Forgotten decision", forgottenSourceID, now)
	if _, err := store.DB.Exec(`UPDATE fact_versions SET supersedes_fact_id = ? WHERE fact_id = ? AND version = ?`, "fact-stale", "fact-history", 2); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO extractions(extraction_id, source_id, source_hash, content_hash, path, extractor_version, prompt_hash, schema_version, strategy, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`, "ext-insights-missing", sourceID, "sha256:source", "sha256:missing", "extractions/missing.json", "test", "test", "1", "single", now); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE fact_versions SET extraction_id = ? WHERE fact_id = ? AND version = ?`, "ext-insights-missing", "fact-stale", 1); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE sources SET forgotten_at = ? WHERE source_id = ?`, now, forgottenSourceID); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO actions(action_id, kind, title, details, status, sensitivity, source_id, revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "action-insights", "task", "Review decision", "", "open", "normal", sourceID, 1, now, now); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := store.DB.Exec(`INSERT INTO actions(action_id, kind, title, details, status, sensitivity, source_id, revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, fmt.Sprintf("action-%03d-sensitive", i), "task", "Sensitive action", "", "open", "sensitive", sourceID, 1, now, now); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		articleID := fmt.Sprintf("article-sensitive-%03d", i)
		slug := fmt.Sprintf("000-sensitive-%03d", i)
		if _, err := store.DB.Exec(`INSERT INTO articles(article_id, slug, title, path, sensitivity, current_version, current_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, articleID, slug, "Sensitive article", "wiki/articles/"+slug+".md", "sensitive", 1, "sha256:invalid", now, now); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if _, err := store.DB.Exec(`INSERT INTO article_versions(article_id, version, content_hash, path, content, created_at) VALUES (?, ?, ?, ?, ?, ?)`, articleID, 1, "sha256:invalid", "wiki/articles/"+slug+".md", "invalid", now); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if _, err := store.DB.Exec(`INSERT INTO article_citations(article_id, version, source_id, locator) VALUES (?, ?, ?, ?)`, articleID, 1, sourceID, "sensitive evidence"); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	insights := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-query", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !insights.OK {
		t.Fatalf("insights failed: %+v", insights.Error)
	}
	data := insights.Data.(map[string]any)
	if data["count"] != float64(3) || data["total_count"] != float64(3) {
		t.Fatalf("insights counts = %+v", data)
	}
	decisions := data["decisions"].([]any)
	if decisions[0].(map[string]any)["fact_id"] != "fact-retracted" || decisions[1].(map[string]any)["fact_id"] != "fact-stale" || decisions[2].(map[string]any)["fact_id"] != "fact-history" {
		t.Fatalf("decision ordering = %+v", decisions)
	}
	if len(data["review_items"].([]any)) < 3 {
		t.Fatalf("review items = %+v", data["review_items"])
	}
	var unavailableEvidence, crossFactSupersession bool
	for _, raw := range data["review_items"].([]any) {
		item := raw.(map[string]any)
		if item["kind"] == "evidence_unavailable" && item["fact_id"] == "fact-stale" {
			unavailableEvidence = true
		}
		if item["kind"] == "superseded" && item["fact_id"] == "fact-stale" && item["superseded_by_fact_id"] == "fact-history" && item["superseded_by_version"] == float64(2) {
			crossFactSupersession = true
		}
	}
	if !unavailableEvidence || !crossFactSupersession {
		t.Fatalf("missing extraction or cross-fact review signal = %+v", data["review_items"])
	}
	first := decisions[0].(map[string]any)
	if len(first["articles"].([]any)) != 1 || len(first["actions"].([]any)) != 1 {
		t.Fatalf("associations = %+v", first)
	}
	action := first["actions"].([]any)[0].(map[string]any)
	if action["association"] != "shared_source" || action["action_id"] != "action-insights" {
		t.Fatalf("action association = %+v", action)
	}

	topic := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-topic", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{"topic": json.RawMessage(`"stale"`), "limit": json.RawMessage(`1`)}})
	if !topic.OK || topic.Data.(map[string]any)["count"] != float64(1) || topic.Data.(map[string]any)["decisions"].([]any)[0].(map[string]any)["fact_id"] != "fact-stale" {
		t.Fatalf("topic insight = %+v", topic)
	}

	if err := os.WriteFile(articlePath, append(mustReadInsightFile(t, articlePath), []byte("manual drift\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	drift := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-drift", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{"topic": json.RawMessage(`"retracted"`)}})
	if !drift.OK {
		t.Fatalf("drift insight failed: %+v", drift.Error)
	}
	driftDecision := drift.Data.(map[string]any)["decisions"].([]any)[0].(map[string]any)
	if driftDecision["articles"].([]any)[0].(map[string]any)["article_id"] != articleID || driftDecision["articles"].([]any)[0].(map[string]any)["drift"] != true {
		t.Fatalf("drift article = %+v", driftDecision["articles"])
	}

	sensitiveDefault := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-sensitive-default", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{"topic": json.RawMessage(`"sensitive"`)}})
	if !sensitiveDefault.OK || sensitiveDefault.Data.(map[string]any)["count"] != float64(0) {
		t.Fatalf("sensitive default = %+v", sensitiveDefault)
	}
	sensitiveAllowed := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-sensitive-allowed", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{"topic": json.RawMessage(`"sensitive"`)}, Options: map[string]json.RawMessage{"allow_sensitive": json.RawMessage(`true`)}})
	if !sensitiveAllowed.OK || sensitiveAllowed.Data.(map[string]any)["count"] != float64(1) {
		t.Fatalf("sensitive allowed = %+v", sensitiveAllowed)
	}
}

func TestKnowledgeInsightsCapabilityValidationAndReadOnlyBehavior(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	capabilities := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-capabilities", Operation: "system.capabilities", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !capabilities.OK {
		t.Fatalf("capabilities failed: %+v", capabilities.Error)
	}
	found := false
	for _, item := range capabilities.Data.(map[string]any)["operations"].([]any) {
		operation := item.(map[string]any)
		if operation["operation"] == "knowledge.insights" {
			found = true
			if operation["mutating"] != false {
				t.Fatalf("insights capability mutating flag = %+v", operation)
			}
		}
	}
	if !found {
		t.Fatal("knowledge.insights is missing from capabilities")
	}

	invalid := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-invalid", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{"limit": json.RawMessage(`51`)}})
	if invalid.OK || invalid.Error == nil || invalid.Error.Code != "REQUEST_INVALID" {
		t.Fatalf("invalid insights request = %+v", invalid)
	}

	insights := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-empty", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !insights.OK {
		t.Fatalf("empty insights failed: %+v", insights.Error)
	}
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&before); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	insights = runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-insights-empty-retry", Operation: "knowledge.insights", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !insights.OK {
		t.Fatalf("second empty insights failed: %+v", insights.Error)
	}
	store, err = storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var after int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("insights changed audit state: before=%d after=%d", before, after)
	}
}

func seedInsightFact(t *testing.T, store *storage.Storage, factID, factKey string, version int, status, freshness, text, sourceID, now string) {
	t.Helper()
	if version > 1 {
		if _, err := store.DB.Exec(`INSERT INTO facts(fact_id, fact_key, kind, current_version, status, freshness, created_at, updated_at) VALUES (?, ?, 'decision', ?, ?, ?, ?, ?)`, factID, factKey, version, status, freshness, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB.Exec(`INSERT INTO fact_versions(fact_id, version, text, status, created_at) VALUES (?, 1, ?, 'superseded', ?)`, factID, "Historical decision", now); err != nil {
			t.Fatal(err)
		}
	} else if _, err := store.DB.Exec(`INSERT INTO facts(fact_id, fact_key, kind, current_version, status, freshness, created_at, updated_at) VALUES (?, ?, 'decision', ?, ?, ?, ?, ?)`, factID, factKey, version, status, freshness, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO fact_versions(fact_id, version, text, status, created_at) VALUES (?, ?, ?, ?, ?)`, factID, version, text, status, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO fact_citations(fact_id, version, source_id, locator) VALUES (?, ?, ?, ?)`, factID, version, sourceID, "decision evidence"); err != nil {
		t.Fatal(err)
	}
}

func mustReadInsightFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
