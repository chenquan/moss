package compile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenquan/moss/internal/knowledge"
	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

type multiArticleOperation struct {
	Operation   string     `json:"operation"`
	ArticleID   string     `json:"article_id,omitempty"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Summary     string     `json:"summary"`
	Body        string     `json:"body"`
	Sensitivity string     `json:"sensitivity"`
	Tags        []string   `json:"tags"`
	SourceIDs   []string   `json:"source_ids"`
	Citations   []citation `json:"citations"`
}

type multiFactOperation struct {
	Operation      string   `json:"operation"`
	FactID         string   `json:"fact_id,omitempty"`
	FactKey        string   `json:"fact_key"`
	Kind           string   `json:"kind"`
	Text           string   `json:"text"`
	Status         string   `json:"status"`
	SourceIDs      []string `json:"source_ids"`
	ExtractionID   string   `json:"extraction_id,omitempty"`
	SupersedesFact string   `json:"supersedes_fact_id,omitempty"`
	ReviewAfter    string   `json:"review_after,omitempty"`
}

type multiWriteCandidate struct {
	Articles  []multiArticleOperation   `json:"articles"`
	Facts     []multiFactOperation      `json:"facts"`
	Relations []knowledge.RelationInput `json:"relations"`
}

type batchPlanResponse struct {
	PlanID        string         `json:"plan_id"`
	JobID         string         `json:"job_id"`
	State         string         `json:"state"`
	ArticleCount  int            `json:"article_count"`
	FactCount     int            `json:"fact_count"`
	RelationCount int            `json:"relation_count"`
	ExpiresAt     string         `json:"expires_at"`
	RiskFlags     []string       `json:"risk_flags"`
	Diff          map[string]any `json:"diff"`
}

type batchArticleRecord struct {
	Ordinal         int
	Operation       string
	ArticleID       string
	Slug            string
	Path            string
	BaseVersion     int
	BaseHash        string
	ProposedVersion int
	ProposedHash    string
	ProposedContent string
	PreviousContent sql.NullString
	Diff            multiArticleOperation
}

type batchFactRecord struct {
	Ordinal             int      `json:"ordinal"`
	Operation           string   `json:"operation"`
	FactID              string   `json:"fact_id"`
	FactKey             string   `json:"fact_key"`
	Kind                string   `json:"kind"`
	BaseVersion         int      `json:"base_version"`
	Text                string   `json:"text"`
	Status              string   `json:"status"`
	ExtractionID        string   `json:"extraction_id"`
	SourceIDs           []string `json:"source_ids"`
	SupersedesID        string   `json:"supersedes_fact_id"`
	ReviewAfter         string   `json:"review_after"`
	PreviousReviewAfter string   `json:"previous_review_after"`
	PreviousFreshness   string   `json:"previous_freshness"`
}

type batchRelationRecord struct {
	Ordinal      int
	RelationID   string
	RelationType string
	From         knowledge.RelationEndpoint
	To           knowledge.RelationEndpoint
	SourceID     string
}

type batchPlanRecord struct {
	ID        string
	JobID     string
	State     string
	BaseHash  string
	Expires   string
	Articles  []batchArticleRecord
	Facts     []batchFactRecord
	Relations []batchRelationRecord
	RiskFlags []string
	Diff      map[string]any
}

func previewBatch(ctx context.Context, store *storage.Storage, req protocol.Request, job jobResponse) (protocol.Response, *protocol.CodedError) {
	var resultPath, recordedHash string
	if err := store.DB.QueryRowContext(ctx, `SELECT result_path, COALESCE(result_hash, '') FROM compile_stages WHERE job_id = ? AND stage = 'write'`, job.JobID).Scan(&resultPath, &recordedHash); errors.Is(err, sql.ErrNoRows) {
		return protocol.Response{}, protocol.NewCodedError("STAGE_NOT_FOUND", "write stage was not found", false, nil)
	} else if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read batch write stage", true, nil)
	}
	contents, err := readStageResult(absoluteRef(store.Paths.Root, resultPath))
	if err != nil || recordedHash == "" || hashBytes(contents) != recordedHash {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_CHANGED", "batch write stage result is unavailable or changed", false, nil)
	}
	if err := ValidateMulti("write", contents); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "batch write result does not satisfy its JSON Schema", false, err.Error())
	}
	var candidate multiWriteCandidate
	if err := json.Unmarshal(contents, &candidate); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "batch write result cannot be decoded", false, nil)
	}
	if len(candidate.Articles) == 0 && len(candidate.Facts) == 0 && len(candidate.Relations) == 0 {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "batch write result must contain an article, fact, or relation operation", false, nil)
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	allowed := map[string]bool{}
	for _, id := range job.SourceIDs {
		allowed[id] = true
	}
	for _, article := range candidate.Articles {
		if err := validateBatchArticle(article, allowed); err != nil {
			return protocol.Response{}, err
		}
	}
	articleTargets := map[string]bool{}
	for _, article := range candidate.Articles {
		key := article.ArticleID
		if key == "" {
			key = "slug:" + article.Slug
		}
		if articleTargets[key] {
			return protocol.Response{}, protocol.NewCodedError("ARTICLE_TARGET_DUPLICATE", "batch contains duplicate article target", false, map[string]any{"target": key})
		}
		articleTargets[key] = true
	}
	for _, fact := range candidate.Facts {
		if err := validateBatchFact(fact, allowed); err != nil {
			return protocol.Response{}, err
		}
	}
	factTargets := map[string]bool{}
	for _, fact := range candidate.Facts {
		key := fact.Kind + "\x00" + fact.FactKey
		if fact.FactID != "" {
			key = "id:" + fact.FactID
		}
		if factTargets[key] {
			return protocol.Response{}, protocol.NewCodedError("FACT_TARGET_DUPLICATE", "batch contains duplicate fact target", false, map[string]any{"target": key})
		}
		factTargets[key] = true
	}
	planID := newID("batch")
	expires := time.Now().UTC().Add(planLifetime).Format(time.RFC3339Nano)
	baseHash := hashBytes(contents)
	articles, facts, relations, risks, diff, codedErr := buildBatchRecords(ctx, store, candidate, planID, allowed, allowSensitive)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	responseData := batchPlanResponse{PlanID: planID, JobID: job.JobID, State: "pending", ArticleCount: len(articles), FactCount: len(facts), RelationCount: len(relations), ExpiresAt: expires, RiskFlags: risks, Diff: diff}
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, _ := json.Marshal(response)
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin batch preview", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve batch preview idempotency", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	diffJSON, _ := json.Marshal(diff)
	riskJSON, _ := json.Marshal(risks)
	if _, err := tx.ExecContext(ctx, `INSERT INTO compile_batch_plans(plan_id, job_id, state, base_hash, diff_json, risk_json, expires_at, created_at) VALUES (?, ?, 'pending', ?, ?, ?, ?, ?)`, planID, job.JobID, baseHash, diffJSON, riskJSON, expires, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot create batch preview plan", err)
	}
	for _, article := range articles {
		itemJSON, _ := json.Marshal(article.Diff)
		if _, err := tx.ExecContext(ctx, `INSERT INTO compile_batch_articles(plan_id, ordinal, operation, article_id, slug, base_version, base_hash, proposed_version, proposed_hash, proposed_content, previous_content, diff_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, planID, article.Ordinal, article.Operation, nullableText(article.ArticleID), article.Slug, article.BaseVersion, nullableText(article.BaseHash), article.ProposedVersion, article.ProposedHash, article.ProposedContent, nullableNullString(article.PreviousContent), itemJSON); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot record batch article", err)
		}
	}
	for _, fact := range facts {
		itemJSON, _ := json.Marshal(fact)
		if _, err := tx.ExecContext(ctx, `INSERT INTO compile_batch_facts(plan_id, ordinal, operation, fact_id, fact_key, kind, base_version, text, status, extraction_id, review_after, previous_review_after, previous_freshness, diff_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)`, planID, fact.Ordinal, fact.Operation, nullableText(fact.FactID), fact.FactKey, fact.Kind, fact.BaseVersion, fact.Text, fact.Status, nullableText(fact.ExtractionID), fact.ReviewAfter, fact.PreviousReviewAfter, fact.PreviousFreshness, itemJSON); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot record batch fact", err)
		}
	}
	for _, relation := range relations {
		diffJSON, _ := json.Marshal(relation)
		if _, err := tx.ExecContext(ctx, `INSERT INTO compile_batch_relations(plan_id, ordinal, relation_id, relation_type, from_type, from_id, from_version, to_type, to_id, to_version, source_id, diff_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`, planID, relation.Ordinal, relation.RelationID, relation.RelationType, relation.From.Type, relation.From.ID, relation.From.Version, relation.To.Type, relation.To.ID, relation.To.Version, relation.SourceID, diffJSON); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot record batch relation", err)
		}
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": planID, "job_id": job.JobID, "articles": len(articles), "facts": len(facts), "relations": len(relations)}); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot audit batch preview", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot complete batch preview idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot commit batch preview", err)
	}
	return response, nil
}

func batchWriteForJob(ctx context.Context, store *storage.Storage, jobID string) (bool, *protocol.CodedError) {
	var path string
	if err := store.DB.QueryRowContext(ctx, `SELECT result_path FROM compile_stages WHERE job_id = ? AND stage = 'write'`, jobID).Scan(&path); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, ProtocolStorageError("cannot inspect batch write stage", err)
	}
	contents, err := readStageResult(absoluteRef(store.Paths.Root, path))
	if err != nil {
		return false, nil
	}
	return isBatchWritePayload(contents), nil
}

func buildBatchRecords(ctx context.Context, store *storage.Storage, candidate multiWriteCandidate, planID string, allowedSources map[string]bool, allowSensitive bool) ([]batchArticleRecord, []batchFactRecord, []batchRelationRecord, []string, map[string]any, *protocol.CodedError) {
	articles := make([]batchArticleRecord, 0, len(candidate.Articles))
	risks := []string{}
	diff := map[string]any{"articles": []any{}, "facts": []any{}, "relations": []any{}}
	for ordinal, op := range candidate.Articles {
		if op.Operation == "create" && op.ArticleID != "" {
			return nil, nil, nil, nil, nil, protocol.NewCodedError("ARTICLE_ID_INVALID", "create operation cannot specify article_id", false, nil)
		}
		article, existing, err := findBatchArticle(ctx, store, op)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if op.Operation != "create" && !existing {
			return nil, nil, nil, nil, nil, protocol.NewCodedError("ARTICLE_NOT_FOUND", "batch article target was not found", false, map[string]any{"article_id": op.ArticleID})
		}
		if err := validateBatchSourceSensitivity(ctx, store, op.SourceIDs, op.Sensitivity); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		version := 1
		baseVersion, baseHash := 0, ""
		if existing {
			baseVersion, baseHash = article.Version, article.Hash
			version = article.Version + 1
			risks = append(risks, "overwrite")
		}
		if op.Operation == "retract" {
			op.Body = "This article has been retracted.\n"
			risks = append(risks, "retraction")
		}
		content, renderErr := renderArticle(article, writeCandidate{Title: op.Title, Slug: op.Slug, Summary: op.Summary, Body: op.Body, Sensitivity: op.Sensitivity, Tags: op.Tags, SourceIDs: op.SourceIDs, Citations: op.Citations}, version)
		if renderErr != nil {
			return nil, nil, nil, nil, nil, protocol.NewCodedError("INTERNAL_ERROR", "cannot render batch article", false, nil)
		}
		record := batchArticleRecord{Ordinal: ordinal, Operation: op.Operation, ArticleID: article.ID, Slug: article.Slug, Path: article.Path, BaseVersion: baseVersion, BaseHash: baseHash, ProposedVersion: version, ProposedHash: hashBytes([]byte(content)), ProposedContent: content, Diff: op}
		if existing {
			previous, err := articlePreviousContent(store, article)
			if err != nil {
				return nil, nil, nil, nil, nil, protocol.NewCodedError("WIKI_DRIFT", "batch article target is drifted", false, nil)
			}
			record.PreviousContent = sql.NullString{String: previous, Valid: true}
		}
		articles = append(articles, record)
		diff["articles"] = append(diff["articles"].([]any), map[string]any{"operation": op.Operation, "article_id": article.ID, "slug": article.Slug, "base_version": baseVersion, "proposed_version": version, "hash": record.ProposedHash})
	}
	facts := make([]batchFactRecord, 0, len(candidate.Facts))
	for ordinal, op := range candidate.Facts {
		fact, err := findBatchFact(ctx, store, op)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		baseVersion := 0
		factID := op.FactID
		if factID == "" && fact.ID != "" {
			factID = fact.ID
		}
		if fact.ID != "" {
			baseVersion = fact.Version
			if op.Operation == "create" {
				return nil, nil, nil, nil, nil, protocol.NewCodedError("FACT_CONFLICT", "fact key already exists", false, nil)
			}
		}
		if op.Operation != "create" && fact.ID == "" {
			return nil, nil, nil, nil, nil, protocol.NewCodedError("FACT_NOT_FOUND", "batch fact target was not found", false, nil)
		}
		if err := validateBatchProvenance(ctx, store, op); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if err := validateBatchSourceSensitivity(ctx, store, op.SourceIDs, "normal"); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		status := op.Status
		if op.Operation == "retract" {
			status = "retracted"
			risks = append(risks, "fact_retraction")
		}
		if factID == "" {
			factID = newID("fact")
		}
		record := batchFactRecord{Ordinal: ordinal, Operation: op.Operation, FactID: factID, FactKey: op.FactKey, Kind: op.Kind, BaseVersion: baseVersion, Text: op.Text, Status: status, ExtractionID: op.ExtractionID, SourceIDs: op.SourceIDs, SupersedesID: op.SupersedesFact, ReviewAfter: op.ReviewAfter, PreviousReviewAfter: fact.ReviewAfter, PreviousFreshness: fact.Freshness}
		facts = append(facts, record)
		diff["facts"] = append(diff["facts"].([]any), map[string]any{"operation": op.Operation, "fact_id": factID, "fact_key": op.FactKey, "base_version": baseVersion, "status": status, "review_after": op.ReviewAfter})
	}
	relations := make([]batchRelationRecord, 0, len(candidate.Relations))
	seenRelations := make(map[string]bool, len(candidate.Relations))
	for ordinal, input := range candidate.Relations {
		relation, relationErr := knowledge.ValidateRelation(ctx, store.DB, input, allowedSources, allowSensitive)
		if relationErr != nil {
			return nil, nil, nil, nil, nil, relationErr
		}
		key := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%d", relation.RelationType, relation.From.Type, relation.From.ID, relation.From.Version, relation.To.Type, relation.To.ID, relation.To.Version)
		if seenRelations[key] {
			return nil, nil, nil, nil, nil, protocol.NewCodedError("RELATION_DUPLICATE", "batch contains duplicate relation", false, nil)
		}
		seenRelations[key] = true
		var existing string
		if err := store.DB.QueryRowContext(ctx, `SELECT relation_id FROM relations WHERE relation_type = ? AND from_type = ? AND from_id = ? AND from_version = ? AND to_type = ? AND to_id = ? AND to_version = ?`, relation.RelationType, relation.From.Type, relation.From.ID, relation.From.Version, relation.To.Type, relation.To.ID, relation.To.Version).Scan(&existing); err == nil {
			return nil, nil, nil, nil, nil, protocol.NewCodedError("RELATION_DUPLICATE", "relation already exists", false, nil)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, nil, nil, ProtocolStorageError("cannot check relation uniqueness", err)
		}
		record := batchRelationRecord{Ordinal: ordinal, RelationID: knowledge.NewRelationID(), RelationType: relation.RelationType, From: relation.From, To: relation.To, SourceID: relation.SourceID}
		relations = append(relations, record)
		diff["relations"] = append(diff["relations"].([]any), map[string]any{"relation_id": record.RelationID, "relation_type": record.RelationType, "from": record.From, "to": record.To, "source_id": record.SourceID})
		risks = append(risks, "explicit_relation")
	}
	return articles, facts, relations, uniqueStrings(risks), diff, nil
}

func validateBatchSourceSensitivity(ctx context.Context, store *storage.Storage, sourceIDs []string, target string) *protocol.CodedError {
	for _, sourceID := range sourceIDs {
		var sensitivity string
		if err := store.DB.QueryRowContext(ctx, `SELECT sensitivity FROM sources WHERE source_id = ? AND forgotten_at IS NULL`, sourceID).Scan(&sensitivity); err != nil {
			return ProtocolStorageError("cannot read batch source sensitivity", err)
		}
		if sensitivityRank(target) < sensitivityRank(sensitivity) {
			return protocol.NewCodedError("SENSITIVITY_INVALID", "batch content sensitivity is below a cited source sensitivity", false, map[string]any{"source_id": sourceID})
		}
	}
	return nil
}

func validateBatchProvenance(ctx context.Context, store *storage.Storage, op multiFactOperation) *protocol.CodedError {
	if op.ExtractionID != "" {
		var sourceID, status string
		if err := store.DB.QueryRowContext(ctx, `SELECT source_id, status FROM extractions WHERE extraction_id = ?`, op.ExtractionID).Scan(&sourceID, &status); err != nil || status != "active" {
			return protocol.NewCodedError("PROVENANCE_INVALID", "fact extraction_id is unavailable or inactive", false, nil)
		}
		found := false
		for _, id := range op.SourceIDs {
			if id == sourceID {
				found = true
			}
		}
		if !found {
			return protocol.NewCodedError("PROVENANCE_INVALID", "fact extraction is outside its cited sources", false, nil)
		}
	}
	if op.SupersedesFact != "" {
		var kind string
		if err := store.DB.QueryRowContext(ctx, `SELECT kind FROM facts WHERE fact_id = ?`, op.SupersedesFact).Scan(&kind); err != nil || kind != op.Kind {
			return protocol.NewCodedError("PROVENANCE_INVALID", "superseded fact is unavailable or has a different kind", false, nil)
		}
	}
	return nil
}

type existingFact struct {
	ID, Key, Kind string
	Version       int
	ReviewAfter   string
	Freshness     string
}

func findBatchFact(ctx context.Context, store *storage.Storage, op multiFactOperation) (existingFact, *protocol.CodedError) {
	var f existingFact
	var err error
	if op.FactID != "" {
		err = store.DB.QueryRowContext(ctx, `SELECT fact_id, fact_key, kind, current_version, COALESCE(review_after, ''), COALESCE(freshness, 'current') FROM facts WHERE fact_id = ?`, op.FactID).Scan(&f.ID, &f.Key, &f.Kind, &f.Version, &f.ReviewAfter, &f.Freshness)
	} else {
		err = store.DB.QueryRowContext(ctx, `SELECT fact_id, fact_key, kind, current_version, COALESCE(review_after, ''), COALESCE(freshness, 'current') FROM facts WHERE kind = ? AND fact_key = ?`, op.Kind, op.FactKey).Scan(&f.ID, &f.Key, &f.Kind, &f.Version, &f.ReviewAfter, &f.Freshness)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil
	}
	if err != nil {
		return f, ProtocolStorageError("cannot read fact", err)
	}
	if op.FactID != "" && (op.FactKey != f.Key || op.Kind != f.Kind) {
		return f, protocol.NewCodedError("FACT_KEY_MISMATCH", "fact operation identity does not match fact_id", false, nil)
	}
	return f, nil
}

func validateBatchArticle(op multiArticleOperation, allowed map[string]bool) *protocol.CodedError {
	if op.Operation != "create" && op.Operation != "update" && op.Operation != "retract" {
		return protocol.NewCodedError("ARTICLE_OPERATION_INVALID", "unsupported batch article operation", false, nil)
	}
	if strings.TrimSpace(op.Slug) == "" || strings.ContainsAny(op.Slug, `/\\`) {
		return protocol.NewCodedError("SLUG_INVALID", "batch article slug is invalid", false, nil)
	}
	if op.Sensitivity != "normal" && op.Sensitivity != "sensitive" && op.Sensitivity != "restricted" {
		return protocol.NewCodedError("SENSITIVITY_INVALID", "batch article sensitivity is invalid", false, nil)
	}
	for _, id := range op.SourceIDs {
		if !allowed[id] {
			return protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "batch article references a source outside the compile job", false, map[string]any{"source_id": id})
		}
	}
	for _, citation := range op.Citations {
		if !allowed[citation.SourceID] {
			return protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "batch citation references a source outside the compile job", false, map[string]any{"source_id": citation.SourceID})
		}
	}
	return nil
}

func validateBatchFact(op multiFactOperation, allowed map[string]bool) *protocol.CodedError {
	if op.Operation != "create" && op.Operation != "update" && op.Operation != "retract" {
		return protocol.NewCodedError("FACT_OPERATION_INVALID", "unsupported batch fact operation", false, nil)
	}
	if strings.TrimSpace(op.FactKey) == "" || strings.TrimSpace(op.Kind) == "" {
		return protocol.NewCodedError("FACT_INVALID", "fact_key and kind are required", false, nil)
	}
	if reviewAfter := strings.TrimSpace(op.ReviewAfter); reviewAfter != "" {
		if _, err := time.Parse(time.RFC3339Nano, reviewAfter); err != nil {
			return protocol.NewCodedError("FACT_REVIEW_AFTER_INVALID", "review_after must be an RFC3339 timestamp", false, nil)
		}
	}
	for _, id := range op.SourceIDs {
		if !allowed[id] {
			return protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "batch fact references a source outside the compile job", false, map[string]any{"source_id": id})
		}
	}
	return nil
}

func findBatchArticle(ctx context.Context, store *storage.Storage, op multiArticleOperation) (articleRecord, bool, *protocol.CodedError) {
	var a articleRecord
	var err error
	if op.ArticleID != "" {
		err = store.DB.QueryRowContext(ctx, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, op.ArticleID).Scan(&a.ID, &a.Slug, &a.Title, &a.Path, &a.Sensitivity, &a.Version, &a.Hash)
	} else {
		err = store.DB.QueryRowContext(ctx, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash FROM articles WHERE slug = ? AND forgotten_at IS NULL`, op.Slug).Scan(&a.ID, &a.Slug, &a.Title, &a.Path, &a.Sensitivity, &a.Version, &a.Hash)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if op.Operation != "create" {
			return a, false, nil
		}
		a.ID = newID("art")
		a.Slug = op.Slug
		a.Title = op.Title
		a.Sensitivity = op.Sensitivity
		a.Path = filepath.Join(store.Paths.Wiki, "articles", op.Slug+".md")
		return a, false, nil
	}
	if err != nil {
		return a, false, ProtocolStorageError("cannot read article", err)
	}
	if op.Operation == "create" {
		return a, true, protocol.NewCodedError("ARTICLE_SLUG_CONFLICT", "batch create slug already exists", false, map[string]any{"slug": op.Slug})
	}
	if op.Slug != a.Slug {
		return a, false, protocol.NewCodedError("ARTICLE_SLUG_MISMATCH", "batch article slug does not match article_id", false, map[string]any{"expected": a.Slug})
	}
	return a, true, nil
}

func ProtocolStorageError(message string, err error) *protocol.CodedError {
	return protocol.NewCodedError("STORAGE_UNHEALTHY", message, true, err.Error())
}
func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableNullString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func loadBatchPlan(ctx context.Context, store *storage.Storage, planID string) (batchPlanRecord, *protocol.CodedError) {
	var p batchPlanRecord
	var diffJSON, riskJSON []byte
	if err := store.DB.QueryRowContext(ctx, `SELECT plan_id, job_id, state, base_hash, diff_json, risk_json, expires_at FROM compile_batch_plans WHERE plan_id = ?`, planID).Scan(&p.ID, &p.JobID, &p.State, &p.BaseHash, &diffJSON, &riskJSON, &p.Expires); errors.Is(err, sql.ErrNoRows) {
		return p, protocol.NewCodedError("PLAN_NOT_FOUND", "plan was not found", false, nil)
	} else if err != nil {
		return p, ProtocolStorageError("cannot read batch plan", err)
	}
	_ = json.Unmarshal(diffJSON, &p.Diff)
	_ = json.Unmarshal(riskJSON, &p.RiskFlags)
	rows, err := store.DB.QueryContext(ctx, `SELECT ordinal, operation, COALESCE(article_id, ''), slug, base_version, COALESCE(base_hash, ''), proposed_version, proposed_hash, proposed_content, previous_content, diff_json FROM compile_batch_articles WHERE plan_id = ? ORDER BY ordinal`, planID)
	if err != nil {
		return p, ProtocolStorageError("cannot read batch articles", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a batchArticleRecord
		var diffJSON []byte
		if err := rows.Scan(&a.Ordinal, &a.Operation, &a.ArticleID, &a.Slug, &a.BaseVersion, &a.BaseHash, &a.ProposedVersion, &a.ProposedHash, &a.ProposedContent, &a.PreviousContent, &diffJSON); err != nil {
			return p, ProtocolStorageError("cannot decode batch article", err)
		}
		if err := json.Unmarshal(diffJSON, &a.Diff); err != nil {
			return p, ProtocolStorageError("invalid batch article diff", err)
		}
		a.Path = filepath.Join(store.Paths.Wiki, "articles", a.Slug+".md")
		p.Articles = append(p.Articles, a)
	}
	rows.Close()
	factRows, err := store.DB.QueryContext(ctx, `SELECT ordinal, operation, COALESCE(fact_id, ''), fact_key, kind, base_version, text, status, COALESCE(extraction_id, ''), COALESCE(review_after, ''), COALESCE(previous_review_after, ''), COALESCE(previous_freshness, 'current'), diff_json FROM compile_batch_facts WHERE plan_id = ? ORDER BY ordinal`, planID)
	if err != nil {
		return p, ProtocolStorageError("cannot read batch facts", err)
	}
	defer factRows.Close()
	for factRows.Next() {
		var f batchFactRecord
		var diffJSON []byte
		if err := factRows.Scan(&f.Ordinal, &f.Operation, &f.FactID, &f.FactKey, &f.Kind, &f.BaseVersion, &f.Text, &f.Status, &f.ExtractionID, &f.ReviewAfter, &f.PreviousReviewAfter, &f.PreviousFreshness, &diffJSON); err != nil {
			return p, ProtocolStorageError("cannot decode batch fact", err)
		}
		var op multiFactOperation
		if err := json.Unmarshal(diffJSON, &op); err == nil {
			// Plans created before source_ids received an explicit JSON tag
			// used Go field names (for example, "SourceIDs"). Keep those
			// pending plans readable while writing the canonical form for all
			// new plans.
			if len(op.SourceIDs) == 0 {
				var legacy struct {
					SourceIDs    []string `json:"SourceIDs"`
					SupersedesID string   `json:"SupersedesID"`
				}
				if legacyErr := json.Unmarshal(diffJSON, &legacy); legacyErr == nil {
					op.SourceIDs = legacy.SourceIDs
					if op.SupersedesFact == "" {
						op.SupersedesFact = legacy.SupersedesID
					}
				}
			}
			f.SourceIDs, f.SupersedesID = op.SourceIDs, op.SupersedesFact
		}
		p.Facts = append(p.Facts, f)
	}
	relationRows, err := store.DB.QueryContext(ctx, `SELECT ordinal, relation_id, relation_type, from_type, from_id, from_version, to_type, to_id, to_version, COALESCE(source_id, '') FROM compile_batch_relations WHERE plan_id = ? ORDER BY ordinal`, planID)
	if err != nil {
		return p, ProtocolStorageError("cannot read batch relations", err)
	}
	defer relationRows.Close()
	for relationRows.Next() {
		var relation batchRelationRecord
		if err := relationRows.Scan(&relation.Ordinal, &relation.RelationID, &relation.RelationType, &relation.From.Type, &relation.From.ID, &relation.From.Version, &relation.To.Type, &relation.To.ID, &relation.To.Version, &relation.SourceID); err != nil {
			return p, ProtocolStorageError("cannot decode batch relation", err)
		}
		p.Relations = append(p.Relations, relation)
	}
	if err := relationRows.Err(); err != nil {
		return p, ProtocolStorageError("cannot finish batch relation read", err)
	}
	return p, nil
}

func (p batchPlanRecord) response() batchPlanResponse {
	return batchPlanResponse{PlanID: p.ID, JobID: p.JobID, State: p.State, ArticleCount: len(p.Articles), FactCount: len(p.Facts), RelationCount: len(p.Relations), ExpiresAt: p.Expires, RiskFlags: p.RiskFlags, Diff: p.Diff}
}

func applyBatch(ctx context.Context, store *storage.Storage, req protocol.Request, p batchPlanRecord) (protocol.Response, *protocol.CodedError) {
	if p.State != "pending" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "batch plan is not pending", false, map[string]any{"state": p.State})
	}
	if expired(p.Expires) {
		return protocol.Response{}, protocol.NewCodedError("PLAN_EXPIRED", "batch plan has expired", false, map[string]any{"expires_at": p.Expires})
	}
	job, codedErr := loadJob(ctx, store, p.JobID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if job.State != "preview_ready" {
		return protocol.Response{}, protocol.NewCodedError("JOB_STATE_INVALID", "compile job is not ready for batch apply", false, map[string]any{"state": job.State})
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	stageDir := filepath.Join(store.Paths.Staging, p.ID+"-batch")
	markerPath := filepath.Join(store.Paths.Staging, p.ID+"-batch.json")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot create batch staging directory", err)
	}
	marker, _ := json.Marshal(map[string]any{"plan_id": p.ID, "staged_dir": stageDir, "article_count": len(p.Articles)})
	if err := writePrivateFile(markerPath, marker); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot create batch recovery marker", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stageDir)
			_ = os.Remove(markerPath)
		}
	}()
	for i, article := range p.Articles {
		if article.Operation != "create" {
			if codedErr := verifyArticleFile(store, articleRecord{ID: article.ArticleID, Slug: article.Slug, Path: article.Path, Sensitivity: article.Diff.Sensitivity, Version: article.BaseVersion, Hash: article.BaseHash}); codedErr != nil {
				return protocol.Response{}, codedErr
			}
		}
		if err := writePrivateFile(filepath.Join(stageDir, fmt.Sprintf("%03d.md", i)), []byte(article.ProposedContent)); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot stage batch article", err)
		}
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot begin batch apply", err)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot reserve batch apply idempotency", err)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Validate every optimistic-concurrency guard before the first final-file
	// rename. A stale item therefore rejects the whole batch before any item is
	// visible in the managed Wiki.
	for _, article := range p.Articles {
		if article.Operation == "create" {
			if _, statErr := os.Lstat(article.Path); statErr == nil {
				return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch create target already exists", false, map[string]any{"path": article.Path})
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return protocol.Response{}, protocol.NewCodedError("WIKI_DRIFT", "batch create target cannot be inspected", false, nil)
			}
		} else {
			var version int
			var hash, path, forgotten string
			if err := tx.QueryRowContext(ctx, `SELECT current_version, current_hash, path, COALESCE(forgotten_at, '') FROM articles WHERE article_id = ?`, article.ArticleID).Scan(&version, &hash, &path, &forgotten); err != nil {
				return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch article target is unavailable", false, nil)
			}
			if version != article.BaseVersion || hash != article.BaseHash || path != article.Path || forgotten != "" {
				return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch article changed before apply", false, map[string]any{"article_id": article.ArticleID})
			}
		}
	}
	for _, fact := range p.Facts {
		if fact.BaseVersion == 0 {
			var existingID string
			if err := tx.QueryRowContext(ctx, `SELECT fact_id FROM facts WHERE kind = ? AND fact_key = ?`, fact.Kind, fact.FactKey).Scan(&existingID); err == nil {
				return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch fact key was created after preview", false, map[string]any{"kind": fact.Kind, "fact_key": fact.FactKey})
			} else if !errors.Is(err, sql.ErrNoRows) {
				return protocol.Response{}, ProtocolStorageError("cannot check batch fact key", err)
			}
			continue
		}
		var version int
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT current_version, status FROM facts WHERE fact_id = ?`, fact.FactID).Scan(&version, &status); err != nil {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch fact target is unavailable", false, nil)
		} else if version != fact.BaseVersion {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch fact changed before apply", false, map[string]any{"fact_id": fact.FactID})
		}
	}
	allowedSources := make(map[string]bool, len(job.SourceIDs))
	for _, sourceID := range job.SourceIDs {
		allowedSources[sourceID] = true
	}
	for _, relation := range p.Relations {
		input := knowledge.RelationInput{
			RelationType: relation.RelationType,
			From:         relation.From,
			To:           relation.To,
			SourceID:     relation.SourceID,
		}
		resolved, relationErr := knowledge.ValidateRelation(ctx, tx, input, allowedSources, allowSensitive)
		if relationErr != nil {
			switch relationErr.Code {
			case "SENSITIVITY_DENIED", "SENSITIVITY_ESCALATION_REQUIRED", "SOURCE_REFERENCE_INVALID":
				return protocol.Response{}, relationErr
			}
			return protocol.Response{}, protocol.NewCodedError("RELATION_PLAN_STALE", "batch relation changed before apply", false, nil)
		}
		if resolved.From.Version != relation.From.Version || resolved.To.Version != relation.To.Version {
			return protocol.Response{}, protocol.NewCodedError("RELATION_PLAN_STALE", "batch relation endpoint version changed before apply", false, nil)
		}
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT relation_id FROM relations WHERE relation_type = ? AND from_type = ? AND from_id = ? AND from_version = ? AND to_type = ? AND to_id = ? AND to_version = ?`, relation.RelationType, relation.From.Type, relation.From.ID, relation.From.Version, relation.To.Type, relation.To.ID, relation.To.Version).Scan(&existing); err == nil {
			return protocol.Response{}, protocol.NewCodedError("RELATION_PLAN_STALE", "batch relation already exists", false, nil)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return protocol.Response{}, ProtocolStorageError("cannot check batch relation", err)
		}
	}
	for i, article := range p.Articles {
		if codedErr := ensureArticlePath(store, article.Path); codedErr != nil {
			return protocol.Response{}, codedErr
		}
		cleanup = false
		if err := os.Rename(filepath.Join(stageDir, fmt.Sprintf("%03d.md", i)), article.Path); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot finalize batch article", err)
		}
		if article.Operation == "create" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO articles(article_id, slug, title, path, sensitivity, current_version, current_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, article.ArticleID, article.Slug, article.Diff.Title, article.Path, article.Diff.Sensitivity, article.ProposedVersion, article.ProposedHash, now, now); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot create batch article metadata", err)
			}
		} else {
			result, err := tx.ExecContext(ctx, `UPDATE articles SET title = ?, sensitivity = ?, current_version = ?, current_hash = ?, updated_at = ? WHERE article_id = ? AND current_version = ? AND current_hash = ?`, article.Diff.Title, article.Diff.Sensitivity, article.ProposedVersion, article.ProposedHash, now, article.ArticleID, article.BaseVersion, article.BaseHash)
			if err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot update batch article metadata", err)
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch article changed before apply", false, nil)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO article_versions(article_id, version, content_hash, path, content, job_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, article.ArticleID, article.ProposedVersion, article.ProposedHash, article.Path, article.ProposedContent, p.JobID, now); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot record batch article version", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM article_citations WHERE article_id = ? AND version = ?`, article.ArticleID, article.ProposedVersion); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot clear batch citations", err)
		}
		for _, c := range uniqueCitations(article.Diff.Citations) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO article_citations(article_id, version, source_id, locator) VALUES (?, ?, ?, ?)`, article.ArticleID, article.ProposedVersion, c.SourceID, c.Locator); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot record batch citation", err)
			}
		}
		if err := storage.ReplaceArticleIndexTx(ctx, tx, article.ArticleID, article.ProposedVersion, article.Diff.Title, article.Slug, strings.Join(article.Diff.Tags, " "), article.Diff.Summary, article.Diff.Body, strings.Join(article.Diff.SourceIDs, " ")); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot update batch article index", err)
		}
	}
	for _, fact := range p.Facts {
		var factID string
		var current int
		err := tx.QueryRowContext(ctx, `SELECT fact_id, current_version FROM facts WHERE kind = ? AND fact_key = ?`, fact.Kind, fact.FactKey).Scan(&factID, &current)
		if errors.Is(err, sql.ErrNoRows) {
			factID = fact.FactID
			if factID == "" {
				factID = newID("fact")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO facts(fact_id, fact_key, kind, current_version, status, freshness, review_after, created_at, updated_at) VALUES (?, ?, ?, 1, ?, 'current', NULLIF(?, ''), ?, ?)`, factID, fact.FactKey, fact.Kind, fact.Status, fact.ReviewAfter, now, now); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot create fact", err)
			}
			current = 0
		} else if err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot read fact during apply", err)
		} else if current != fact.BaseVersion {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "fact changed before apply", false, nil)
		} else if _, err := tx.ExecContext(ctx, `UPDATE facts SET current_version = ?, status = ?, freshness = 'current', review_after = NULLIF(?, ''), updated_at = ? WHERE fact_id = ? AND current_version = ?`, current+1, fact.Status, fact.ReviewAfter, now, factID, current); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot update fact", err)
		}
		version := current + 1
		if current > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE fact_versions SET status = 'superseded' WHERE fact_id = ? AND version = ?`, factID, current); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot supersede previous fact version", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fact_versions(fact_id, version, text, status, extraction_id, supersedes_fact_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, factID, version, fact.Text, fact.Status, nullableText(fact.ExtractionID), nullableText(fact.SupersedesID), now); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot record fact version", err)
		}
		for _, sourceID := range fact.SourceIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO fact_citations(fact_id, version, source_id, locator) VALUES (?, ?, ?, ?)`, factID, version, sourceID, "source"); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot record fact citation", err)
			}
		}
	}
	for _, relation := range p.Relations {
		resolved := knowledge.ResolvedRelation{RelationID: relation.RelationID, RelationType: relation.RelationType, From: relation.From, To: relation.To, SourceID: relation.SourceID}
		if _, relationErr := knowledge.InsertRelationTx(ctx, tx, resolved, "compile_batch", p.ID, now); relationErr != nil {
			return protocol.Response{}, relationErr
		}
	}
	planUpdate, err := tx.ExecContext(ctx, `UPDATE compile_batch_plans SET state = 'applied', applied_at = ? WHERE plan_id = ? AND state = 'pending'`, now, p.ID)
	if err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot mark batch plan applied", err)
	}
	if affected, _ := planUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch plan state changed before apply", false, nil)
	}
	jobUpdate, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'applied', applied_plan_id = ?, updated_at = ? WHERE job_id = ? AND state = 'preview_ready'`, p.ID, now, p.JobID)
	if err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot mark batch job applied", err)
	}
	if affected, _ := jobUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch job state changed before apply", false, nil)
	}
	responseData := p.response()
	responseData.State = "applied"
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": p.ID, "job_id": p.JobID, "articles": len(p.Articles), "facts": len(p.Facts), "relations": len(p.Relations)}); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot audit batch apply", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot complete batch apply idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot commit batch apply", err)
	}
	cleanup = false
	_ = os.RemoveAll(stageDir)
	_ = os.Remove(markerPath)
	return response, nil
}

func undoBatch(ctx context.Context, store *storage.Storage, req protocol.Request, p batchPlanRecord) (protocol.Response, *protocol.CodedError) {
	if p.State != "applied" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "only an applied batch plan can be undone", false, map[string]any{"state": p.State})
	}
	job, codedErr := loadJob(ctx, store, p.JobID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if job.State != "applied" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "compile job is not in the applied state", false, map[string]any{"state": job.State})
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot read batch undo idempotency", err)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	stageDir := filepath.Join(store.Paths.Staging, p.ID+"-batch-undo")
	markerPath := filepath.Join(store.Paths.Staging, p.ID+"-batch-undo.json")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot create batch undo staging directory", err)
	}
	marker, _ := json.Marshal(map[string]any{"plan_id": p.ID, "undo": true, "phase": "prepared"})
	if err := writePrivateFile(markerPath, marker); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot create batch undo recovery marker", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stageDir)
			_ = os.Remove(markerPath)
		}
	}()
	for i, article := range p.Articles {
		current := articleRecord{ID: article.ArticleID, Slug: article.Slug, Path: article.Path, Version: article.ProposedVersion, Hash: article.ProposedHash, Sensitivity: article.Diff.Sensitivity}
		if codedErr := verifyArticleFile(store, current); codedErr != nil {
			return protocol.Response{}, codedErr
		}
		if article.Operation != "create" {
			if !article.PreviousContent.Valid {
				return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch undo previous article content is unavailable", false, nil)
			}
			if err := writePrivateFile(filepath.Join(stageDir, fmt.Sprintf("%03d.md", i)), []byte(article.PreviousContent.String)); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot stage batch undo article", err)
			}
		}
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot begin batch undo", err)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot reserve batch undo idempotency", err)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	for _, article := range p.Articles {
		var version int
		var hash, path, forgotten string
		if err := tx.QueryRowContext(ctx, `SELECT current_version, current_hash, path, COALESCE(forgotten_at, '') FROM articles WHERE article_id = ?`, article.ArticleID).Scan(&version, &hash, &path, &forgotten); err != nil {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch article is unavailable during undo", false, nil)
		}
		if version != article.ProposedVersion || hash != article.ProposedHash || path != article.Path || forgotten != "" {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch article changed before undo", false, nil)
		}
	}
	for _, fact := range p.Facts {
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT current_version FROM facts WHERE fact_id = ?`, fact.FactID).Scan(&current); err != nil || current != fact.BaseVersion+1 {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch fact changed before undo", false, nil)
		}
	}
	for i, article := range p.Articles {
		if article.Operation == "create" {
			trashPath := filepath.Join(store.Paths.Trash, "batch-undo-"+p.ID+"-"+article.ArticleID+".md")
			if err := ensureTrashPathAbsent(trashPath); err != nil {
				return protocol.Response{}, err
			}
			if err := os.Rename(article.Path, trashPath); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot move batch-created article to trash", err)
			}
		} else {
			if err := os.Rename(filepath.Join(stageDir, fmt.Sprintf("%03d.md", i)), article.Path); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot restore batch article", err)
			}
		}
	}
	cleanup = false
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, article := range p.Articles {
		if article.Operation == "create" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM articles WHERE article_id = ? AND current_version = ? AND current_hash = ?`, article.ArticleID, article.ProposedVersion, article.ProposedHash); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot remove batch-created article metadata", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM article_fts WHERE article_id = ?`, article.ArticleID); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot remove batch-created article index", err)
			}
			continue
		}
		parsed, err := knowledge.ParseManagedArticle([]byte(article.PreviousContent.String))
		if err != nil || parsed.ArticleID != article.ArticleID || parsed.Version != article.BaseVersion {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "previous batch article projection is invalid", false, nil)
		}
		result, err := tx.ExecContext(ctx, `UPDATE articles SET title = ?, sensitivity = ?, current_version = ?, current_hash = ?, updated_at = ? WHERE article_id = ? AND current_version = ? AND current_hash = ?`, parsed.Title, parsed.Sensitivity, article.BaseVersion, article.BaseHash, now, article.ArticleID, article.ProposedVersion, article.ProposedHash)
		if err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot restore batch article metadata", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "batch article changed before undo", false, nil)
		}
		if err := storage.ReplaceArticleIndexTx(ctx, tx, article.ArticleID, article.BaseVersion, parsed.Title, parsed.Slug, strings.Join(parsed.Tags, " "), parsed.Summary, parsed.Body, strings.Join(parsed.SourceIDs, " ")); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot restore batch article index", err)
		}
	}
	for _, fact := range p.Facts {
		if fact.BaseVersion == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE fact_id = ? AND current_version = 1`, fact.FactID); err != nil {
				return protocol.Response{}, ProtocolStorageError("cannot remove batch-created fact", err)
			}
			continue
		}
		var oldStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM fact_versions WHERE fact_id = ? AND version = ?`, fact.FactID, fact.BaseVersion).Scan(&oldStatus); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot read prior batch fact version", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM fact_versions WHERE fact_id = ? AND version = ?`, fact.FactID, fact.BaseVersion+1); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot remove applied batch fact version", err)
		}
		previousFreshness := fact.PreviousFreshness
		if previousFreshness == "" {
			previousFreshness = "current"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE facts SET current_version = ?, status = ?, freshness = ?, review_after = NULLIF(?, ''), updated_at = ? WHERE fact_id = ? AND current_version = ?`, fact.BaseVersion, oldStatus, previousFreshness, fact.PreviousReviewAfter, now, fact.FactID, fact.BaseVersion+1); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot restore batch fact", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fact_versions SET status = ? WHERE fact_id = ? AND version = ?`, oldStatus, fact.FactID, fact.BaseVersion); err != nil {
			return protocol.Response{}, ProtocolStorageError("cannot restore prior batch fact status", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relations WHERE origin_kind = 'compile_batch' AND origin_id = ?`, p.ID); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot remove batch relations", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compile_batch_plans SET state = 'undone', undone_at = ? WHERE plan_id = ? AND state = 'applied'`, now, p.ID); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot mark batch plan undone", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'preview_ready', applied_plan_id = NULL, updated_at = ? WHERE job_id = ? AND applied_plan_id = ?`, now, p.JobID, p.ID); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot restore batch job state", err)
	}
	data := p.response()
	data.State = "undone"
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": p.ID, "job_id": p.JobID, "relations": len(p.Relations)}); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot audit batch undo", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot complete batch undo idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, ProtocolStorageError("cannot commit batch undo", err)
	}
	_ = os.RemoveAll(stageDir)
	_ = os.Remove(markerPath)
	return response, nil
}

func ensureTrashPathAbsent(path string) *protocol.CodedError {
	if _, err := os.Lstat(path); err == nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "batch undo trash path already exists", true, nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "batch undo trash path cannot be inspected", true, nil)
	}
	return nil
}
