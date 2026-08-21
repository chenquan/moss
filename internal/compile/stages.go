package compile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moss/internal/protocol"
	"moss/internal/storage"
)

type observation struct {
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids"`
}

type extractResult struct {
	Facts         []observation `json:"facts"`
	Decisions     []observation `json:"decisions"`
	Preferences   []observation `json:"preferences"`
	Projects      []observation `json:"projects"`
	People        []observation `json:"people"`
	Relationships []observation `json:"relationships"`
	Actions       []observation `json:"actions"`
	Conflicts     []observation `json:"conflicts"`
	Citations     []citation    `json:"citations"`
}

type category struct {
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	SourceIDs []string `json:"source_ids"`
}

type classifyResult struct {
	Categories []category `json:"categories"`
	Outline    []struct {
		Heading string   `json:"heading"`
		Bullets []string `json:"bullets"`
	} `json:"outline"`
}

type submitResponse struct {
	JobID      string `json:"job_id"`
	Stage      string `json:"stage"`
	State      string `json:"state"`
	NextStage  string `json:"next_stage,omitempty"`
	ResultHash string `json:"result_hash"`
}

func Submit(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[submitArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if strings.TrimSpace(args.JobID) == "" || strings.TrimSpace(args.Stage) == "" || strings.TrimSpace(args.ResultFile) == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "job_id, stage, and result_file are required", false, nil)
	}
	if !validStage(args.Stage) {
		return protocol.Response{}, protocol.NewCodedError("STAGE_INVALID", "stage is not supported", false, map[string]any{"supported": stageOrder})
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
	if job.State != "running" {
		return protocol.Response{}, protocol.NewCodedError("JOB_STATE_INVALID", "job is not accepting stage submissions", false, map[string]any{"state": job.State})
	}
	var stageStatus, expectedResult, sourceID string
	err = store.DB.QueryRowContext(ctx, `SELECT cs.status, cs.result_path, cj.source_id FROM compile_stages cs JOIN compile_jobs cj ON cj.job_id = cs.job_id WHERE cs.job_id = ? AND cs.stage = ?`, args.JobID, args.Stage).Scan(&stageStatus, &expectedResult, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Response{}, protocol.NewCodedError("STAGE_NOT_FOUND", "compile stage was not found", false, nil)
	}
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile stage", true, nil)
	}
	if stageStatus == "submitted" {
		return protocol.Response{}, protocol.NewCodedError("STAGE_ALREADY_SUBMITTED", "stage was already submitted", false, nil)
	}
	if args.Stage != job.CurrentStage {
		return protocol.Response{}, protocol.NewCodedError("STAGE_OUT_OF_ORDER", "stage is not the current available stage", false, map[string]any{"current_stage": job.CurrentStage})
	}
	if stageStatus != "available" {
		return protocol.Response{}, protocol.NewCodedError("STAGE_NOT_AVAILABLE", "stage is not available for submission", false, nil)
	}
	jobRoot := filepath.Join(store.Paths.Jobs, args.JobID)
	expectedPath := absoluteRef(store.Paths.Root, expectedResult)
	resultPath := args.ResultFile
	if !filepath.IsAbs(resultPath) {
		resultPath = absoluteRef(store.Paths.Root, resultPath)
	}
	if filepath.Clean(resultPath) != filepath.Clean(expectedPath) || !within(resultPath, jobRoot) {
		return protocol.Response{}, protocol.NewCodedError("PATH_INVALID", "result_file must be the current stage result file", false, nil)
	}
	contents, err := readStageResult(resultPath)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_UNREADABLE", "stage result cannot be read", false, nil)
	}
	if err := Validate(args.Stage, contents); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "stage result does not satisfy its JSON Schema", false, err.Error())
	}
	if codedErr := validateReferences(ctx, store, args.Stage, contents, sourceID); codedErr != nil {
		return protocol.Response{}, codedErr
	}
	resultHash := hashBytes(contents)
	nextStage := nextStage(args.Stage)
	state := "running"
	if nextStage == "" {
		state = "preview_ready"
	}
	data := submitResponse{JobID: args.JobID, Stage: args.Stage, State: state, NextStage: nextStage, ResultHash: resultHash}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, err := json.Marshal(response)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot encode stage response", false, nil)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin stage transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve stage idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE compile_stages SET status = 'submitted', result_hash = ?, submitted_at = ? WHERE job_id = ? AND stage = ?`, resultHash, now, args.JobID, args.Stage); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot update compile stage", true, nil)
	}
	if nextStage == "" {
		_, err = tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'preview_ready', current_stage = NULL, updated_at = ? WHERE job_id = ?`, now, args.JobID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE compile_stages SET status = 'available' WHERE job_id = ? AND stage = ?`, args.JobID, nextStage)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE compile_jobs SET current_stage = ?, updated_at = ? WHERE job_id = ?`, nextStage, now, args.JobID)
		}
	}
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot advance compile job", true, nil)
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"job_id": args.JobID, "stage": args.Stage, "result_hash": resultHash}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write stage audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete stage idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit stage transition", true, nil)
	}
	return response, nil
}

func Abort(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[jobArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
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
	if job.State == "applied" {
		return protocol.Response{}, protocol.NewCodedError("JOB_ALREADY_APPLIED", "an applied job cannot be aborted", false, nil)
	}
	if job.State == "aborted" {
		return protocol.Response{}, protocol.NewCodedError("JOB_ALREADY_ABORTED", "job is already aborted", false, nil)
	}
	response := protocol.NewSuccessResponse(req, map[string]any{"job_id": args.JobID, "state": "aborted"})
	responseBytes, _ := json.Marshal(response)
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin abort transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve abort idempotency key", true, nil)
	} else if found {
		return replayOrConflict(stored, fingerprint)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE compile_jobs SET state = 'aborted', current_stage = NULL, updated_at = ? WHERE job_id = ?`, now, args.JobID); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot abort compile job", true, nil)
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"job_id": args.JobID}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write abort audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete abort idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit abort", true, nil)
	}
	return response, nil
}

func validateReferences(ctx context.Context, store *storage.Storage, stage string, contents []byte, sourceID string) *protocol.CodedError {
	allowed := map[string]bool{sourceID: true}
	check := func(ids []string) *protocol.CodedError {
		for _, id := range ids {
			if !allowed[id] {
				return protocol.NewCodedError("SOURCE_REFERENCE_INVALID", "stage result references a source outside the compile job", false, map[string]any{"source_id": id})
			}
		}
		return nil
	}
	switch stage {
	case "extract":
		var value extractResult
		if err := json.Unmarshal(contents, &value); err != nil {
			return protocol.NewCodedError("STAGE_RESULT_INVALID", "extract result cannot be decoded", false, nil)
		}
		for _, group := range [][]observation{value.Facts, value.Decisions, value.Preferences, value.Projects, value.People, value.Relationships, value.Actions, value.Conflicts} {
			for _, item := range group {
				if codedErr := check(item.SourceIDs); codedErr != nil {
					return codedErr
				}
			}
		}
		for _, item := range value.Citations {
			if codedErr := check([]string{item.SourceID}); codedErr != nil {
				return codedErr
			}
		}
	case "classify":
		var value classifyResult
		if err := json.Unmarshal(contents, &value); err != nil {
			return protocol.NewCodedError("STAGE_RESULT_INVALID", "classify result cannot be decoded", false, nil)
		}
		for _, item := range value.Categories {
			if codedErr := check(item.SourceIDs); codedErr != nil {
				return codedErr
			}
		}
	case "write":
		var value writeCandidate
		if err := json.Unmarshal(contents, &value); err != nil {
			return protocol.NewCodedError("STAGE_RESULT_INVALID", "write result cannot be decoded", false, nil)
		}
		if codedErr := check(value.SourceIDs); codedErr != nil {
			return codedErr
		}
		for _, item := range value.Citations {
			if codedErr := check([]string{item.SourceID}); codedErr != nil {
				return codedErr
			}
		}
		var sourceSensitivity string
		if err := store.DB.QueryRowContext(ctx, `SELECT sensitivity FROM sources WHERE source_id = ? AND forgotten_at IS NULL`, sourceID).Scan(&sourceSensitivity); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read source sensitivity", true, nil)
		}
		if sensitivityRank(value.Sensitivity) < sensitivityRank(sourceSensitivity) {
			return protocol.NewCodedError("SENSITIVITY_ESCALATION_REQUIRED", "article sensitivity cannot be lower than its source sensitivity", false, nil)
		}
	}
	return nil
}

func readStageResult(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStageBytes {
		return nil, fmt.Errorf("invalid stage result file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxStageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > maxStageBytes {
		return nil, fmt.Errorf("stage result exceeds maximum size")
	}
	return contents, nil
}

func validStage(stage string) bool {
	for _, candidate := range stageOrder {
		if candidate == stage {
			return true
		}
	}
	return false
}

func nextStage(stage string) string {
	for index, candidate := range stageOrder {
		if candidate == stage && index+1 < len(stageOrder) {
			return stageOrder[index+1]
		}
	}
	return ""
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

func within(path, root string) bool {
	path, _ = filepath.Abs(path)
	root, _ = filepath.Abs(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
