package compile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cairn/internal/protocol"
	"cairn/internal/storage"
)

type planDiff struct {
	Kind              string `json:"kind"`
	Path              string `json:"path"`
	Slug              string `json:"slug"`
	BeforeHash        string `json:"before_hash,omitempty"`
	BeforeVersion     int    `json:"before_version"`
	AfterHash         string `json:"after_hash"`
	AfterVersion      int    `json:"after_version"`
	BeforeTitle       string `json:"before_title,omitempty"`
	BeforeSensitivity string `json:"before_sensitivity,omitempty"`
	AfterTitle        string `json:"after_title"`
	AfterSensitivity  string `json:"after_sensitivity"`
}

type planRecord struct {
	ID                string
	JobID             string
	Kind              string
	State             string
	ArticleID         string
	BaseVersion       int
	BaseHash          string
	ProposedVersion   int
	ProposedHash      string
	ProposedContent   string
	PreviousContent   sql.NullString
	Diff              planDiff
	RiskFlags         []string
	ExpiresAt         string
	AppliedAt         sql.NullString
	UndoneAt          sql.NullString
	AffectedSourceIDs []string
}

type articleRecord struct {
	ID          string
	Slug        string
	Title       string
	Path        string
	Sensitivity string
	Version     int
	NextVersion int
	Hash        string
}

// Preview converts a submitted write stage into a durable, unapplied plan.
// It deliberately does not touch the managed Wiki.
func Preview(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[previewArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if strings.TrimSpace(args.JobID) == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "job_id is required", false, nil)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	job, codedErr := loadJob(ctx, store, args.JobID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if job.State != "preview_ready" {
		return protocol.Response{}, protocol.NewCodedError("JOB_STATE_INVALID", "compile job is not ready for preview", false, map[string]any{"state": job.State})
	}
	var resultPath, recordedHash, sourceID string
	err = store.DB.QueryRowContext(ctx, `SELECT result_path, COALESCE(result_hash, ''), cj.source_id FROM compile_stages cs JOIN compile_jobs cj ON cj.job_id = cs.job_id WHERE cs.job_id = ? AND cs.stage = 'write'`, args.JobID).Scan(&resultPath, &recordedHash, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Response{}, protocol.NewCodedError("STAGE_NOT_FOUND", "write stage was not found", false, nil)
	}
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read write stage", true, nil)
	}
	resultFile := absoluteRef(store.Paths.Root, resultPath)
	contents, err := readStageResult(resultFile)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_UNREADABLE", "write stage result cannot be read", false, nil)
	}
	if recordedHash == "" || hashBytes(contents) != recordedHash {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_CHANGED", "write stage result changed after submission", false, nil)
	}
	if err := Validate("write", contents); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "write stage result does not satisfy its JSON Schema", false, err.Error())
	}
	if codedErr := validateReferences(ctx, store, "write", contents, sourceID); codedErr != nil {
		return protocol.Response{}, codedErr
	}
	var candidate writeCandidate
	if err := json.Unmarshal(contents, &candidate); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "write stage result cannot be decoded", false, nil)
	}
	if err := validateCandidate(&candidate); err != nil {
		return protocol.Response{}, err
	}

	article, existing, riskFlags, diff, prepareErr := prepareArticle(ctx, store, candidate)
	if prepareErr != nil {
		return protocol.Response{}, prepareErr
	}
	proposedVersion := article.NextVersion
	if proposedVersion < 1 {
		proposedVersion = article.Version + 1
	}
	content, err := renderArticle(article, candidate, proposedVersion)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot render managed article", false, nil)
	}
	proposedHash := hashBytes([]byte(content))
	diff.AfterHash = proposedHash
	diff.AfterVersion = proposedVersion
	diff.AfterTitle = candidate.Title
	diff.AfterSensitivity = candidate.Sensitivity
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot encode plan diff", false, nil)
	}
	riskFlags = uniqueStrings(riskFlags)
	riskJSON, err := json.Marshal(riskFlags)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot encode plan risk", false, nil)
	}
	planID := newID("plan")
	expiresAt := time.Now().UTC().Add(planLifetime).Format(time.RFC3339Nano)
	responseData := planResponse{
		PlanID: planID, JobID: args.JobID, State: "pending", ArticleID: article.ID,
		Path: article.Path, BaseVersion: article.Version, ProposedVersion: proposedVersion,
		RiskFlags: riskFlags, Diff: map[string]any{}, ExpiresAt: expiresAt,
		AffectedSourceIDs: uniqueStrings(candidate.SourceIDs),
	}
	if err := json.Unmarshal(diffJSON, &responseData.Diff); err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot decode plan diff", false, nil)
	}
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, err := json.Marshal(response)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot encode plan response", false, nil)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin preview transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve preview idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	previousContent := any(nil)
	if existing {
		previous, err := articlePreviousContent(store, article)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("WIKI_DRIFT", "managed article changed while creating the plan", false, nil)
		}
		previousContent = previous
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plans(plan_id, job_id, kind, state, article_id, base_version, base_hash, proposed_version, proposed_hash, proposed_content, previous_content, diff_json, risk_json, expires_at, created_at) VALUES (?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, planID, args.JobID, diff.Kind, article.ID, article.Version, nullableString(article.Hash), proposedVersion, proposedHash, content, previousContent, diffJSON, riskJSON, expiresAt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create preview plan", true, nil)
	}
	for _, source := range uniqueStrings(candidate.SourceIDs) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_items(plan_id, item_type, item_id) VALUES (?, 'source', ?)`, planID, source); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot record plan source", true, nil)
		}
	}
	for _, item := range uniqueCitations(candidate.Citations) {
		detail, _ := json.Marshal(item)
		itemID := item.SourceID + "#" + item.Locator
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_items(plan_id, item_type, item_id, detail_json) VALUES (?, 'citation', ?, ?)`, planID, itemID, detail); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot record plan citation", true, nil)
		}
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": planID, "job_id": args.JobID, "article_id": article.ID, "risk_flags": riskFlags}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write preview audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete preview idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit preview plan", true, nil)
	}
	return response, nil
}

// Inspect returns a plan without allowing it to change state.
func Inspect(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[planArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	record, err := loadPlan(ctx, store, args.PlanID)
	if err != nil {
		return nil, err
	}
	return record.response(), nil
}

// Apply is the sole gateway that writes managed Wiki content.
func Apply(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[planArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if !args.Confirmed {
		return protocol.Response{}, protocol.NewCodedError("CONFIRMATION_REQUIRED", "plan.apply requires explicit Skill confirmation", false, nil)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	record, codedErr := loadPlan(ctx, store, args.PlanID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if record.State != "pending" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "plan is not pending", false, map[string]any{"state": record.State})
	}
	if expired(record.ExpiresAt) {
		return protocol.Response{}, protocol.NewCodedError("PLAN_EXPIRED", "plan has expired", false, map[string]any{"expires_at": record.ExpiresAt})
	}
	article, codedErr := checkPlanCurrent(ctx, store, record, false)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if article.Path == "" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "plan article target is unavailable", false, nil)
	}
	if err := ensureArticlePath(store, article.Path); err != nil {
		return protocol.Response{}, err
	}
	markerPath := filepath.Join(store.Paths.Staging, record.ID+".json")
	stagedPath := filepath.Join(store.Paths.Staging, record.ID+".md")
	marker := map[string]any{"plan_id": record.ID, "target": article.Path, "staged": stagedPath, "kind": record.Kind}
	markerBytes, _ := json.Marshal(marker)
	if err := writePrivateFile(markerPath, markerBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create recovery marker", true, nil)
	}
	cleanupBeforeRename := true
	defer func() {
		if cleanupBeforeRename {
			_ = os.Remove(stagedPath)
			_ = os.Remove(markerPath)
		}
	}()
	if err := writePrivateFile(stagedPath, []byte(record.ProposedContent)); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot stage managed article", true, nil)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin plan apply transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve apply idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	if record.Kind == "update" {
		if codedErr := verifyArticleFile(store, article); codedErr != nil {
			return protocol.Response{}, codedErr
		}
	} else if _, err := os.Lstat(article.Path); err == nil {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "new article path is no longer available", false, nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return protocol.Response{}, protocol.NewCodedError("WIKI_DRIFT", "new article path cannot be inspected", false, nil)
	}
	if err := os.Rename(stagedPath, article.Path); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot atomically replace managed article", true, nil)
	}
	cleanupBeforeRename = false
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if record.Kind == "create" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO articles(article_id, slug, title, path, sensitivity, current_version, current_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.ArticleID, record.Diff.Slug, record.Diff.AfterTitle, article.Path, record.Diff.AfterSensitivity, record.ProposedVersion, record.ProposedHash, now, now); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create article metadata", true, nil)
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE articles SET title = ?, sensitivity = ?, current_version = ?, current_hash = ?, updated_at = ? WHERE article_id = ? AND current_version = ? AND current_hash = ?`, record.Diff.AfterTitle, record.Diff.AfterSensitivity, record.ProposedVersion, record.ProposedHash, now, record.ArticleID, record.BaseVersion, record.BaseHash)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot update article metadata", true, nil)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "article version changed before apply", false, nil)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO article_versions(article_id, version, content_hash, path, content, job_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, record.ArticleID, record.ProposedVersion, record.ProposedHash, article.Path, record.ProposedContent, record.JobID, now); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot record article version", true, nil)
	}
	if err := insertPlanCitations(ctx, tx, record); err != nil {
		return protocol.Response{}, err
	}
	planUpdate, err := tx.ExecContext(ctx, `UPDATE plans SET state = 'applied', applied_at = ? WHERE plan_id = ? AND state = 'pending'`, now, record.ID)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark plan applied", true, nil)
	}
	if affected, _ := planUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "plan state changed before apply", false, nil)
	}
	jobUpdate, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'applied', applied_plan_id = ?, updated_at = ? WHERE job_id = ? AND state = 'preview_ready'`, record.ID, now, record.JobID)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark compile job applied", true, nil)
	}
	if affected, _ := jobUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "compile job state changed before apply", false, nil)
	}
	responseData := record.response()
	responseData.State = "applied"
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": record.ID, "article_id": record.ArticleID}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write apply audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete apply idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit plan apply", true, nil)
	}
	if err := os.Remove(markerPath); err != nil {
		return response, nil
	}
	return response, nil
}

// Undo restores the exact version produced by a plan, and never overwrites a later edit.
func Undo(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[planArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if !args.Confirmed {
		return protocol.Response{}, protocol.NewCodedError("CONFIRMATION_REQUIRED", "plan.undo requires explicit Skill confirmation", false, nil)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	record, codedErr := loadPlan(ctx, store, args.PlanID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if record.State != "applied" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "only an applied plan can be undone", false, map[string]any{"state": record.State})
	}
	article, codedErr := checkPlanCurrent(ctx, store, record, true)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	markerPath := filepath.Join(store.Paths.Staging, record.ID+"-undo.json")
	stagedPath := filepath.Join(store.Paths.Staging, record.ID+"-undo.md")
	markerBytes, _ := json.Marshal(map[string]any{"plan_id": record.ID, "target": article.Path, "kind": record.Kind, "undo": true})
	if err := writePrivateFile(markerPath, markerBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create undo recovery marker", true, nil)
	}
	if codedErr := verifyArticleFile(store, article); codedErr != nil {
		_ = os.Remove(markerPath)
		return protocol.Response{}, codedErr
	}
	cleanupBeforeRename := true
	defer func() {
		if cleanupBeforeRename {
			_ = os.Remove(stagedPath)
			_ = os.Remove(markerPath)
		}
	}()
	if record.Kind == "create" {
		if err := os.Rename(article.Path, trashPath(store, record)); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot move managed article to trash", true, nil)
		}
	} else {
		if !record.PreviousContent.Valid {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "previous article content is unavailable", false, nil)
		}
		if err := writePrivateFile(stagedPath, []byte(record.PreviousContent.String)); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot stage article undo", true, nil)
		}
		if err := os.Rename(stagedPath, article.Path); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore managed article", true, nil)
		}
	}
	cleanupBeforeRename = false
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin undo transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve undo idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if record.Kind == "create" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM articles WHERE article_id = ? AND current_version = ? AND current_hash = ?`, record.ArticleID, record.ProposedVersion, record.ProposedHash); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot remove new article metadata", true, nil)
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE articles SET title = ?, sensitivity = ?, current_version = ?, current_hash = ?, updated_at = ? WHERE article_id = ? AND current_version = ? AND current_hash = ?`, record.Diff.BeforeTitle, record.Diff.BeforeSensitivity, record.BaseVersion, record.BaseHash, now, record.ArticleID, record.ProposedVersion, record.ProposedHash)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore article metadata", true, nil)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "article version changed before undo", false, nil)
		}
	}
	planUpdate, err := tx.ExecContext(ctx, `UPDATE plans SET state = 'undone', undone_at = ? WHERE plan_id = ? AND state = 'applied'`, now, record.ID)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark plan undone", true, nil)
	}
	if affected, _ := planUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "plan state changed before undo", false, nil)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'preview_ready', applied_plan_id = NULL, updated_at = ? WHERE job_id = ? AND applied_plan_id = ?`, now, record.JobID, record.ID); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore compile job state", true, nil)
	}
	responseData := record.response()
	responseData.State = "undone"
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": record.ID, "article_id": record.ArticleID}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write undo audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete undo idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit plan undo", true, nil)
	}
	_ = os.Remove(markerPath)
	return response, nil
}

func validateCandidate(candidate *writeCandidate) *protocol.CodedError {
	candidate.Slug = strings.TrimSpace(candidate.Slug)
	if candidate.Slug == "" || strings.Contains(candidate.Slug, "/") || strings.Contains(candidate.Slug, "\\") || candidate.Slug == "." || candidate.Slug == ".." {
		return protocol.NewCodedError("SLUG_INVALID", "article slug is invalid", false, nil)
	}
	if candidate.ArticleID != "" && (strings.ContainsAny(candidate.ArticleID, "/\\") || strings.TrimSpace(candidate.ArticleID) != candidate.ArticleID) {
		return protocol.NewCodedError("ARTICLE_ID_INVALID", "article_id is invalid", false, nil)
	}
	return nil
}

func prepareArticle(ctx context.Context, store *storage.Storage, candidate writeCandidate) (articleRecord, bool, []string, planDiff, *protocol.CodedError) {
	riskFlags := []string{}
	if candidate.Sensitivity != "normal" {
		riskFlags = append(riskFlags, "sensitive_content")
	}
	if candidate.ArticleID != "" {
		article, err := queryArticle(ctx, store.DB, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, candidate.ArticleID)
		if errors.Is(err, sql.ErrNoRows) {
			return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("ARTICLE_NOT_FOUND", "target article was not found", false, nil)
		}
		if err != nil {
			return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read target article", true, nil)
		}
		if candidate.Slug != article.Slug {
			return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("ARTICLE_SLUG_MISMATCH", "write result slug does not match target article", false, map[string]any{"expected": article.Slug})
		}
		if codedErr := verifyArticleFile(store, article); codedErr != nil {
			return articleRecord{}, false, nil, planDiff{}, codedErr
		}
		riskFlags = append(riskFlags, "overwrite")
		nextVersion, err := nextArticleVersion(ctx, store.DB, article.ID)
		if err != nil {
			return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot determine next article version", true, nil)
		}
		article.NextVersion = nextVersion
		return article, true, riskFlags, planDiff{Kind: "update", Path: article.Path, Slug: article.Slug, BeforeHash: article.Hash, BeforeVersion: article.Version, BeforeTitle: article.Title, BeforeSensitivity: article.Sensitivity}, nil
	}
	article, err := queryArticle(ctx, store.DB, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash FROM articles WHERE slug = ? AND forgotten_at IS NULL`, candidate.Slug)
	if err == nil {
		// A new candidate must never silently overwrite an article selected only by slug.
		riskFlags = append(riskFlags, "slug_collision")
		seed, _ := json.Marshal(candidate)
		candidate.Slug = collisionSlug(candidate.Slug, seed)
		if _, collisionErr := queryArticle(ctx, store.DB, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash FROM articles WHERE slug = ? AND forgotten_at IS NULL`, candidate.Slug); collisionErr == nil {
			return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("ARTICLE_SLUG_CONFLICT", "deterministic collision slug is already in use", false, map[string]any{"slug": candidate.Slug})
		} else if !errors.Is(collisionErr, sql.ErrNoRows) {
			return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot check deterministic article slug", true, nil)
		}
		article = articleRecord{}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return articleRecord{}, false, nil, planDiff{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot check article slug", true, nil)
	}
	article.ID = newID("art")
	article.Slug = candidate.Slug
	article.Path = filepath.Join(store.Paths.Wiki, "articles", candidate.Slug+".md")
	article.Version = 0
	article.NextVersion = 1
	return article, false, riskFlags, planDiff{Kind: "create", Path: article.Path, Slug: article.Slug}, nil
}

func renderArticle(article articleRecord, candidate writeCandidate, version int) (string, error) {
	tags := uniqueStrings(candidate.Tags)
	sources := uniqueStrings(candidate.SourceIDs)
	citations := uniqueCitations(candidate.Citations)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("cairn_article_id: ")
	b.WriteString(strconv.Quote(article.ID))
	b.WriteString("\n")
	b.WriteString("title: ")
	b.WriteString(strconv.Quote(candidate.Title))
	b.WriteString("\n")
	b.WriteString("slug: ")
	b.WriteString(strconv.Quote(article.Slug))
	b.WriteString("\n")
	b.WriteString("summary: ")
	b.WriteString(strconv.Quote(candidate.Summary))
	b.WriteString("\n")
	b.WriteString("sensitivity: ")
	b.WriteString(candidate.Sensitivity)
	b.WriteString("\n")
	b.WriteString("version: ")
	b.WriteString(strconv.Itoa(version))
	b.WriteString("\n")
	b.WriteString("tags:\n")
	for _, tag := range tags {
		b.WriteString("  - ")
		b.WriteString(strconv.Quote(tag))
		b.WriteString("\n")
	}
	b.WriteString("sources:\n")
	for _, source := range sources {
		b.WriteString("  - ")
		b.WriteString(strconv.Quote(source))
		b.WriteString("\n")
	}
	b.WriteString("citations:\n")
	for _, citation := range citations {
		b.WriteString("  - source_id: ")
		b.WriteString(strconv.Quote(citation.SourceID))
		b.WriteString("\n    locator: ")
		b.WriteString(strconv.Quote(citation.Locator))
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(candidate.Body, "\n"))
	b.WriteString("\n")
	return b.String(), nil
}

func nextArticleVersion(ctx context.Context, db *sql.DB, articleID string) (int, error) {
	var next int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM article_versions WHERE article_id = ?`, articleID).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
}

func queryArticle(ctx context.Context, db *sql.DB, query string, args ...any) (articleRecord, error) {
	var article articleRecord
	err := db.QueryRowContext(ctx, query, args...).Scan(&article.ID, &article.Slug, &article.Title, &article.Path, &article.Sensitivity, &article.Version, &article.Hash)
	return article, err
}

func loadPlan(ctx context.Context, store *storage.Storage, planID string) (planRecord, *protocol.CodedError) {
	if strings.TrimSpace(planID) == "" {
		return planRecord{}, protocol.NewCodedError("REQUEST_INVALID", "plan_id is required", false, nil)
	}
	var record planRecord
	var diffJSON, riskJSON []byte
	err := store.DB.QueryRowContext(ctx, `SELECT plan_id, job_id, kind, state, article_id, base_version, COALESCE(base_hash, ''), proposed_version, proposed_hash, proposed_content, previous_content, diff_json, risk_json, expires_at, applied_at, undone_at FROM plans WHERE plan_id = ?`, planID).Scan(&record.ID, &record.JobID, &record.Kind, &record.State, &record.ArticleID, &record.BaseVersion, &record.BaseHash, &record.ProposedVersion, &record.ProposedHash, &record.ProposedContent, &record.PreviousContent, &diffJSON, &riskJSON, &record.ExpiresAt, &record.AppliedAt, &record.UndoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return planRecord{}, protocol.NewCodedError("PLAN_NOT_FOUND", "plan was not found", false, nil)
	}
	if err != nil {
		return planRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read plan", true, nil)
	}
	if err := json.Unmarshal(diffJSON, &record.Diff); err != nil {
		return planRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "plan diff is invalid", false, nil)
	}
	if err := json.Unmarshal(riskJSON, &record.RiskFlags); err != nil {
		return planRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "plan risk metadata is invalid", false, nil)
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT item_id FROM plan_items WHERE plan_id = ? AND item_type = 'source' ORDER BY item_id`, planID)
	if err != nil {
		return planRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read plan sources", true, nil)
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return planRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode plan source", true, nil)
		}
		record.AffectedSourceIDs = append(record.AffectedSourceIDs, source)
	}
	if err := rows.Err(); err != nil {
		return planRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish plan source read", true, nil)
	}
	return record, nil
}

func (record planRecord) response() planResponse {
	diff := map[string]any{}
	data, _ := json.Marshal(record.Diff)
	_ = json.Unmarshal(data, &diff)
	return planResponse{PlanID: record.ID, JobID: record.JobID, State: record.State, ArticleID: record.ArticleID, Path: record.Diff.Path, BaseVersion: record.BaseVersion, ProposedVersion: record.ProposedVersion, RiskFlags: append([]string(nil), record.RiskFlags...), Diff: diff, ExpiresAt: record.ExpiresAt, AffectedSourceIDs: append([]string(nil), record.AffectedSourceIDs...)}
}

func checkPlanCurrent(ctx context.Context, store *storage.Storage, record planRecord, undo bool) (articleRecord, *protocol.CodedError) {
	article, err := queryArticle(ctx, store.DB, `SELECT article_id, slug, title, path, sensitivity, current_version, current_hash FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, record.ArticleID)
	if record.Kind == "create" {
		if !undo && errors.Is(err, sql.ErrNoRows) {
			// A new article has no metadata row until this plan is applied.
			article = articleRecord{ID: record.ArticleID, Slug: record.Diff.Slug, Path: record.Diff.Path}
		} else if err == nil {
			if !undo {
				return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "new article target was created after preview", false, nil)
			}
			// For undo, the article must exist and match the applied output.
		} else if errors.Is(err, sql.ErrNoRows) {
			return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "new article metadata is missing", false, nil)
		} else {
			return articleRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article metadata", true, nil)
		}
	} else {
		if errors.Is(err, sql.ErrNoRows) {
			return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "article metadata is missing", false, nil)
		}
		if err != nil {
			return articleRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article metadata", true, nil)
		}
	}
	if article.Path == "" {
		article.Path = record.Diff.Path
	}
	if record.Diff.Path != "" && filepath.Clean(article.Path) != filepath.Clean(record.Diff.Path) {
		return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "managed article path changed since the plan boundary", false, nil)
	}
	if !within(article.Path, filepath.Join(store.Paths.Wiki, "articles")) {
		return articleRecord{}, protocol.NewCodedError("PATH_INVALID", "managed article path is outside the Wiki", false, nil)
	}
	if !undo && record.Kind == "create" {
		if _, err := os.Lstat(article.Path); err == nil {
			return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "new article path is no longer available", false, nil)
		} else if !errors.Is(err, os.ErrNotExist) {
			return articleRecord{}, protocol.NewCodedError("WIKI_DRIFT", "new article path cannot be inspected", false, nil)
		}
		return article, nil
	}
	if article.Version != record.ProposedVersion && undo || article.Version != record.BaseVersion && !undo {
		return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "article version changed since the plan boundary", false, map[string]any{"current_version": article.Version})
	}
	expectedHash := record.ProposedHash
	if !undo {
		expectedHash = record.BaseHash
	}
	if article.Hash != expectedHash {
		return articleRecord{}, protocol.NewCodedError("PLAN_STALE", "article hash changed since the plan boundary", false, nil)
	}
	if codedErr := verifyArticleFile(store, article); codedErr != nil {
		return articleRecord{}, codedErr
	}
	return article, nil
}

func verifyArticleFile(store *storage.Storage, article articleRecord) *protocol.CodedError {
	if !within(article.Path, filepath.Join(store.Paths.Wiki, "articles")) {
		return protocol.NewCodedError("PATH_INVALID", "managed article path is outside the Wiki", false, nil)
	}
	info, err := os.Lstat(article.Path)
	if errors.Is(err, os.ErrNotExist) {
		return protocol.NewCodedError("WIKI_DRIFT", "managed article file is missing", false, map[string]any{"path": article.Path})
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return protocol.NewCodedError("WIKI_DRIFT", "managed article file is unavailable", false, map[string]any{"path": article.Path})
	}
	contents, err := os.ReadFile(article.Path)
	if err != nil {
		return protocol.NewCodedError("WIKI_DRIFT", "managed article file cannot be read", false, map[string]any{"path": article.Path})
	}
	if hashBytes(contents) != article.Hash {
		return protocol.NewCodedError("WIKI_DRIFT", "managed article file differs from its recorded hash", false, map[string]any{"path": article.Path, "expected_hash": article.Hash})
	}
	return nil
}

func ensureArticlePath(store *storage.Storage, path string) *protocol.CodedError {
	articleRoot := filepath.Join(store.Paths.Wiki, "articles")
	if !within(path, articleRoot) {
		return protocol.NewCodedError("PATH_INVALID", "managed article path is outside the Wiki", false, nil)
	}
	if err := os.MkdirAll(articleRoot, 0700); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create managed Wiki directory", true, nil)
	}
	if info, err := os.Lstat(articleRoot); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return protocol.NewCodedError("PATH_INVALID", "managed Wiki directory is invalid", false, nil)
	}
	return nil
}

func insertPlanCitations(ctx context.Context, tx *sql.Tx, record planRecord) *protocol.CodedError {
	rows, err := tx.QueryContext(ctx, `SELECT item_id, detail_json FROM plan_items WHERE plan_id = ? AND item_type = 'citation' ORDER BY item_id`, record.ID)
	if err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read plan citations", true, nil)
	}
	defer rows.Close()
	for rows.Next() {
		var itemID string
		var detail []byte
		if err := rows.Scan(&itemID, &detail); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode plan citation", true, nil)
		}
		var citation citation
		if err := json.Unmarshal(detail, &citation); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "plan citation is invalid", false, nil)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO article_citations(article_id, version, source_id, locator) VALUES (?, ?, ?, ?)`, record.ArticleID, record.ProposedVersion, citation.SourceID, citation.Locator); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot persist article citation", true, map[string]any{"item_id": itemID})
		}
	}
	if err := rows.Err(); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish plan citation read", true, nil)
	}
	return nil
}

func articlePreviousContent(store *storage.Storage, article articleRecord) (string, error) {
	// The preview caller has already verified the managed file hash. Re-read and
	// compare once more so undo never stores a raced external edit.
	contents, err := os.ReadFile(article.Path)
	if err != nil {
		return "", err
	}
	if hashBytes(contents) != article.Hash {
		return "", errors.New("managed article hash changed")
	}
	return string(contents), nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func uniqueCitations(values []citation) []citation {
	seen := make(map[string]struct{}, len(values))
	result := make([]citation, 0, len(values))
	for _, value := range values {
		key := value.SourceID + "\x00" + value.Locator
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceID == result[j].SourceID {
			return result[i].Locator < result[j].Locator
		}
		return result[i].SourceID < result[j].SourceID
	})
	return result
}

func collisionSlug(slug string, seed []byte) string {
	suffix := hashBytes(seed)[len("sha256:") : len("sha256:")+8]
	maxBase := 120 - len(suffix) - 1
	if maxBase < 1 {
		maxBase = 1
	}
	if len(slug) > maxBase {
		slug = strings.TrimRight(slug[:maxBase], "-")
	}
	return strings.TrimRight(slug, "-") + "-" + suffix
}

func expired(value string) bool {
	when, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || time.Now().UTC().After(when)
}

func trashPath(store *storage.Storage, record planRecord) string {
	name := filepath.Base(record.Diff.Path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = record.ArticleID + ".md"
	}
	path := filepath.Join(store.Paths.Trash, record.ID+"-"+name)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	return filepath.Join(store.Paths.Trash, record.ID+"-"+strconv.FormatInt(time.Now().UTC().UnixNano(), 10)+"-"+name)
}
