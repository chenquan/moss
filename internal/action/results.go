package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chenquan/moss/internal/knowledge"
	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	maxResultSummaryBytes  = 4000
	maxResultMetadataBytes = 64 << 10
)

type resultArguments struct {
	ActionID    string         `json:"action_id"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	SourceID    string         `json:"source_id,omitempty"`
	Sensitivity string         `json:"sensitivity,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type resultApplyArguments struct {
	PlanID    string `json:"plan_id"`
	Confirmed bool   `json:"confirmed,omitempty"`
}

type resultPayload struct {
	ActionID    string         `json:"action_id"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	SourceID    string         `json:"source_id,omitempty"`
	Sensitivity string         `json:"sensitivity"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type resultPlanData struct {
	PlanID             string         `json:"plan_id"`
	ResultID           string         `json:"result_id"`
	ActionID           string         `json:"action_id"`
	State              string         `json:"state"`
	BaseActionRevision int            `json:"base_action_revision"`
	ResultVersion      int            `json:"result_version"`
	Diff               map[string]any `json:"diff"`
	RiskFlags          []string       `json:"risk_flags"`
	ExpiresAt          string         `json:"expires_at"`
}

type actionResultView struct {
	ResultID    string         `json:"result_id"`
	ActionID    string         `json:"action_id"`
	Version     int            `json:"version"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	SourceID    string         `json:"source_id,omitempty"`
	Sensitivity string         `json:"sensitivity"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

type resultPlanRecord struct {
	PlanID        string
	State         string
	ActionID      string
	ResultID      string
	ResultVersion int
	BaseRevision  int
	Proposed      resultPayload
	Diff          map[string]any
	RiskFlags     []string
	ExpiresAt     string
}

func ResultPlan(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[resultArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	actionID := strings.TrimSpace(args.ActionID)
	if actionID == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "action_id is required", false, nil)
	}
	current, codedErr := loadAction(ctx, store.DB, actionID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	actionSensitivity, codedErr := effectiveActionSensitivity(ctx, store.DB, current)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	payload := resultPayload{ActionID: actionID, Status: strings.TrimSpace(args.Status), Summary: strings.TrimSpace(args.Summary), SourceID: strings.TrimSpace(args.SourceID), Sensitivity: strings.TrimSpace(args.Sensitivity), Metadata: args.Metadata}
	if payload.Sensitivity == "" {
		payload.Sensitivity = actionSensitivity
	}
	if codedErr := validateResultPayload(ctx, store.DB, payload, actionSensitivity, allowSensitive); codedErr != nil {
		return protocol.Response{}, codedErr
	}
	var resultVersion int
	if err := store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM action_results WHERE action_id = ?`, actionID).Scan(&resultVersion); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action result version", true, nil)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action result idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	planID := newID("action-result-plan")
	resultID := newID("result")
	expiresAt := time.Now().UTC().Add(planLifetime).Format(time.RFC3339Nano)
	riskFlags := []string{"new_action_result"}
	if payload.Status == "failed" || payload.Status == "partial" || payload.Status == "unknown" {
		riskFlags = append(riskFlags, "non_success_result")
	}
	if payload.Sensitivity != "normal" {
		riskFlags = append(riskFlags, "sensitive_content")
	}
	diff := map[string]any{"action_id": actionID, "result_id": resultID, "version": resultVersion, "result": payload}
	proposedJSON, _ := json.Marshal(payload)
	diffJSON, _ := json.Marshal(diff)
	riskJSON, _ := json.Marshal(riskFlags)
	data := resultPlanData{PlanID: planID, ResultID: resultID, ActionID: actionID, State: "pending", BaseActionRevision: current.Revision, ResultVersion: resultVersion, Diff: diff, RiskFlags: riskFlags, ExpiresAt: expiresAt}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin action result plan", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve action result plan idempotency", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO action_result_plans(plan_id, state, action_id, result_id, result_version, base_revision, proposed_json, diff_json, risk_json, expires_at, created_at) VALUES (?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, planID, actionID, resultID, resultVersion, current.Revision, proposedJSON, diffJSON, riskJSON, expiresAt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return ProtocolError("cannot create action result plan", err)
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": planID, "result_id": resultID, "action_id": actionID}); err != nil {
		return ProtocolError("cannot audit action result plan", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return ProtocolError("cannot complete action result plan idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return ProtocolError("cannot commit action result plan", err)
	}
	return response, nil
}

func ResultApply(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[resultApplyArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if !args.Confirmed {
		return protocol.Response{}, protocol.NewCodedError("CONFIRMATION_REQUIRED", "action.result.apply requires explicit Skill confirmation", false, nil)
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if stored, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action result apply idempotency state", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	plan, codedErr := loadResultPlan(ctx, store.DB, args.PlanID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if plan.State != "pending" {
		return protocol.Response{}, protocol.NewCodedError("ACTION_RESULT_PLAN_STATE_INVALID", "action result plan is not pending", false, map[string]any{"state": plan.State})
	}
	if expired(plan.ExpiresAt) {
		return protocol.Response{}, protocol.NewCodedError("ACTION_RESULT_PLAN_EXPIRED", "action result plan has expired", false, map[string]any{"expires_at": plan.ExpiresAt})
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin action result apply", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve action result apply idempotency", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	current, codedErr := loadActionTx(ctx, tx, plan.ActionID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	actionSensitivity, codedErr := effectiveActionSensitivity(ctx, tx, current)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if current.Revision != plan.BaseRevision {
		return protocol.Response{}, protocol.NewCodedError("ACTION_RESULT_PLAN_STALE", "action revision changed since result plan creation", false, nil)
	}
	var latestResultVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM action_results WHERE action_id = ?`, plan.ActionID).Scan(&latestResultVersion); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot verify action result version", true, nil)
	}
	if latestResultVersion+1 != plan.ResultVersion {
		return protocol.Response{}, protocol.NewCodedError("ACTION_RESULT_PLAN_STALE", "another action result was recorded since result plan creation", false, nil)
	}
	if codedErr := validateResultPayload(ctx, tx, plan.Proposed, actionSensitivity, allowSensitive); codedErr != nil {
		return protocol.Response{}, codedErr
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	metadataJSON, _ := json.Marshal(plan.Proposed.Metadata)
	if _, err := tx.ExecContext(ctx, `INSERT INTO action_results(result_id, action_id, version, status, summary, source_id, sensitivity, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)`, plan.ResultID, plan.ActionID, plan.ResultVersion, plan.Proposed.Status, plan.Proposed.Summary, plan.Proposed.SourceID, plan.Proposed.Sensitivity, metadataJSON, now); err != nil {
		return ProtocolError("cannot create action result", err)
	}
	relation := knowledge.ResolvedRelation{RelationType: knowledge.RelationProduces, From: knowledge.RelationEndpoint{Type: knowledge.EndpointAction, ID: plan.ActionID, Version: current.Revision}, To: knowledge.RelationEndpoint{Type: knowledge.EndpointActionResult, ID: plan.ResultID, Version: plan.ResultVersion}, SourceID: plan.Proposed.SourceID}
	relationID, relationErr := knowledge.InsertRelationTx(ctx, tx, relation, "action_result", plan.ResultID, now)
	if relationErr != nil {
		return protocol.Response{}, relationErr
	}
	planUpdate, err := tx.ExecContext(ctx, `UPDATE action_result_plans SET state = 'applied', applied_at = ? WHERE plan_id = ? AND state = 'pending'`, now, plan.PlanID)
	if err != nil {
		return ProtocolError("cannot mark action result plan applied", err)
	}
	if affected, _ := planUpdate.RowsAffected(); affected != 1 {
		return protocol.Response{}, protocol.NewCodedError("ACTION_RESULT_PLAN_STALE", "action result plan state changed before apply", false, nil)
	}
	result := actionResultView{ResultID: plan.ResultID, ActionID: plan.ActionID, Version: plan.ResultVersion, Status: plan.Proposed.Status, Summary: plan.Proposed.Summary, SourceID: plan.Proposed.SourceID, Sensitivity: plan.Proposed.Sensitivity, Metadata: plan.Proposed.Metadata, CreatedAt: now}
	responseData := map[string]any{"plan_id": plan.PlanID, "result": result, "relation_id": relationID, "action_status": current.Status}
	response := protocol.NewSuccessResponse(req, responseData)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"plan_id": plan.PlanID, "result_id": plan.ResultID, "relation_id": relationID, "action_status": current.Status}); err != nil {
		return ProtocolError("cannot audit action result apply", err)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return ProtocolError("cannot complete action result apply idempotency", err)
	}
	if err := tx.Commit(); err != nil {
		return ProtocolError("cannot commit action result apply", err)
	}
	return response, nil
}

func validateResultPayload(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, payload resultPayload, actionSensitivity string, allowSensitive bool) *protocol.CodedError {
	if payload.Status != "succeeded" && payload.Status != "failed" && payload.Status != "partial" && payload.Status != "cancelled" && payload.Status != "unknown" {
		return protocol.NewCodedError("ACTION_RESULT_STATUS_INVALID", "result status is not supported", false, nil)
	}
	if payload.Summary == "" || len([]byte(payload.Summary)) > maxResultSummaryBytes {
		return protocol.NewCodedError("ACTION_RESULT_SUMMARY_INVALID", "result summary is required and must be bounded", false, nil)
	}
	if payload.Sensitivity != "normal" && payload.Sensitivity != "sensitive" && payload.Sensitivity != "restricted" {
		return protocol.NewCodedError("SENSITIVITY_INVALID", "result sensitivity is not supported", false, nil)
	}
	if resultSensitivityRank(payload.Sensitivity) < resultSensitivityRank(actionSensitivity) {
		return protocol.NewCodedError("SENSITIVITY_ESCALATION_REQUIRED", "result sensitivity cannot be lower than the action", false, nil)
	}
	if !allowSensitive && resultSensitivityRank(payload.Sensitivity) > 0 {
		return protocol.NewCodedError("SENSITIVITY_DENIED", "action result requires sensitive-content permission", false, nil)
	}
	if payload.SourceID != "" {
		var sensitivity, forgotten string
		if err := q.QueryRowContext(ctx, `SELECT sensitivity, COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, payload.SourceID).Scan(&sensitivity, &forgotten); errors.Is(err, sql.ErrNoRows) {
			return protocol.NewCodedError("SOURCE_NOT_FOUND", "action result source was not found", false, nil)
		} else if err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot validate action result source", true, nil)
		} else if forgotten != "" {
			return protocol.NewCodedError("SOURCE_NOT_FOUND", "action result source was forgotten", false, nil)
		} else if resultSensitivityRank(payload.Sensitivity) < resultSensitivityRank(sensitivity) {
			return protocol.NewCodedError("SENSITIVITY_ESCALATION_REQUIRED", "result sensitivity cannot be lower than its source", false, nil)
		} else if !allowSensitive && resultSensitivityRank(sensitivity) > 0 {
			return protocol.NewCodedError("SENSITIVITY_DENIED", "action result source requires sensitive-content permission", false, nil)
		}
	}
	if payload.Metadata != nil {
		encoded, err := json.Marshal(payload.Metadata)
		if err != nil || len(encoded) > maxResultMetadataBytes {
			return protocol.NewCodedError("ACTION_RESULT_METADATA_INVALID", "result metadata is invalid or too large", false, nil)
		}
	}
	return nil
}

func effectiveActionSensitivity(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, current actionRecord) (string, *protocol.CodedError) {
	sensitivity := current.Sensitivity
	if current.SourceID == "" {
		return sensitivity, nil
	}
	var sourceSensitivity, forgotten string
	err := q.QueryRowContext(ctx, `SELECT sensitivity, COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, current.SourceID).Scan(&sourceSensitivity, &forgotten)
	if errors.Is(err, sql.ErrNoRows) || forgotten != "" {
		return "", protocol.NewCodedError("SOURCE_NOT_FOUND", "action source was not found or was forgotten", false, nil)
	}
	if err != nil {
		return "", protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot validate action source", true, nil)
	}
	if resultSensitivityRank(sourceSensitivity) > resultSensitivityRank(sensitivity) {
		sensitivity = sourceSensitivity
	}
	return sensitivity, nil
}

func loadResultPlan(ctx context.Context, db *sql.DB, planID string) (resultPlanRecord, *protocol.CodedError) {
	if strings.TrimSpace(planID) == "" {
		return resultPlanRecord{}, protocol.NewCodedError("REQUEST_INVALID", "plan_id is required", false, nil)
	}
	var plan resultPlanRecord
	var proposedJSON, diffJSON, riskJSON []byte
	err := db.QueryRowContext(ctx, `SELECT plan_id, state, action_id, result_id, result_version, base_revision, proposed_json, diff_json, risk_json, expires_at FROM action_result_plans WHERE plan_id = ?`, planID).Scan(&plan.PlanID, &plan.State, &plan.ActionID, &plan.ResultID, &plan.ResultVersion, &plan.BaseRevision, &proposedJSON, &diffJSON, &riskJSON, &plan.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return resultPlanRecord{}, protocol.NewCodedError("ACTION_RESULT_PLAN_NOT_FOUND", "action result plan was not found", false, nil)
	}
	if err != nil {
		return resultPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read action result plan", true, nil)
	}
	if err := json.Unmarshal(proposedJSON, &plan.Proposed); err != nil {
		return resultPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action result plan proposal is invalid", false, nil)
	}
	if err := json.Unmarshal(diffJSON, &plan.Diff); err != nil {
		return resultPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action result plan diff is invalid", false, nil)
	}
	if err := json.Unmarshal(riskJSON, &plan.RiskFlags); err != nil {
		return resultPlanRecord{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "action result plan risk is invalid", false, nil)
	}
	return plan, nil
}

func resultSensitivityRank(value string) int {
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
