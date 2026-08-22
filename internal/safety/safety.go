package safety

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenquan/moss/internal/knowledge"
	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	planLifetime       = 24 * time.Hour
	maxAuditLimit      = 200
	maxAuditSummary    = 64 << 10
	maxPlanTargetBytes = 8 << 20
)

type forgetArguments struct {
	SourceIDs      []string `json:"source_ids,omitempty"`
	OriginContains string   `json:"origin_contains,omitempty"`
}

type rollbackArguments struct {
	ArticleID     string `json:"article_id,omitempty"`
	Slug          string `json:"slug,omitempty"`
	TargetVersion int    `json:"target_version"`
}

type applyArguments struct {
	PlanID    string `json:"plan_id"`
	Confirmed bool   `json:"confirmed,omitempty"`
}

type auditArguments struct {
	Limit     int    `json:"limit,omitempty"`
	Operation string `json:"operation,omitempty"`
}

type planData struct {
	PlanID    string         `json:"plan_id"`
	Kind      string         `json:"kind"`
	State     string         `json:"state"`
	Impact    map[string]any `json:"impact"`
	Diff      map[string]any `json:"diff"`
	RiskFlags []string       `json:"risk_flags"`
	ExpiresAt string         `json:"expires_at"`
}

type safetyPlan struct {
	PlanID    string
	Kind      string
	State     string
	Target    json.RawMessage
	Previous  json.RawMessage
	Impact    map[string]any
	Diff      map[string]any
	RiskFlags []string
	ExpiresAt string
	CreatedAt string
	AppliedAt string
	UndoneAt  string
}

type forgetTarget struct {
	Sources  []forgetSource  `json:"sources"`
	Blobs    []forgetBlob    `json:"blobs"`
	Articles []forgetArticle `json:"articles"`
	Facts    []forgetFact    `json:"facts"`
	Actions  []forgetAction  `json:"actions"`
	Jobs     []forgetJob     `json:"jobs"`
}

type forgetFact struct {
	FactID  string `json:"fact_id"`
	Version int    `json:"version"`
	Status  string `json:"status"`
	FactKey string `json:"fact_key"`
}

type forgetSource struct {
	SourceID    string `json:"source_id"`
	ContentHash string `json:"content_hash"`
	RawPath     string `json:"raw_path"`
	ForgottenAt string `json:"forgotten_at,omitempty"`
}

type forgetBlob struct {
	ContentHash string `json:"content_hash"`
	RawPath     string `json:"raw_path"`
	TrashPath   string `json:"trash_path,omitempty"`
	Move        bool   `json:"move"`
}

type forgetArticle struct {
	ArticleID      string `json:"article_id"`
	Path           string `json:"path"`
	TrashPath      string `json:"trash_path"`
	CurrentHash    string `json:"current_hash"`
	Version        int    `json:"version"`
	CurrentContent string `json:"current_content"`
	ForgottenAt    string `json:"forgotten_at,omitempty"`
}

type forgetAction struct {
	ActionID    string `json:"action_id"`
	ForgottenAt string `json:"forgotten_at,omitempty"`
}

type forgetJob struct {
	JobID        string `json:"job_id"`
	State        string `json:"state"`
	CurrentStage string `json:"current_stage,omitempty"`
}

type rollbackTarget struct {
	ArticleID      string `json:"article_id"`
	Path           string `json:"path"`
	CurrentVersion int    `json:"current_version"`
	CurrentHash    string `json:"current_hash"`
	TargetVersion  int    `json:"target_version"`
	TargetHash     string `json:"target_hash"`
	TargetContent  string `json:"target_content"`
}

type rollbackPrevious struct {
	ArticleID      string `json:"article_id"`
	Path           string `json:"path"`
	CurrentVersion int    `json:"current_version"`
	CurrentHash    string `json:"current_hash"`
	CurrentContent string `json:"current_content"`
}

type auditEntry struct {
	AuditID        string         `json:"audit_id"`
	Operation      string         `json:"operation"`
	RequestID      string         `json:"request_id"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Status         string         `json:"status"`
	Summary        map[string]any `json:"summary,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

type auditData struct {
	Entries []auditEntry `json:"entries"`
	Count   int          `json:"count"`
}

func ForgetPlan(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[forgetArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if len(args.SourceIDs) == 0 && strings.TrimSpace(args.OriginContains) == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "source_ids or origin_contains is required", false, nil)
	}
	target, impact, diff, riskFlags, codedErr := resolveForget(ctx, store, args)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	return createPlan(ctx, store, req, "forget", target, nil, impact, diff, riskFlags)
}

func RollbackPlan(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[rollbackArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if args.TargetVersion < 1 || (strings.TrimSpace(args.ArticleID) == "" && strings.TrimSpace(args.Slug) == "") || (strings.TrimSpace(args.ArticleID) != "" && strings.TrimSpace(args.Slug) != "") {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "exactly one article selector and a positive target_version are required", false, nil)
	}
	current, codedErr := loadArticle(ctx, store, args.ArticleID, args.Slug)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if err := verifyFile(store, current.Path, current.Hash, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
		return protocol.Response{}, err
	}
	if args.TargetVersion >= current.Version {
		return protocol.Response{}, protocol.NewCodedError("ROLLBACK_TARGET_INVALID", "target version must be older than the current version", false, nil)
	}
	var targetHash, targetContent string
	if err := store.DB.QueryRowContext(ctx, `SELECT content_hash, content FROM article_versions WHERE article_id = ? AND version = ?`, current.ID, args.TargetVersion).Scan(&targetHash, &targetContent); errors.Is(err, sql.ErrNoRows) {
		return protocol.Response{}, protocol.NewCodedError("VERSION_NOT_FOUND", "target article version was not found", false, nil)
	} else if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read rollback target version", true, nil)
	} else if hashBytes([]byte(targetContent)) != targetHash {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "rollback target content hash is invalid", false, nil)
	}
	target := rollbackTarget{ArticleID: current.ID, Path: current.Path, CurrentVersion: current.Version, CurrentHash: current.Hash, TargetVersion: args.TargetVersion, TargetHash: targetHash, TargetContent: targetContent}
	previous := rollbackPrevious{ArticleID: current.ID, Path: current.Path, CurrentVersion: current.Version, CurrentHash: current.Hash}
	currentContent, err := os.ReadFile(current.Path)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("WIKI_DRIFT", "cannot read current article for rollback", false, nil)
	}
	previous.CurrentContent = string(currentContent)
	return createPlan(ctx, store, req, "rollback", target, &previous, map[string]any{"articles": 1, "article_ids": []string{current.ID}}, map[string]any{"kind": "rollback", "from_version": current.Version, "to_version": args.TargetVersion}, []string{"rollback"})
}

func Inspect(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	plan, codedErr := loadPlan(ctx, store.DB, planIDFromRequest(req))
	if codedErr != nil {
		return nil, codedErr
	}
	return planData{PlanID: plan.PlanID, Kind: plan.Kind, State: plan.State, Impact: plan.Impact, Diff: plan.Diff, RiskFlags: plan.RiskFlags, ExpiresAt: plan.ExpiresAt}, nil
}

func Apply(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[applyArguments](req)
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
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read plan idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	plan, codedErr := loadPlan(ctx, store.DB, args.PlanID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if plan.State != "pending" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "safety plan is not pending", false, map[string]any{"state": plan.State})
	}
	if expired(plan.ExpiresAt) {
		return protocol.Response{}, protocol.NewCodedError("PLAN_EXPIRED", "safety plan has expired", false, map[string]any{"expires_at": plan.ExpiresAt})
	}
	markerPath := filepath.Join(store.Paths.Staging, plan.PlanID+".safety.json")
	marker := map[string]any{"plan_id": plan.PlanID, "kind": plan.Kind, "phase": "prepared"}
	marker["target_json"] = string(plan.Target)
	if plan.Kind == "rollback" {
		var target rollbackTarget
		var previous rollbackPrevious
		if json.Unmarshal(plan.Target, &target) == nil && json.Unmarshal(plan.Previous, &previous) == nil {
			marker["article_id"] = target.ArticleID
			marker["target"] = target.Path
			marker["before_hash"] = target.CurrentHash
			marker["after_hash"] = target.TargetHash
			marker["before_content"] = previous.CurrentContent
		}
	}
	markerBytes, _ := json.Marshal(marker)
	if err := writePrivateFile(markerPath, markerBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create safety recovery marker", true, nil)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(markerPath)
		}
	}()
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin safety plan transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve safety apply idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	if plan.Kind == "forget" {
		if codedErr := applyForget(ctx, store, tx, plan, func() { cleanup = false }); codedErr != nil {
			return protocol.Response{}, codedErr
		}
	} else {
		if codedErr := applyRollback(ctx, store, tx, plan, func() { cleanup = false }); codedErr != nil {
			return protocol.Response{}, codedErr
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := tx.ExecContext(ctx, `UPDATE safety_plans SET state = 'applied', applied_at = ? WHERE plan_id = ? AND state = 'pending'`, now, plan.PlanID)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark safety plan applied", true, nil)
	}
	if affected, _ := updated.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "safety plan state changed before apply", false, nil)
	}
	data := planData{PlanID: plan.PlanID, Kind: plan.Kind, State: "applied", Impact: plan.Impact, Diff: plan.Diff, RiskFlags: plan.RiskFlags, ExpiresAt: plan.ExpiresAt}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": plan.PlanID, "kind": plan.Kind, "impact": plan.Impact}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write safety apply audit", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete safety apply idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit safety plan", true, nil)
	}
	cleanup = false
	_ = os.Remove(markerPath)
	return response, nil
}

func Undo(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[applyArguments](req)
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
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read plan idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	plan, codedErr := loadPlan(ctx, store.DB, args.PlanID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if plan.State != "applied" {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STATE_INVALID", "safety plan is not applied", false, map[string]any{"state": plan.State})
	}
	markerPath := filepath.Join(store.Paths.Staging, plan.PlanID+"-undo.safety.json")
	marker := map[string]any{"plan_id": plan.PlanID, "kind": plan.Kind, "undo": true, "phase": "prepared"}
	marker["target_json"] = string(plan.Target)
	if plan.Kind == "rollback" {
		var target rollbackTarget
		var previous rollbackPrevious
		if json.Unmarshal(plan.Target, &target) == nil && json.Unmarshal(plan.Previous, &previous) == nil {
			marker["article_id"] = target.ArticleID
			marker["target"] = target.Path
			marker["before_hash"] = target.TargetHash
			marker["after_hash"] = previous.CurrentHash
			marker["before_content"] = target.TargetContent
		}
	}
	markerBytes, _ := json.Marshal(marker)
	if err := writePrivateFile(markerPath, markerBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create safety undo marker", true, nil)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(markerPath)
		}
	}()
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin safety undo transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve safety undo idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	if plan.Kind == "forget" {
		if codedErr := undoForget(ctx, store, tx, plan, func() { cleanup = false }); codedErr != nil {
			return protocol.Response{}, codedErr
		}
	} else {
		if codedErr := undoRollback(ctx, store, tx, plan, func() { cleanup = false }); codedErr != nil {
			return protocol.Response{}, codedErr
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := tx.ExecContext(ctx, `UPDATE safety_plans SET state = 'undone', undone_at = ? WHERE plan_id = ? AND state = 'applied'`, now, plan.PlanID)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark safety plan undone", true, nil)
	}
	if affected, _ := updated.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("PLAN_STALE", "safety plan state changed before undo", false, nil)
	}
	data := planData{PlanID: plan.PlanID, Kind: plan.Kind, State: "undone", Impact: plan.Impact, Diff: plan.Diff, RiskFlags: plan.RiskFlags, ExpiresAt: plan.ExpiresAt}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": plan.PlanID, "kind": plan.Kind}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write safety undo audit", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete safety undo idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit safety undo", true, nil)
	}
	cleanup = false
	_ = os.Remove(markerPath)
	return response, nil
}

func AuditQuery(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[auditArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	limit := args.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > maxAuditLimit {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "audit limit is outside the supported range", false, map[string]any{"max": maxAuditLimit})
	}
	query := `SELECT audit_id, operation, request_id, COALESCE(idempotency_key, ''), status, COALESCE(summary_json, ''), created_at FROM audit_events`
	params := []any{}
	if strings.TrimSpace(args.Operation) != "" {
		query += ` WHERE operation = ?`
		params = append(params, strings.TrimSpace(args.Operation))
	}
	query += ` ORDER BY created_at DESC, audit_id DESC LIMIT ?`
	params = append(params, limit)
	rows, err := store.DB.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot query audit events", true, nil)
	}
	defer rows.Close()
	result := auditData{Entries: make([]auditEntry, 0)}
	for rows.Next() {
		var entry auditEntry
		var summary []byte
		if err := rows.Scan(&entry.AuditID, &entry.Operation, &entry.RequestID, &entry.IdempotencyKey, &entry.Status, &summary, &entry.CreatedAt); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode audit event", true, nil)
		}
		if len(summary) > maxAuditSummary {
			entry.Summary = map[string]any{"redacted": true, "reason": "summary_too_large"}
		} else if len(summary) > 0 {
			if err := json.Unmarshal(summary, &entry.Summary); err != nil {
				entry.Summary = map[string]any{"redacted": true, "reason": "summary_invalid"}
			}
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish audit query", true, nil)
	}
	result.Count = len(result.Entries)
	return result, nil
}

func resolveForget(ctx context.Context, store *storage.Storage, args forgetArguments) (forgetTarget, map[string]any, map[string]any, []string, *protocol.CodedError) {
	selected := make(map[string]struct{})
	for _, id := range args.SourceIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			selected[id] = struct{}{}
		}
	}
	if strings.TrimSpace(args.OriginContains) != "" {
		rows, err := store.DB.QueryContext(ctx, `SELECT source_id FROM sources WHERE forgotten_at IS NULL AND (lower(origin_name) LIKE '%' || lower(?) || '%' OR lower(origin_key) LIKE '%' || lower(?) || '%') ORDER BY source_id`, strings.TrimSpace(args.OriginContains), strings.TrimSpace(args.OriginContains))
		if err != nil {
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot resolve source origin selector", true, nil)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode source selector", true, nil)
			}
			selected[id] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("SOURCE_NOT_FOUND", "no active sources matched the forget selector", false, nil)
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	target := forgetTarget{Sources: make([]forgetSource, 0), Blobs: make([]forgetBlob, 0), Articles: make([]forgetArticle, 0), Facts: make([]forgetFact, 0), Actions: make([]forgetAction, 0), Jobs: make([]forgetJob, 0)}
	for _, id := range ids {
		var source forgetSource
		if err := store.DB.QueryRowContext(ctx, `SELECT s.source_id, s.content_hash, b.raw_path, COALESCE(s.forgotten_at, '') FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.source_id = ? AND s.forgotten_at IS NULL`, id).Scan(&source.SourceID, &source.ContentHash, &source.RawPath, &source.ForgottenAt); errors.Is(err, sql.ErrNoRows) {
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("SOURCE_NOT_FOUND", "one or more sources were not found or already forgotten", false, map[string]any{"source_id": id})
		} else if err != nil {
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read forget source", true, nil)
		}
		target.Sources = append(target.Sources, source)
	}
	selectedSet := selected
	blobMap := make(map[string]*forgetBlob)
	for _, source := range target.Sources {
		blob := blobMap[source.ContentHash]
		if blob == nil {
			blob = &forgetBlob{ContentHash: source.ContentHash, RawPath: source.RawPath, Move: true, TrashPath: filepath.ToSlash(filepath.Join("trash", "forget-pending-"+source.ContentHash))}
			blobMap[source.ContentHash] = blob
		}
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT source_id, content_hash FROM sources WHERE forgotten_at IS NULL ORDER BY source_id`)
	if err != nil {
		return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect shared source blobs", true, nil)
	}
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect source blobs", true, nil)
		}
		if _, chosen := selectedSet[id]; !chosen {
			if blob := blobMap[hash]; blob != nil {
				blob.Move = false
			}
		}
	}
	rows.Close()
	for _, blob := range blobMap {
		if blob.Move {
			blob.TrashPath = filepath.ToSlash(filepath.Join("trash", "forget-"+newID("blob")+"-"+strings.TrimPrefix(blob.ContentHash, "sha256:")[:12]))
		}
		target.Blobs = append(target.Blobs, *blob)
	}
	sort.Slice(target.Blobs, func(i, j int) bool { return target.Blobs[i].ContentHash < target.Blobs[j].ContentHash })
	// Articles cited by selected sources.
	articleRows, err := store.DB.QueryContext(ctx, `SELECT DISTINCT a.article_id, a.path, a.current_hash, a.current_version, COALESCE(a.forgotten_at, '') FROM articles a JOIN article_citations c ON c.article_id = a.article_id WHERE a.forgotten_at IS NULL AND c.source_id IN (`+placeholders(len(ids))+") ORDER BY a.article_id", stringSlice(ids)...)
	if err != nil {
		return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot resolve cited articles", true, nil)
	}
	for articleRows.Next() {
		var article forgetArticle
		if err := articleRows.Scan(&article.ArticleID, &article.Path, &article.CurrentHash, &article.Version, &article.ForgottenAt); err != nil {
			articleRows.Close()
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode cited article", true, nil)
		}
		if err := verifyFile(store, article.Path, article.CurrentHash, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
			articleRows.Close()
			return forgetTarget{}, nil, nil, nil, err
		}
		content, err := os.ReadFile(article.Path)
		if err != nil {
			articleRows.Close()
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read cited article", true, nil)
		}
		article.CurrentContent = string(content)
		article.TrashPath = filepath.ToSlash(filepath.Join("trash", "forget-article-"+newID("file")+"-"+article.ArticleID+".md"))
		target.Articles = append(target.Articles, article)
	}
	articleRows.Close()
	factRows, err := store.DB.QueryContext(ctx, `SELECT DISTINCT f.fact_id, f.current_version, f.status, f.fact_key FROM facts f JOIN fact_citations c ON c.fact_id = f.fact_id AND c.version = f.current_version WHERE f.status <> 'retracted' AND c.source_id IN (`+placeholders(len(ids))+") ORDER BY f.fact_id", stringSlice(ids)...)
	if err != nil {
		return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot resolve cited facts", true, nil)
	}
	for factRows.Next() {
		var fact forgetFact
		if err := factRows.Scan(&fact.FactID, &fact.Version, &fact.Status, &fact.FactKey); err != nil {
			factRows.Close()
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode cited fact", true, nil)
		}
		target.Facts = append(target.Facts, fact)
	}
	factRows.Close()
	actionRows, err := store.DB.QueryContext(ctx, `SELECT action_id, COALESCE(forgotten_at, '') FROM actions WHERE forgotten_at IS NULL AND source_id IN (`+placeholders(len(ids))+") ORDER BY action_id", stringSlice(ids)...)
	if err != nil {
		return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot resolve source actions", true, nil)
	}
	for actionRows.Next() {
		var action forgetAction
		if err := actionRows.Scan(&action.ActionID, &action.ForgottenAt); err != nil {
			actionRows.Close()
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode source action", true, nil)
		}
		target.Actions = append(target.Actions, action)
	}
	actionRows.Close()
	jobRows, err := store.DB.QueryContext(ctx, `SELECT job_id, state, COALESCE(current_stage, '') FROM compile_jobs WHERE source_id IN (`+placeholders(len(ids))+") ORDER BY job_id", stringSlice(ids)...)
	if err != nil {
		return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot resolve source jobs", true, nil)
	}
	for jobRows.Next() {
		var job forgetJob
		if err := jobRows.Scan(&job.JobID, &job.State, &job.CurrentStage); err != nil {
			jobRows.Close()
			return forgetTarget{}, nil, nil, nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode source job", true, nil)
		}
		target.Jobs = append(target.Jobs, job)
	}
	jobRows.Close()
	impact := map[string]any{"sources": len(target.Sources), "raw_blobs": len(target.Blobs), "raw_blobs_to_trash": countMovableBlobs(target.Blobs), "articles": len(target.Articles), "facts": len(target.Facts), "actions": len(target.Actions), "compile_jobs": len(target.Jobs), "source_ids": ids}
	diff := map[string]any{"kind": "forget", "source_ids": ids}
	risk := []string{"privacy_impact", "reversible_trash"}
	return target, impact, diff, risk, nil
}

func createPlan(ctx context.Context, store *storage.Storage, req protocol.Request, kind string, target any, previous any, impact, diff map[string]any, risk []string) (protocol.Response, *protocol.CodedError) {
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read safety plan idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	planID := newID("plan")
	targetJSON, _ := json.Marshal(target)
	if len(targetJSON) > maxPlanTargetBytes {
		return protocol.Response{}, protocol.NewCodedError("PLAN_TOO_LARGE", "safety plan target exceeds the maximum size", false, nil)
	}
	previousJSON, _ := json.Marshal(previous)
	impactJSON, _ := json.Marshal(impact)
	diffJSON, _ := json.Marshal(diff)
	riskJSON, _ := json.Marshal(uniqueStrings(risk))
	expiresAt := time.Now().UTC().Add(planLifetime).Format(time.RFC3339Nano)
	data := planData{PlanID: planID, Kind: kind, State: "pending", Impact: impact, Diff: diff, RiskFlags: uniqueStrings(risk), ExpiresAt: expiresAt}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin safety plan transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve safety plan idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO safety_plans(plan_id, kind, state, target_json, previous_json, impact_json, diff_json, risk_json, expires_at, created_at) VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?)`, planID, kind, targetJSON, nullableBytes(previousJSON), impactJSON, diffJSON, riskJSON, expiresAt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create safety plan", true, nil)
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": planID, "kind": kind, "impact": impact}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write safety plan audit", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete safety plan idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit safety plan", true, nil)
	}
	return response, nil
}

func loadPlan(ctx context.Context, db *sql.DB, planID string) (safetyPlan, *protocol.CodedError) {
	if strings.TrimSpace(planID) == "" {
		return safetyPlan{}, protocol.NewCodedError("REQUEST_INVALID", "plan_id is required", false, nil)
	}
	var plan safetyPlan
	var impactJSON, diffJSON, riskJSON []byte
	var previousJSON []byte
	err := db.QueryRowContext(ctx, `SELECT plan_id, kind, state, target_json, previous_json, impact_json, diff_json, risk_json, expires_at, created_at, COALESCE(applied_at, ''), COALESCE(undone_at, '') FROM safety_plans WHERE plan_id = ?`, planID).Scan(&plan.PlanID, &plan.Kind, &plan.State, &plan.Target, &previousJSON, &impactJSON, &diffJSON, &riskJSON, &plan.ExpiresAt, &plan.CreatedAt, &plan.AppliedAt, &plan.UndoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return safetyPlan{}, protocol.NewCodedError("PLAN_NOT_FOUND", "plan was not found", false, nil)
	}
	if err != nil {
		return safetyPlan{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read safety plan", true, nil)
	}
	plan.Previous = json.RawMessage(previousJSON)
	if err := json.Unmarshal(impactJSON, &plan.Impact); err != nil || plan.Impact == nil {
		return safetyPlan{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "safety plan impact is invalid", false, nil)
	}
	if err := json.Unmarshal(diffJSON, &plan.Diff); err != nil || plan.Diff == nil {
		return safetyPlan{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "safety plan diff is invalid", false, nil)
	}
	if err := json.Unmarshal(riskJSON, &plan.RiskFlags); err != nil {
		return safetyPlan{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "safety plan risk is invalid", false, nil)
	}
	return plan, nil
}

func applyForget(ctx context.Context, store *storage.Storage, tx *sql.Tx, plan safetyPlan, onFinalization func()) *protocol.CodedError {
	var target forgetTarget
	if err := json.Unmarshal(plan.Target, &target); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "forget target is invalid", false, nil)
	}
	for _, source := range target.Sources {
		var hash, rawPath, forgotten string
		if err := tx.QueryRowContext(ctx, `SELECT s.content_hash, b.raw_path, COALESCE(s.forgotten_at, '') FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.source_id = ?`, source.SourceID).Scan(&hash, &rawPath, &forgotten); err != nil {
			return planStaleOrStorage(err, "forget source changed")
		}
		if hash != source.ContentHash || rawPath != source.RawPath || forgotten != "" {
			return protocol.NewCodedError("PLAN_STALE", "forget source snapshot changed", false, map[string]any{"source_id": source.SourceID})
		}
	}
	for _, article := range target.Articles {
		var path, hash, forgotten string
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT path, current_hash, current_version, COALESCE(forgotten_at, '') FROM articles WHERE article_id = ?`, article.ArticleID).Scan(&path, &hash, &version, &forgotten); err != nil {
			return planStaleOrStorage(err, "forget article changed")
		}
		if path != article.Path || hash != article.CurrentHash || version != article.Version || forgotten != "" {
			return protocol.NewCodedError("PLAN_STALE", "forget article snapshot changed", false, map[string]any{"article_id": article.ArticleID})
		}
		if err := verifyFile(store, path, hash, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
			return err
		}
	}
	for _, fact := range target.Facts {
		var version int
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT current_version, status FROM facts WHERE fact_id = ?`, fact.FactID).Scan(&version, &status); err != nil {
			return planStaleOrStorage(err, "forget fact changed")
		}
		if version != fact.Version || status != fact.Status {
			return protocol.NewCodedError("PLAN_STALE", "forget fact snapshot changed", false, map[string]any{"fact_id": fact.FactID})
		}
	}
	for _, action := range target.Actions {
		var forgotten string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(forgotten_at, '') FROM actions WHERE action_id = ?`, action.ActionID).Scan(&forgotten); err != nil {
			return planStaleOrStorage(err, "forget action changed")
		}
		if forgotten != "" {
			return protocol.NewCodedError("PLAN_STALE", "forget action snapshot changed", false, map[string]any{"action_id": action.ActionID})
		}
	}
	for _, blob := range target.Blobs {
		if !blob.Move {
			continue
		}
		if err := verifyFile(store, blob.RawPath, blob.ContentHash, store.Paths.Raw); err != nil {
			return err
		}
		if err := ensureTrashTargetAbsent(store, blob.TrashPath); err != nil {
			return err
		}
	}
	for _, article := range target.Articles {
		if err := ensureTrashTargetAbsent(store, article.TrashPath); err != nil {
			return err
		}
	}
	for _, blob := range target.Blobs {
		if !blob.Move {
			continue
		}
		onFinalization()
		if err := moveManaged(store, blob.RawPath, blob.TrashPath, store.Paths.Raw); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE blobs SET raw_path = ? WHERE content_hash = ? AND raw_path = ?`, blob.TrashPath, blob.ContentHash, blob.RawPath); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot update forgotten blob path", true, nil)
		}
	}
	for _, article := range target.Articles {
		onFinalization()
		if err := moveManaged(store, article.Path, article.TrashPath, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE articles SET path = ?, forgotten_at = ? WHERE article_id = ? AND path = ? AND current_hash = ?`, article.TrashPath, time.Now().UTC().Format(time.RFC3339Nano), article.ArticleID, article.Path, article.CurrentHash); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark forgotten article", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM article_fts WHERE article_id = ?`, article.ArticleID); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot remove forgotten article index", true, nil)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, source := range target.Sources {
		if _, err := tx.ExecContext(ctx, `UPDATE sources SET forgotten_at = ? WHERE source_id = ? AND forgotten_at IS NULL`, now, source.SourceID); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark forgotten source", true, nil)
		}
	}
	for _, fact := range target.Facts {
		var text string
		if err := tx.QueryRowContext(ctx, `SELECT text FROM fact_versions WHERE fact_id = ? AND version = ?`, fact.FactID, fact.Version).Scan(&text); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read fact version for retraction", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fact_versions SET status = 'superseded' WHERE fact_id = ? AND version = ?`, fact.FactID, fact.Version); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot supersede forgotten fact version", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE facts SET current_version = ?, status = 'retracted', updated_at = ? WHERE fact_id = ? AND current_version = ? AND status = ?`, fact.Version+1, now, fact.FactID, fact.Version, fact.Status); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot retract forgotten fact", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fact_versions(fact_id, version, text, status, created_at) VALUES (?, ?, ?, 'retracted', ?)`, fact.FactID, fact.Version+1, text, now); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot record forgotten fact retraction", true, nil)
		}
	}
	for _, action := range target.Actions {
		if _, err := tx.ExecContext(ctx, `UPDATE actions SET forgotten_at = ?, updated_at = ? WHERE action_id = ? AND forgotten_at IS NULL`, now, now, action.ActionID); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark forgotten action", true, nil)
		}
	}
	for _, job := range target.Jobs {
		if _, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'forgotten', current_stage = NULL, updated_at = ? WHERE job_id = ? AND state = ?`, now, job.JobID, job.State); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark forgotten compile job", true, nil)
		}
	}
	return nil
}

func undoForget(ctx context.Context, store *storage.Storage, tx *sql.Tx, plan safetyPlan, onFinalization func()) *protocol.CodedError {
	var target forgetTarget
	if err := json.Unmarshal(plan.Target, &target); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "forget target is invalid", false, nil)
	}
	for _, blob := range target.Blobs {
		if blob.Move {
			if err := verifyFile(store, blob.TrashPath, blob.ContentHash, store.Paths.Trash); err != nil {
				return err
			}
		}
	}
	for _, article := range target.Articles {
		if err := verifyFile(store, article.TrashPath, article.CurrentHash, store.Paths.Trash); err != nil {
			return err
		}
	}
	for _, blob := range target.Blobs {
		if blob.Move {
			if err := ensureTargetAbsent(store, blob.RawPath, store.Paths.Raw); err != nil {
				return err
			}
		}
	}
	for _, article := range target.Articles {
		if err := ensureTargetAbsent(store, article.Path, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
			return err
		}
	}
	for _, blob := range target.Blobs {
		if !blob.Move {
			continue
		}
		onFinalization()
		if err := moveManaged(store, blob.TrashPath, blob.RawPath, store.Paths.Trash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE blobs SET raw_path = ? WHERE content_hash = ? AND raw_path = ?`, blob.RawPath, blob.ContentHash, blob.TrashPath); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore blob path", true, nil)
		}
	}
	for _, article := range target.Articles {
		onFinalization()
		if err := moveManaged(store, article.TrashPath, article.Path, store.Paths.Trash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE articles SET path = ?, forgotten_at = NULL WHERE article_id = ? AND path = ? AND forgotten_at IS NOT NULL`, article.Path, article.ArticleID, article.TrashPath); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore article path", true, nil)
		}
		parsed, parseErr := knowledge.ParseManagedArticle([]byte(article.CurrentContent))
		if parseErr != nil || parsed.ArticleID != article.ArticleID || parsed.Version != article.Version {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "forgotten article projection is invalid", false, map[string]any{"article_id": article.ArticleID})
		}
		if err := storage.ReplaceArticleIndexTx(ctx, tx, article.ArticleID, article.Version, parsed.Title, parsed.Slug, strings.Join(parsed.Tags, " "), parsed.Summary, parsed.Body, strings.Join(parsed.SourceIDs, " ")); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore forgotten article index", true, nil)
		}
	}
	for _, source := range target.Sources {
		if _, err := tx.ExecContext(ctx, `UPDATE sources SET forgotten_at = NULL WHERE source_id = ? AND forgotten_at IS NOT NULL`, source.SourceID); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore source", true, nil)
		}
	}
	for _, fact := range target.Facts {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT current_version FROM facts WHERE fact_id = ? AND status = 'retracted'`, fact.FactID).Scan(&current); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read forgotten fact version", true, nil)
		}
		var text string
		if err := tx.QueryRowContext(ctx, `SELECT text FROM fact_versions WHERE fact_id = ? AND version = ?`, fact.FactID, fact.Version).Scan(&text); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read forgotten fact text", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fact_versions SET status = 'superseded' WHERE fact_id = ? AND version = ?`, fact.FactID, current); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot supersede retraction version", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE facts SET current_version = ?, status = ?, updated_at = ? WHERE fact_id = ? AND current_version = ? AND status = 'retracted'`, current+1, fact.Status, now, fact.FactID, current); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore forgotten fact", true, nil)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fact_versions(fact_id, version, text, status, created_at) VALUES (?, ?, ?, ?, ?)`, fact.FactID, current+1, text, fact.Status, now); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot record restored fact version", true, nil)
		}
	}
	for _, action := range target.Actions {
		if _, err := tx.ExecContext(ctx, `UPDATE actions SET forgotten_at = NULL, updated_at = ? WHERE action_id = ? AND forgotten_at IS NOT NULL`, time.Now().UTC().Format(time.RFC3339Nano), action.ActionID); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore action", true, nil)
		}
	}
	for _, job := range target.Jobs {
		if _, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = ?, current_stage = ?, updated_at = ? WHERE job_id = ? AND state = 'forgotten'`, job.State, nullable(job.CurrentStage), time.Now().UTC().Format(time.RFC3339Nano), job.JobID); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore compile job", true, nil)
		}
	}
	return nil
}

func applyRollback(ctx context.Context, store *storage.Storage, tx *sql.Tx, plan safetyPlan, onFinalization func()) *protocol.CodedError {
	var target rollbackTarget
	if err := json.Unmarshal(plan.Target, &target); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "rollback target is invalid", false, nil)
	}
	var version int
	var hash, path string
	if err := tx.QueryRowContext(ctx, `SELECT current_version, current_hash, path FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, target.ArticleID).Scan(&version, &hash, &path); errors.Is(err, sql.ErrNoRows) {
		return protocol.NewCodedError("PLAN_STALE", "rollback article is unavailable", false, nil)
	} else if err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read rollback article", true, nil)
	}
	if version != target.CurrentVersion || hash != target.CurrentHash || path != target.Path {
		return protocol.NewCodedError("PLAN_STALE", "rollback article version changed", false, nil)
	}
	if err := verifyFile(store, path, hash, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
		return err
	}
	parsed, err := knowledge.ParseManagedArticle([]byte(target.TargetContent))
	if err != nil || parsed.ArticleID != target.ArticleID || parsed.Version != target.TargetVersion {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "rollback target metadata is invalid", false, nil)
	}
	staged := filepath.Join(store.Paths.Staging, plan.PlanID+"-rollback.md")
	if err := writePrivateFile(staged, []byte(target.TargetContent)); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot stage rollback article", true, nil)
	}
	onFinalization()
	if err := os.Rename(staged, path); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot apply rollback article", true, nil)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE articles SET title = ?, slug = ?, sensitivity = ?, current_version = ?, current_hash = ?, updated_at = ? WHERE article_id = ? AND current_version = ? AND current_hash = ?`, parsed.Title, parsed.Slug, parsed.Sensitivity, target.TargetVersion, target.TargetHash, time.Now().UTC().Format(time.RFC3339Nano), target.ArticleID, target.CurrentVersion, target.CurrentHash); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot update rollback article", true, nil)
	}
	if err := replaceRollbackCitations(ctx, tx, parsed); err != nil {
		return err
	}
	if err := storage.ReplaceArticleIndexTx(ctx, tx, target.ArticleID, target.TargetVersion, parsed.Title, parsed.Slug, strings.Join(parsed.Tags, " "), parsed.Summary, parsed.Body, strings.Join(parsed.SourceIDs, " ")); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot update rollback article index", true, nil)
	}
	return nil
}

func undoRollback(ctx context.Context, store *storage.Storage, tx *sql.Tx, plan safetyPlan, onFinalization func()) *protocol.CodedError {
	var target rollbackTarget
	var previous rollbackPrevious
	if err := json.Unmarshal(plan.Target, &target); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "rollback target is invalid", false, nil)
	}
	if err := json.Unmarshal(plan.Previous, &previous); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "rollback previous snapshot is invalid", false, nil)
	}
	var version int
	var hash, path string
	if err := tx.QueryRowContext(ctx, `SELECT current_version, current_hash, path FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, target.ArticleID).Scan(&version, &hash, &path); err != nil {
		return protocol.NewCodedError("PLAN_STALE", "rollback article is unavailable", false, nil)
	}
	if version != target.TargetVersion || hash != target.TargetHash || path != target.Path {
		return protocol.NewCodedError("PLAN_STALE", "rollback target changed before undo", false, nil)
	}
	if err := verifyFile(store, path, hash, filepath.Join(store.Paths.Wiki, "articles")); err != nil {
		return err
	}
	parsed, err := knowledge.ParseManagedArticle([]byte(previous.CurrentContent))
	if err != nil || parsed.ArticleID != previous.ArticleID || parsed.Version != previous.CurrentVersion {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "rollback undo metadata is invalid", false, nil)
	}
	staged := filepath.Join(store.Paths.Staging, plan.PlanID+"-rollback-undo.md")
	if err := writePrivateFile(staged, []byte(previous.CurrentContent)); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot stage rollback undo", true, nil)
	}
	onFinalization()
	if err := os.Rename(staged, path); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore rollback article", true, nil)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE articles SET title = ?, slug = ?, sensitivity = ?, current_version = ?, current_hash = ?, updated_at = ? WHERE article_id = ? AND current_version = ? AND current_hash = ?`, parsed.Title, parsed.Slug, parsed.Sensitivity, previous.CurrentVersion, previous.CurrentHash, time.Now().UTC().Format(time.RFC3339Nano), previous.ArticleID, target.TargetVersion, target.TargetHash); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore rollback metadata", true, nil)
	}
	if err := replaceRollbackCitations(ctx, tx, parsed); err != nil {
		return err
	}
	if err := storage.ReplaceArticleIndexTx(ctx, tx, previous.ArticleID, previous.CurrentVersion, parsed.Title, parsed.Slug, strings.Join(parsed.Tags, " "), parsed.Summary, parsed.Body, strings.Join(parsed.SourceIDs, " ")); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore rollback article index", true, nil)
	}
	return nil
}

func replaceRollbackCitations(ctx context.Context, tx *sql.Tx, article knowledge.ManagedArticle) *protocol.CodedError {
	if _, err := tx.ExecContext(ctx, `DELETE FROM article_citations WHERE article_id = ? AND version = ?`, article.ArticleID, article.Version); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot replace rollback article citations", true, nil)
	}
	for _, citation := range article.Citations {
		if _, err := tx.ExecContext(ctx, `INSERT INTO article_citations(article_id, version, source_id, locator) VALUES (?, ?, ?, ?)`, article.ArticleID, article.Version, citation.SourceID, citation.Locator); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore rollback article citation", true, nil)
		}
	}
	return nil
}

func loadArticle(ctx context.Context, store *storage.Storage, articleID, slug string) (struct {
	ID, Path, Hash string
	Version        int
}, *protocol.CodedError) {
	var row struct {
		ID, Path, Hash string
		Version        int
	}
	var err error
	if articleID != "" {
		err = store.DB.QueryRowContext(ctx, `SELECT article_id, path, current_hash, current_version FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, articleID).Scan(&row.ID, &row.Path, &row.Hash, &row.Version)
	} else {
		err = store.DB.QueryRowContext(ctx, `SELECT article_id, path, current_hash, current_version FROM articles WHERE slug = ? AND forgotten_at IS NULL`, slug).Scan(&row.ID, &row.Path, &row.Hash, &row.Version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return row, protocol.NewCodedError("ARTICLE_NOT_FOUND", "article was not found", false, nil)
	}
	if err != nil {
		return row, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read article", true, nil)
	}
	return row, nil
}

func verifyFile(store *storage.Storage, path, expectedHash, root string) *protocol.CodedError {
	if !filepath.IsAbs(path) {
		path = filepath.Join(store.Paths.Root, filepath.FromSlash(path))
	}
	if !within(path, root) {
		return protocol.NewCodedError("PATH_INVALID", "managed path is outside the expected directory", false, nil)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return protocol.NewCodedError("WIKI_DRIFT", "managed file is missing", false, map[string]any{"path": path})
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return protocol.NewCodedError("WIKI_DRIFT", "managed file is unavailable", false, map[string]any{"path": path})
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return protocol.NewCodedError("WIKI_DRIFT", "managed file cannot be read", false, map[string]any{"path": path})
	}
	if hashBytes(contents) != expectedHash {
		return protocol.NewCodedError("WIKI_DRIFT", "managed file hash differs from the plan", false, map[string]any{"path": path})
	}
	return nil
}

func moveManaged(store *storage.Storage, source, target, root string) *protocol.CodedError {
	if !filepath.IsAbs(source) {
		source = filepath.Join(store.Paths.Root, filepath.FromSlash(source))
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(store.Paths.Root, filepath.FromSlash(target))
	}
	if !within(source, root) && !within(source, store.Paths.Trash) {
		return protocol.NewCodedError("PATH_INVALID", "source path is outside managed storage", false, nil)
	}
	if !within(target, store.Paths.Trash) && !within(target, store.Paths.Raw) && !within(target, filepath.Join(store.Paths.Wiki, "articles")) {
		return protocol.NewCodedError("PATH_INVALID", "target path is outside managed storage", false, nil)
	}
	if _, err := os.Lstat(target); err == nil {
		return protocol.NewCodedError("PLAN_STALE", "trash target already exists", false, map[string]any{"path": target})
	} else if !errors.Is(err, os.ErrNotExist) {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect managed move target", true, nil)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create managed trash directory", true, nil)
	}
	if err := os.Rename(source, target); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot move managed file", true, nil)
	}
	return nil
}

func ensureTrashTargetAbsent(store *storage.Storage, path string) *protocol.CodedError {
	return ensureTargetAbsent(store, path, store.Paths.Trash)
}

func ensureTargetAbsent(store *storage.Storage, path, root string) *protocol.CodedError {
	if !filepath.IsAbs(path) {
		path = filepath.Join(store.Paths.Root, filepath.FromSlash(path))
	}
	if !within(path, root) {
		return protocol.NewCodedError("PATH_INVALID", "managed target is outside the expected directory", false, nil)
	}
	if _, err := os.Lstat(path); err == nil {
		return protocol.NewCodedError("PLAN_STALE", "managed target already exists", false, map[string]any{"path": path})
	} else if !errors.Is(err, os.ErrNotExist) {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect managed target", true, nil)
	}
	return nil
}

func planIDFromRequest(req protocol.Request) string {
	var args applyArguments
	decoded, err := json.Marshal(req.Arguments)
	if err != nil {
		return ""
	}
	_ = json.Unmarshal(decoded, &args)
	return args.PlanID
}

func planStaleOrStorage(err error, message string) *protocol.CodedError {
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.NewCodedError("PLAN_STALE", message, false, nil)
	}
	return protocol.NewCodedError("STORAGE_UNHEALTHY", message, true, nil)
}

func countMovableBlobs(blobs []forgetBlob) int {
	count := 0
	for _, blob := range blobs {
		if blob.Move {
			count++
		}
	}
	return count
}

func placeholders(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func stringSlice(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return value
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func expired(value string) bool {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || time.Now().UTC().After(timestamp)
}

func hashBytes(contents []byte) string {
	hash := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func newID(prefix string) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%s_%s_%s", prefix, time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:]))
}

func writePrivateFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func within(path, root string) bool {
	path, _ = filepath.Abs(path)
	root, _ = filepath.Abs(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func replayOrConflict(record storage.IdempotencyRecord, fingerprint string) (protocol.Response, *protocol.CodedError) {
	if record.Fingerprint != fingerprint {
		return protocol.Response{}, protocol.NewCodedError("IDEMPOTENCY_CONFLICT", "idempotency key was used with different arguments", false, nil)
	}
	if record.Status == "processing" {
		return protocol.Response{}, protocol.NewCodedError("IDEMPOTENCY_IN_PROGRESS", "an identical request is already being processed", true, nil)
	}
	var response protocol.Response
	if err := json.Unmarshal(record.Response, &response); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored idempotency response is invalid", false, nil)
	}
	return response, nil
}
