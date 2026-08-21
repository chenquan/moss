package action

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"moss/internal/protocol"
	"moss/internal/storage"
)

const (
	maxTitleBytes       = 240
	maxDetailsBytes     = 1 << 20
	maxWaitingBytes     = 240
	planLifetime        = 24 * time.Hour
	defaultQueryLimit   = 100
	maxQueryLimit       = 200
	maxActionKinds      = 3
	maxActionStatuses   = 5
	defaultActionStatus = "open"
)

type createArguments struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Details     string `json:"details,omitempty"`
	Status      string `json:"status,omitempty"`
	DueAt       string `json:"due_at,omitempty"`
	WaitingFor  string `json:"waiting_for,omitempty"`
	Sensitivity string `json:"sensitivity,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
}

type updateArguments struct {
	ActionID    string  `json:"action_id"`
	Kind        *string `json:"kind,omitempty"`
	Title       *string `json:"title,omitempty"`
	Details     *string `json:"details,omitempty"`
	Status      *string `json:"status,omitempty"`
	DueAt       *string `json:"due_at,omitempty"`
	WaitingFor  *string `json:"waiting_for,omitempty"`
	Sensitivity *string `json:"sensitivity,omitempty"`
	SourceID    *string `json:"source_id,omitempty"`
}

type applyArguments struct {
	PlanID    string `json:"plan_id"`
	Confirmed bool   `json:"confirmed,omitempty"`
}

type queryArguments struct {
	Date            string `json:"date,omitempty"`
	Status          string `json:"status,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	IncludeComplete bool   `json:"include_completed,omitempty"`
}

type actionPayload struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Details     string `json:"details"`
	Status      string `json:"status"`
	DueAt       string `json:"due_at,omitempty"`
	WaitingFor  string `json:"waiting_for,omitempty"`
	Sensitivity string `json:"sensitivity"`
	SourceID    string `json:"source_id,omitempty"`
}

type actionRecord struct {
	ActionID    string `json:"action_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Details     string `json:"details"`
	Status      string `json:"status"`
	DueAt       string `json:"due_at,omitempty"`
	WaitingFor  string `json:"waiting_for,omitempty"`
	Sensitivity string `json:"sensitivity"`
	SourceID    string `json:"source_id,omitempty"`
	Revision    int    `json:"revision"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type actionView struct {
	ActionID    string `json:"action_id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Details     string `json:"details,omitempty"`
	Status      string `json:"status"`
	DueAt       string `json:"due_at,omitempty"`
	WaitingFor  string `json:"waiting_for,omitempty"`
	Sensitivity string `json:"sensitivity"`
	SourceID    string `json:"source_id,omitempty"`
	Revision    int    `json:"revision"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type planData struct {
	PlanID           string         `json:"plan_id"`
	Kind             string         `json:"kind"`
	ActionID         string         `json:"action_id"`
	State            string         `json:"state"`
	BaseRevision     int            `json:"base_revision"`
	ProposedRevision int            `json:"proposed_revision"`
	Diff             map[string]any `json:"diff"`
	RiskFlags        []string       `json:"risk_flags"`
	ExpiresAt        string         `json:"expires_at"`
}

type queryData struct {
	Date    string       `json:"date"`
	Today   []actionView `json:"today"`
	Overdue []actionView `json:"overdue"`
	Waiting []actionView `json:"waiting"`
	Actions []actionView `json:"actions"`
	Count   int          `json:"count"`
}

type actionPlanRecord struct {
	PlanID       string
	Kind         string
	State        string
	ActionID     string
	BaseRevision int
	Proposed     actionPayload
	Previous     *actionRecord
	Diff         map[string]any
	RiskFlags    []string
	ExpiresAt    string
	CreatedAt    string
	AppliedAt    string
}

func CreatePlan(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[createArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	payload, codedErr := validateCreate(ctx, store, args)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	return createPlan(ctx, store, req, payload, "create", newID("act"), 0, nil)
}

func UpdatePlan(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[updateArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if strings.TrimSpace(args.ActionID) == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "action_id is required", false, nil)
	}
	current, codedErr := loadAction(ctx, store.DB, args.ActionID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	payload := actionPayload{Kind: current.Kind, Title: current.Title, Details: current.Details, Status: current.Status, DueAt: current.DueAt, WaitingFor: current.WaitingFor, Sensitivity: current.Sensitivity, SourceID: current.SourceID}
	changed := make([]string, 0, 8)
	if args.Kind != nil {
		payload.Kind = strings.TrimSpace(*args.Kind)
		changed = append(changed, "kind")
	}
	if args.Title != nil {
		payload.Title = strings.TrimSpace(*args.Title)
		changed = append(changed, "title")
	}
	if args.Details != nil {
		payload.Details = *args.Details
		changed = append(changed, "details")
	}
	if args.Status != nil {
		payload.Status = strings.TrimSpace(*args.Status)
		changed = append(changed, "status")
	}
	if args.DueAt != nil {
		payload.DueAt = strings.TrimSpace(*args.DueAt)
		changed = append(changed, "due_at")
	}
	if args.WaitingFor != nil {
		payload.WaitingFor = strings.TrimSpace(*args.WaitingFor)
		changed = append(changed, "waiting_for")
	}
	if args.Sensitivity != nil {
		payload.Sensitivity = strings.TrimSpace(*args.Sensitivity)
		changed = append(changed, "sensitivity")
	}
	if args.SourceID != nil {
		payload.SourceID = strings.TrimSpace(*args.SourceID)
		changed = append(changed, "source_id")
	}
	if len(changed) == 0 {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "at least one action field must change", false, nil)
	}
	if codedErr := validatePayload(ctx, store, &payload); codedErr != nil {
		return protocol.Response{}, codedErr
	}
	return createPlan(ctx, store, req, payload, "update", current.ActionID, current.Revision, &current)
}

func Apply(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[applyArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if !args.Confirmed {
		return protocol.Response{}, protocol.NewCodedError("CONFIRMATION_REQUIRED", "action.apply requires explicit Skill confirmation", false, nil)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	plan, codedErr := loadPlan(ctx, store.DB, args.PlanID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if plan.State != "pending" {
		return protocol.Response{}, protocol.NewCodedError("ACTION_PLAN_STATE_INVALID", "action plan is not pending", false, map[string]any{"state": plan.State})
	}
	if expired(plan.ExpiresAt) {
		return protocol.Response{}, protocol.NewCodedError("ACTION_PLAN_EXPIRED", "action plan has expired", false, map[string]any{"expires_at": plan.ExpiresAt})
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin action apply transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve action apply idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var applied actionRecord
	if plan.Kind == "create" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO actions(action_id, kind, title, details, status, due_at, waiting_for, sensitivity, source_id, revision, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), 1, ?, ?, ?)`, plan.ActionID, plan.Proposed.Kind, plan.Proposed.Title, plan.Proposed.Details, plan.Proposed.Status, nullable(plan.Proposed.DueAt), nullable(plan.Proposed.WaitingFor), plan.Proposed.Sensitivity, plan.Proposed.SourceID, now, now, nullable(completionTime(plan.Proposed.Status, now))); err != nil {
			return ProtocolError("cannot create action", err)
		}
		applied = actionRecord{ActionID: plan.ActionID, Kind: plan.Proposed.Kind, Title: plan.Proposed.Title, Details: plan.Proposed.Details, Status: plan.Proposed.Status, DueAt: plan.Proposed.DueAt, WaitingFor: plan.Proposed.WaitingFor, Sensitivity: plan.Proposed.Sensitivity, SourceID: plan.Proposed.SourceID, Revision: 1, CreatedAt: now, UpdatedAt: now, CompletedAt: completionTime(plan.Proposed.Status, now)}
	} else {
		current, codedErr := loadActionTx(ctx, tx, plan.ActionID)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		if current.Revision != plan.BaseRevision {
			return protocol.Response{}, protocol.NewCodedError("ACTION_PLAN_STALE", "action revision changed since plan creation", false, map[string]any{"current_revision": current.Revision, "base_revision": plan.BaseRevision})
		}
		result, err := tx.ExecContext(ctx, `UPDATE actions SET kind = ?, title = ?, details = ?, status = ?, due_at = ?, waiting_for = ?, sensitivity = ?, source_id = NULLIF(?, ''), revision = revision + 1, updated_at = ?, completed_at = ? WHERE action_id = ? AND revision = ? AND forgotten_at IS NULL`, plan.Proposed.Kind, plan.Proposed.Title, plan.Proposed.Details, plan.Proposed.Status, nullable(plan.Proposed.DueAt), nullable(plan.Proposed.WaitingFor), plan.Proposed.Sensitivity, plan.Proposed.SourceID, now, nullable(completionTime(plan.Proposed.Status, now)), plan.ActionID, plan.BaseRevision)
		if err != nil {
			return ProtocolError("cannot update action", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return protocol.Response{}, protocol.NewCodedError("ACTION_PLAN_STALE", "action revision changed before apply", false, nil)
		}
		applied = actionRecord{ActionID: plan.ActionID, Kind: plan.Proposed.Kind, Title: plan.Proposed.Title, Details: plan.Proposed.Details, Status: plan.Proposed.Status, DueAt: plan.Proposed.DueAt, WaitingFor: plan.Proposed.WaitingFor, Sensitivity: plan.Proposed.Sensitivity, SourceID: plan.Proposed.SourceID, Revision: plan.BaseRevision + 1, CreatedAt: current.CreatedAt, UpdatedAt: now, CompletedAt: completionTime(plan.Proposed.Status, now)}
	}
	planUpdate, err := tx.ExecContext(ctx, `UPDATE action_plans SET state = 'applied', applied_at = ? WHERE plan_id = ? AND state = 'pending'`, now, plan.PlanID)
	if err != nil {
		return ProtocolError("cannot mark action plan applied", err)
	}
	if affected, _ := planUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("ACTION_PLAN_STALE", "action plan state changed before apply", false, nil)
	}
	response := protocol.NewSuccessResponse(req, applied.view())
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": plan.PlanID, "action_id": applied.ActionID, "revision": applied.Revision}); err != nil {
		return ProtocolError("cannot write action audit event", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return ProtocolError("cannot complete action idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return ProtocolError("cannot commit action apply", err)
	}
	return response, nil
}

func Query(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[queryArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	limit, codedErr := boundedLimit(args.Limit)
	if codedErr != nil {
		return nil, codedErr
	}
	status := strings.TrimSpace(args.Status)
	if status != "" && !validStatus(status) {
		return nil, protocol.NewCodedError("ACTION_STATUS_INVALID", "status is not supported", false, nil)
	}
	date := strings.TrimSpace(args.Date)
	if date == "" {
		date = time.Now().Local().Format("2006-01-02")
	}
	if _, err := time.ParseInLocation("2006-01-02", date, time.Local); err != nil {
		return nil, protocol.NewCodedError("DATE_INVALID", "date must use YYYY-MM-DD", false, nil)
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT action_id, kind, title, details, status, COALESCE(due_at, ''), COALESCE(waiting_for, ''), sensitivity, COALESCE(source_id, ''), revision, created_at, updated_at, COALESCE(completed_at, '') FROM actions WHERE forgotten_at IS NULL ORDER BY action_id ASC`)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot query action ledger", true, nil)
	}
	defer rows.Close()
	all := make([]actionRecord, 0)
	for rows.Next() {
		var value actionRecord
		if err := rows.Scan(&value.ActionID, &value.Kind, &value.Title, &value.Details, &value.Status, &value.DueAt, &value.WaitingFor, &value.Sensitivity, &value.SourceID, &value.Revision, &value.CreatedAt, &value.UpdatedAt, &value.CompletedAt); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode action ledger", true, nil)
		}
		if !sensitivityAllowed(value.Sensitivity, allowSensitive) || (status != "" && value.Status != status) || (!args.IncludeComplete && terminal(value.Status)) {
			continue
		}
		all = append(all, value)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish action query", true, nil)
	}
	sortActions(all)
	result := queryData{Date: date, Today: make([]actionView, 0), Overdue: make([]actionView, 0), Waiting: make([]actionView, 0), Actions: make([]actionView, 0)}
	for _, value := range all {
		view := value.view()
		result.Actions = append(result.Actions, view)
		bucket := dueBucket(value.DueAt, date)
		if bucket == "today" {
			result.Today = append(result.Today, view)
		} else if bucket == "overdue" {
			result.Overdue = append(result.Overdue, view)
		}
		if value.WaitingFor != "" {
			result.Waiting = append(result.Waiting, view)
		}
		if len(result.Actions) >= limit {
			break
		}
	}
	result.Count = len(result.Actions)
	return result, nil
}

func createPlan(ctx context.Context, store *storage.Storage, req protocol.Request, payload actionPayload, kind, actionID string, baseRevision int, previous *actionRecord) (protocol.Response, *protocol.CodedError) {
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	planID := newID("action-plan")
	expiresAt := time.Now().UTC().Add(planLifetime).Format(time.RFC3339Nano)
	riskFlags := []string{}
	if kind == "create" {
		riskFlags = append(riskFlags, "new_action")
	}
	if payload.Status == "cancelled" {
		riskFlags = append(riskFlags, "cancellation")
	}
	if payload.Sensitivity != "normal" {
		riskFlags = append(riskFlags, "sensitive_content")
	}
	if previous != nil && previous.DueAt != payload.DueAt {
		riskFlags = append(riskFlags, "schedule_change")
	}
	riskFlags = uniqueStrings(riskFlags)
	diff := map[string]any{"kind": kind, "action_id": actionID, "fields": payload}
	diffJSON, _ := json.Marshal(diff)
	riskJSON, _ := json.Marshal(riskFlags)
	proposedJSON, _ := json.Marshal(payload)
	var previousJSON any
	if previous != nil {
		data, _ := json.Marshal(previous)
		previousJSON = data
	}
	data := planData{PlanID: planID, Kind: kind, ActionID: actionID, State: "pending", BaseRevision: baseRevision, ProposedRevision: baseRevision + 1, Diff: diff, RiskFlags: riskFlags, ExpiresAt: expiresAt}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin action plan transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return ProtocolError("cannot reserve action plan idempotency key", err)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO action_plans(plan_id, kind, state, action_id, base_revision, proposed_json, previous_json, diff_json, risk_json, expires_at, created_at) VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?)`, planID, kind, actionID, baseRevision, proposedJSON, previousJSON, diffJSON, riskJSON, expiresAt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return ProtocolError("cannot create action plan", err)
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": planID, "action_id": actionID, "kind": kind}); err != nil {
		return ProtocolError("cannot write action plan audit event", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return ProtocolError("cannot complete action plan idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return ProtocolError("cannot commit action plan", err)
	}
	return response, nil
}

func validateCreate(ctx context.Context, store *storage.Storage, args createArguments) (actionPayload, *protocol.CodedError) {
	status := strings.TrimSpace(args.Status)
	if status == "" {
		status = defaultActionStatus
	}
	sensitivity := strings.TrimSpace(args.Sensitivity)
	if sensitivity == "" {
		sensitivity = "normal"
	}
	payload := actionPayload{Kind: strings.TrimSpace(args.Kind), Title: strings.TrimSpace(args.Title), Details: args.Details, Status: status, DueAt: strings.TrimSpace(args.DueAt), WaitingFor: strings.TrimSpace(args.WaitingFor), Sensitivity: sensitivity, SourceID: strings.TrimSpace(args.SourceID)}
	if codedErr := validatePayload(ctx, store, &payload); codedErr != nil {
		return actionPayload{}, codedErr
	}
	return payload, nil
}

func validatePayload(ctx context.Context, store *storage.Storage, payload *actionPayload) *protocol.CodedError {
	if payload.Kind != "task" && payload.Kind != "commitment" && payload.Kind != "reminder" {
		return protocol.NewCodedError("ACTION_KIND_INVALID", "kind is not supported", false, nil)
	}
	if payload.Title == "" || len([]byte(payload.Title)) > maxTitleBytes {
		return protocol.NewCodedError("ACTION_TITLE_INVALID", "title is required and must be short", false, nil)
	}
	if len([]byte(payload.Details)) > maxDetailsBytes {
		return protocol.NewCodedError("ACTION_DETAILS_TOO_LARGE", "details exceed the maximum size", false, nil)
	}
	if !validStatus(payload.Status) {
		return protocol.NewCodedError("ACTION_STATUS_INVALID", "status is not supported", false, nil)
	}
	if payload.Sensitivity != "normal" && payload.Sensitivity != "sensitive" && payload.Sensitivity != "restricted" {
		return protocol.NewCodedError("SENSITIVITY_INVALID", "sensitivity is not supported", false, nil)
	}
	if len([]byte(payload.WaitingFor)) > maxWaitingBytes {
		return protocol.NewCodedError("ACTION_WAITING_INVALID", "waiting_for is too long", false, nil)
	}
	if payload.DueAt != "" {
		normalized, err := normalizeDue(payload.DueAt)
		if err != nil {
			return protocol.NewCodedError("DUE_AT_INVALID", "due_at must be an RFC3339 timestamp or YYYY-MM-DD", false, nil)
		}
		payload.DueAt = normalized
	}
	if payload.SourceID != "" {
		var found, sourceSensitivity string
		if err := store.DB.QueryRowContext(ctx, `SELECT source_id, sensitivity FROM sources WHERE source_id = ? AND forgotten_at IS NULL`, payload.SourceID).Scan(&found, &sourceSensitivity); errors.Is(err, sql.ErrNoRows) {
			return protocol.NewCodedError("SOURCE_NOT_FOUND", "action source was not found", false, nil)
		} else if err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot validate action source", true, nil)
		}
		if sensitivityRank(payload.Sensitivity) < sensitivityRank(sourceSensitivity) {
			return protocol.NewCodedError("SENSITIVITY_ESCALATION_REQUIRED", "action sensitivity cannot be lower than its source", false, nil)
		}
	}
	return nil
}

func loadPlan(ctx context.Context, db *sql.DB, planID string) (actionPlanRecord, *protocol.CodedError) {
	if strings.TrimSpace(planID) == "" {
		return actionPlanRecord{}, protocol.NewCodedError("REQUEST_INVALID", "plan_id is required", false, nil)
	}
	var plan actionPlanRecord
	var proposedJSON, previousJSON, diffJSON, riskJSON []byte
	err := db.QueryRowContext(ctx, `SELECT plan_id, kind, state, action_id, base_revision, proposed_json, previous_json, diff_json, risk_json, expires_at, created_at, COALESCE(applied_at, '') FROM action_plans WHERE plan_id = ?`, planID).Scan(&plan.PlanID, &plan.Kind, &plan.State, &plan.ActionID, &plan.BaseRevision, &proposedJSON, &previousJSON, &diffJSON, &riskJSON, &plan.ExpiresAt, &plan.CreatedAt, &plan.AppliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return actionPlanRecord{}, protocol.NewCodedError("ACTION_PLAN_NOT_FOUND", "action plan was not found", false, nil)
	}
	if err != nil {
		return actionPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action plan", true, nil)
	}
	if err := json.Unmarshal(proposedJSON, &plan.Proposed); err != nil {
		return actionPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action plan proposal is invalid", false, nil)
	}
	if len(previousJSON) > 0 {
		plan.Previous = &actionRecord{}
		if err := json.Unmarshal(previousJSON, plan.Previous); err != nil {
			return actionPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action plan previous snapshot is invalid", false, nil)
		}
	}
	if err := json.Unmarshal(diffJSON, &plan.Diff); err != nil || plan.Diff == nil {
		return actionPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action plan diff is invalid", false, nil)
	}
	if err := json.Unmarshal(riskJSON, &plan.RiskFlags); err != nil {
		return actionPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action plan risk is invalid", false, nil)
	}
	return plan, nil
}

func loadAction(ctx context.Context, db *sql.DB, actionID string) (actionRecord, *protocol.CodedError) {
	var action actionRecord
	err := db.QueryRowContext(ctx, `SELECT action_id, kind, title, details, status, COALESCE(due_at, ''), COALESCE(waiting_for, ''), sensitivity, COALESCE(source_id, ''), revision, created_at, updated_at, COALESCE(completed_at, '') FROM actions WHERE action_id = ? AND forgotten_at IS NULL`, actionID).Scan(&action.ActionID, &action.Kind, &action.Title, &action.Details, &action.Status, &action.DueAt, &action.WaitingFor, &action.Sensitivity, &action.SourceID, &action.Revision, &action.CreatedAt, &action.UpdatedAt, &action.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return actionRecord{}, protocol.NewCodedError("ACTION_NOT_FOUND", "action was not found", false, nil)
	}
	if err != nil {
		return actionRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action", true, nil)
	}
	return action, nil
}

func loadActionTx(ctx context.Context, tx *sql.Tx, actionID string) (actionRecord, *protocol.CodedError) {
	var action actionRecord
	err := tx.QueryRowContext(ctx, `SELECT action_id, kind, title, details, status, COALESCE(due_at, ''), COALESCE(waiting_for, ''), sensitivity, COALESCE(source_id, ''), revision, created_at, updated_at, COALESCE(completed_at, '') FROM actions WHERE action_id = ? AND forgotten_at IS NULL`, actionID).Scan(&action.ActionID, &action.Kind, &action.Title, &action.Details, &action.Status, &action.DueAt, &action.WaitingFor, &action.Sensitivity, &action.SourceID, &action.Revision, &action.CreatedAt, &action.UpdatedAt, &action.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return actionRecord{}, protocol.NewCodedError("ACTION_NOT_FOUND", "action was not found", false, nil)
	}
	if err != nil {
		return actionRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action", true, nil)
	}
	return action, nil
}

func (action actionRecord) view() actionView {
	return actionView{ActionID: action.ActionID, Kind: action.Kind, Title: action.Title, Details: action.Details, Status: action.Status, DueAt: action.DueAt, WaitingFor: action.WaitingFor, Sensitivity: action.Sensitivity, SourceID: action.SourceID, Revision: action.Revision, CreatedAt: action.CreatedAt, UpdatedAt: action.UpdatedAt, CompletedAt: action.CompletedAt}
}

func validStatus(value string) bool {
	switch value {
	case "open", "in_progress", "done", "deferred", "cancelled":
		return true
	default:
		return false
	}
}

func terminal(value string) bool { return value == "done" || value == "cancelled" }

func sensitivityAllowed(value string, allowSensitive bool) bool {
	return allowSensitive || value == "normal"
}

func sensitivityRank(value string) int {
	switch value {
	case "normal":
		return 0
	case "sensitive":
		return 1
	case "restricted":
		return 2
	default:
		return -1
	}
}

func normalizeDue(value string) (string, error) {
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return value, nil
	}
	timestamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", err
	}
	return timestamp.UTC().Format(time.RFC3339Nano), nil
}

func dueBucket(dueAt, date string) string {
	if dueAt == "" {
		return ""
	}
	if len(dueAt) == len("2006-01-02") {
		if dueAt == date {
			return "today"
		}
		if dueAt < date {
			return "overdue"
		}
		return ""
	}
	timestamp, err := time.Parse(time.RFC3339Nano, dueAt)
	if err != nil {
		return ""
	}
	localDate := timestamp.In(time.Local).Format("2006-01-02")
	if localDate == date {
		return "today"
	}
	if localDate < date {
		return "overdue"
	}
	return ""
}

func sortActions(values []actionRecord) {
	sort.SliceStable(values, func(i, j int) bool {
		leftDue, rightDue := values[i].DueAt, values[j].DueAt
		if leftDue == "" && rightDue != "" {
			return false
		}
		if leftDue != "" && rightDue == "" {
			return true
		}
		if leftDue != rightDue {
			return leftDue < rightDue
		}
		if values[i].Status != values[j].Status {
			return values[i].Status < values[j].Status
		}
		return values[i].ActionID < values[j].ActionID
	})
}

func boundedLimit(value int) (int, *protocol.CodedError) {
	if value == 0 {
		return defaultQueryLimit, nil
	}
	if value < 1 || value > maxQueryLimit {
		return 0, protocol.NewCodedError("REQUEST_INVALID", "limit is outside the supported range", false, map[string]any{"max": maxQueryLimit})
	}
	return value, nil
}

func completionTime(status, now string) string {
	if terminal(status) {
		return now
	}
	return ""
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func expired(value string) bool {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || time.Now().UTC().After(timestamp)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func newID(prefix string) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%s_%s_%s", prefix, time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:]))
}

func ProtocolError(message string, err error) (protocol.Response, *protocol.CodedError) {
	return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", message, true, err.Error())
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
