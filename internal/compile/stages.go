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

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
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

type multiExtractionItem struct {
	SourceID  string          `json:"source_id"`
	Content   json.RawMessage `json:"content"`
	Citations []struct {
		Locator string `json:"locator"`
	} `json:"citations"`
}

type multiExtractionResult struct {
	Pipeline pipelineFingerprint   `json:"pipeline"`
	Items    []multiExtractionItem `json:"items"`
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
	validationErr := Validate(args.Stage, contents)
	if jobIsMulti(job) && args.Stage == "extract" && isMultiExtractionPayload(contents) {
		validationErr = ValidateMulti("extract", contents)
	} else if jobIsMulti(job) && args.Stage == "write" && isBatchWritePayload(contents) {
		validationErr = ValidateMulti("write", contents)
	}
	if err := validationErr; err != nil {
		return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "stage result does not satisfy its JSON Schema", false, err.Error())
	}
	if codedErr := validateReferences(ctx, store, args.Stage, contents, args.JobID, sourceID); codedErr != nil {
		return protocol.Response{}, codedErr
	}
	resultHash := hashBytes(contents)
	var extractionIDs []string
	var extractionExisting []bool
	var extractionContents [][]byte
	var invalidExtractionIDs []string
	var extractionFP pipelineFingerprint
	if args.Stage == "extract" {
		var pipelineJSON []byte
		if err := store.DB.QueryRowContext(ctx, `SELECT COALESCE(pipeline_json, '{}') FROM compile_jobs WHERE job_id = ?`, args.JobID).Scan(&pipelineJSON); err == nil {
			_ = json.Unmarshal(pipelineJSON, &extractionFP)
		}
		if isMultiExtractionPayload(contents) {
			var extracted multiExtractionResult
			if err := json.Unmarshal(contents, &extracted); err == nil && extracted.Pipeline.ExtractorVersion != "" {
				extractionFP = extracted.Pipeline
			}
		}
		if extractionFP.ExtractorVersion == "" {
			extractionFP.ExtractorVersion = "external"
		}
		if extractionFP.PromptHash == "" {
			extractionFP.PromptHash = "unspecified"
		}
		if extractionFP.SchemaVersion == "" {
			extractionFP.SchemaVersion = "1"
		}
		if extractionFP.Strategy == "" {
			extractionFP.Strategy = "multi-source"
		}
		itemContents := map[string][]byte{}
		if isMultiExtractionPayload(contents) {
			var extracted multiExtractionResult
			if err := json.Unmarshal(contents, &extracted); err != nil {
				return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "multi extraction payload cannot be decoded", false, nil)
			}
			for _, item := range extracted.Items {
				if _, exists := itemContents[item.SourceID]; exists {
					return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "multi extraction contains duplicate source items", false, nil)
				}
				itemContents[item.SourceID] = append([]byte(nil), item.Content...)
			}
		}
		rows, err := store.DB.QueryContext(ctx, `SELECT cjs.source_id, s.content_hash FROM compile_job_sources cjs JOIN sources s ON s.source_id = cjs.source_id WHERE cjs.job_id = ? ORDER BY cjs.ordinal`, args.JobID)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read extraction source set", true, nil)
		}
		defer rows.Close()
		for rows.Next() {
			var sourceID, sourceHash string
			if err := rows.Scan(&sourceID, &sourceHash); err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode extraction source", true, nil)
			}
			var extractionID, extractionPath, extractionHash string
			itemContent := contents
			if len(itemContents) > 0 {
				var ok bool
				itemContent, ok = itemContents[sourceID]
				if !ok {
					return protocol.Response{}, protocol.NewCodedError("STAGE_RESULT_INVALID", "multi extraction is missing a Job source item", false, map[string]any{"source_id": sourceID})
				}
			}
			itemHash := hashBytes(itemContent)
			lookupErr := store.DB.QueryRowContext(ctx, `SELECT extraction_id, path, content_hash FROM extractions WHERE source_id = ? AND source_hash = ? AND extractor_version = ? AND prompt_hash = ? AND schema_version = ? AND strategy = ? AND status = 'active' ORDER BY created_at DESC LIMIT 1`, sourceID, sourceHash, extractionFP.ExtractorVersion, extractionFP.PromptHash, extractionFP.SchemaVersion, extractionFP.Strategy).Scan(&extractionID, &extractionPath, &extractionHash)
			existing := lookupErr == nil
			if existing {
				managedPath := absoluteRef(store.Paths.Root, extractionPath)
				info, statErr := os.Lstat(managedPath)
				artifact, readErr := os.ReadFile(managedPath)
				if !within(managedPath, store.Paths.Extractions) || statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || readErr != nil || hashBytes(artifact) != extractionHash || extractionHash != itemHash {
					existing = false
					invalidExtractionIDs = append(invalidExtractionIDs, extractionID)
				}
			}
			if !existing {
				extractionID = newID("ext")
				extractionPath = store.Relative(filepath.Join(store.Paths.Extractions, extractionID+".json"))
				if err := writePrivateFile(filepath.Join(store.Paths.Root, filepath.FromSlash(extractionPath)), itemContent); err != nil {
					return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot persist extraction artifact", true, nil)
				}
			}
			extractionIDs = append(extractionIDs, extractionID)
			extractionExisting = append(extractionExisting, existing)
			extractionContents = append(extractionContents, itemContent)
		}
		if err := rows.Err(); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish extraction source read", true, nil)
		}
	}
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
	if args.Stage == "extract" {
		for _, extractionID := range invalidExtractionIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE extractions SET status = 'stale' WHERE extraction_id = ? AND status = 'active'`, extractionID); err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot stale invalid extraction", true, nil)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT cjs.source_id, s.content_hash FROM compile_job_sources cjs JOIN sources s ON s.source_id = cjs.source_id WHERE cjs.job_id = ? ORDER BY cjs.ordinal`, args.JobID)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read extraction source set", true, nil)
		}
		defer rows.Close()
		ordinal := 0
		for rows.Next() {
			var sourceID, sourceHash string
			if err := rows.Scan(&sourceID, &sourceHash); err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode extraction source", true, nil)
			}
			if ordinal >= len(extractionIDs) {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "extraction artifact count mismatch", false, nil)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE extractions SET status = 'stale' WHERE source_id = ? AND status = 'active' AND (source_hash <> ? OR extractor_version <> ? OR prompt_hash <> ? OR schema_version <> ? OR strategy <> ?)`, sourceID, sourceHash, extractionFP.ExtractorVersion, extractionFP.PromptHash, extractionFP.SchemaVersion, extractionFP.Strategy); err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot mark stale extractions", true, nil)
			}
			if extractionExisting[ordinal] {
				ordinal++
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO extractions(extraction_id, source_id, source_hash, content_hash, path, extractor_version, prompt_hash, schema_version, strategy, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`, extractionIDs[ordinal], sourceID, sourceHash, hashBytes(extractionContents[ordinal]), store.Relative(filepath.Join(store.Paths.Extractions, extractionIDs[ordinal]+".json")), extractionFP.ExtractorVersion, extractionFP.PromptHash, extractionFP.SchemaVersion, extractionFP.Strategy, now); err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot register extraction artifact", true, nil)
			}
			ordinal++
		}
		if err := rows.Err(); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish extraction source read", true, nil)
		}
	}
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

func jobIsMulti(job jobResponse) bool { return len(job.SourceIDs) > 1 }

func isBatchWritePayload(contents []byte) bool {
	var value struct {
		Articles json.RawMessage `json:"articles"`
	}
	return json.Unmarshal(contents, &value) == nil && len(value.Articles) > 0
}

func isMultiExtractionPayload(contents []byte) bool {
	var value struct {
		Pipeline json.RawMessage `json:"pipeline"`
	}
	return json.Unmarshal(contents, &value) == nil && len(value.Pipeline) > 0
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

func validateReferences(ctx context.Context, store *storage.Storage, stage string, contents []byte, jobID, sourceID string) *protocol.CodedError {
	allowed := map[string]bool{sourceID: true}
	rows, err := store.DB.QueryContext(ctx, `SELECT source_id FROM compile_job_sources WHERE job_id = ?`, jobID)
	if err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile source references", true, nil)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode compile source reference", true, nil)
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish compile source reference read", true, nil)
	}
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
		if isMultiExtractionPayload(contents) {
			var value multiExtractionResult
			if err := json.Unmarshal(contents, &value); err != nil {
				return protocol.NewCodedError("STAGE_RESULT_INVALID", "multi-source extract result cannot be decoded", false, nil)
			}
			for _, item := range value.Items {
				if codedErr := check([]string{item.SourceID}); codedErr != nil {
					return codedErr
				}
			}
			return nil
		}
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
		if isBatchWritePayload(contents) {
			var value multiWriteCandidate
			if err := json.Unmarshal(contents, &value); err != nil {
				return protocol.NewCodedError("STAGE_RESULT_INVALID", "batch write result cannot be decoded", false, nil)
			}
			for _, item := range value.Articles {
				if codedErr := check(item.SourceIDs); codedErr != nil {
					return codedErr
				}
				for _, citation := range item.Citations {
					if codedErr := check([]string{citation.SourceID}); codedErr != nil {
						return codedErr
					}
				}
			}
			for _, item := range value.Facts {
				if codedErr := check(item.SourceIDs); codedErr != nil {
					return codedErr
				}
			}
			return nil
		}
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
