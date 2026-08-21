package compile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cairn/internal/protocol"
	"cairn/internal/storage"
)

var stageOrder = []string{"extract", "classify", "write"}

const (
	maxStageBytes = 4 << 20
	planLifetime  = 24 * time.Hour
)

type startArguments struct {
	SourceID string `json:"source_id"`
}

type jobArguments struct {
	JobID string `json:"job_id"`
}

type submitArguments struct {
	JobID      string `json:"job_id"`
	Stage      string `json:"stage"`
	ResultFile string `json:"result_file"`
}

type previewArguments struct {
	JobID string `json:"job_id"`
}

type planArguments struct {
	PlanID    string `json:"plan_id"`
	Confirmed bool   `json:"confirmed,omitempty"`
}

type citation struct {
	SourceID string `json:"source_id"`
	Locator  string `json:"locator"`
}

type writeCandidate struct {
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

type stageReference struct {
	Stage       string   `json:"stage"`
	Status      string   `json:"status"`
	InputFiles  []string `json:"input_files"`
	SchemaFile  string   `json:"schema_file"`
	ResultFile  string   `json:"result_file"`
	ResultHash  string   `json:"result_hash,omitempty"`
	SubmittedAt string   `json:"submitted_at,omitempty"`
}

type jobResponse struct {
	JobID        string           `json:"job_id"`
	SourceID     string           `json:"source_id"`
	State        string           `json:"state"`
	CurrentStage string           `json:"current_stage,omitempty"`
	CreatedAt    string           `json:"created_at,omitempty"`
	UpdatedAt    string           `json:"updated_at,omitempty"`
	Stages       []stageReference `json:"stages,omitempty"`
}

type planResponse struct {
	PlanID            string         `json:"plan_id"`
	JobID             string         `json:"job_id"`
	State             string         `json:"state"`
	ArticleID         string         `json:"article_id"`
	Path              string         `json:"path"`
	BaseVersion       int            `json:"base_version"`
	ProposedVersion   int            `json:"proposed_version"`
	RiskFlags         []string       `json:"risk_flags"`
	Diff              map[string]any `json:"diff"`
	ExpiresAt         string         `json:"expires_at"`
	AffectedSourceIDs []string       `json:"affected_source_ids,omitempty"`
}

func Start(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[startArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if strings.TrimSpace(args.SourceID) == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "source_id is required", false, nil)
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

	var contentHash, rawPath string
	var sourceType, sensitivity string
	var sourceSize int64
	err = store.DB.QueryRowContext(ctx, `SELECT s.content_hash, s.source_type, s.sensitivity, s.byte_size, b.raw_path FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.source_id = ? AND s.forgotten_at IS NULL`, args.SourceID).Scan(&contentHash, &sourceType, &sensitivity, &sourceSize, &rawPath)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.Response{}, protocol.NewCodedError("SOURCE_NOT_FOUND", "source was not found", false, nil)
	}
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile source", true, nil)
	}
	_ = sourceType
	_ = sensitivity
	jobID := newID("job")
	jobRoot := filepath.Join(store.Paths.Jobs, jobID)
	if err := initializeJobFiles(store, jobRoot, rawPath, contentHash); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot initialize compile job files", true, nil)
	}
	stages := makeStageReferences(jobRoot, store.Paths.Root)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	response := protocol.NewSuccessResponse(req, jobResponse{JobID: jobID, SourceID: args.SourceID, State: "running", CurrentStage: "extract", CreatedAt: now, UpdatedAt: now, Stages: stages})
	responseBytes, err := json.Marshal(response)
	if err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot encode compile start response", false, nil)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin compile transaction", true, nil)
	}
	defer tx.Rollback()
	if stored, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve compile idempotency key", true, nil)
	} else if found {
		_ = os.RemoveAll(jobRoot)
		return replayOrConflict(stored, fingerprint)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO compile_jobs(job_id, source_id, state, current_stage, created_at, updated_at) VALUES (?, ?, 'running', 'extract', ?, ?)`, jobID, args.SourceID, now, now); err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create compile job", true, nil)
	}
	for _, stage := range stages {
		inputJSON, _ := json.Marshal(stage.InputFiles)
		if _, err := tx.ExecContext(ctx, `INSERT INTO compile_stages(job_id, stage, status, input_json, schema_path, result_path) VALUES (?, ?, ?, ?, ?, ?)`, jobID, stage.Stage, stage.Status, inputJSON, store.Relative(stage.SchemaFile), store.Relative(stage.ResultFile)); err != nil {
			_ = os.RemoveAll(jobRoot)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create compile stage", true, nil)
		}
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"job_id": jobID, "source_id": args.SourceID, "source_size": sourceSize}); err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write compile audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete compile idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit compile job", true, nil)
	}
	return response, nil
}

func Next(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[jobArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	job, err := loadJob(ctx, store, args.JobID)
	if err != nil {
		return nil, err
	}
	if job.CurrentStage == "" {
		return job, nil
	}
	for _, stage := range job.Stages {
		if stage.Stage == job.CurrentStage {
			return stage, nil
		}
	}
	return nil, protocol.NewCodedError("JOB_STATE_INVALID", "job current stage is not available", false, nil)
}

func Status(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[jobArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	return loadJob(ctx, store, args.JobID)
}

func loadJob(ctx context.Context, store *storage.Storage, jobID string) (jobResponse, *protocol.CodedError) {
	if strings.TrimSpace(jobID) == "" {
		return jobResponse{}, protocol.NewCodedError("REQUEST_INVALID", "job_id is required", false, nil)
	}
	var result jobResponse
	err := store.DB.QueryRowContext(ctx, `SELECT cj.job_id, cj.source_id, cj.state, COALESCE(cj.current_stage, ''), cj.created_at, cj.updated_at FROM compile_jobs cj JOIN sources s ON s.source_id = cj.source_id AND s.forgotten_at IS NULL WHERE cj.job_id = ? AND cj.state <> 'forgotten'`, jobID).Scan(&result.JobID, &result.SourceID, &result.State, &result.CurrentStage, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return jobResponse{}, protocol.NewCodedError("JOB_NOT_FOUND", "compile job was not found", false, nil)
	}
	if err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile job", true, nil)
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT stage, status, input_json, schema_path, result_path, COALESCE(result_hash, ''), COALESCE(submitted_at, '') FROM compile_stages WHERE job_id = ? ORDER BY CASE stage WHEN 'extract' THEN 1 WHEN 'classify' THEN 2 WHEN 'write' THEN 3 ELSE 4 END`, jobID)
	if err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile stages", true, nil)
	}
	defer rows.Close()
	for rows.Next() {
		var stage, status, inputJSON, schemaPath, resultPath, hash, submittedAt string
		if err := rows.Scan(&stage, &status, &inputJSON, &schemaPath, &resultPath, &hash, &submittedAt); err != nil {
			return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode compile stage", true, nil)
		}
		var inputFiles []string
		if err := json.Unmarshal([]byte(inputJSON), &inputFiles); err != nil {
			return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "compile stage input metadata is invalid", false, nil)
		}
		result.Stages = append(result.Stages, stageReference{Stage: stage, Status: status, InputFiles: absoluteRefs(store.Paths.Root, inputFiles), SchemaFile: absoluteRef(store.Paths.Root, schemaPath), ResultFile: absoluteRef(store.Paths.Root, resultPath), ResultHash: hash, SubmittedAt: submittedAt})
	}
	if err := rows.Err(); err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish compile stage read", true, nil)
	}
	return result, nil
}

func initializeJobFiles(store *storage.Storage, jobRoot, rawPath, contentHash string) error {
	for _, dir := range []string{"input", "schemas", "output"} {
		if err := os.MkdirAll(filepath.Join(jobRoot, dir), 0700); err != nil {
			return err
		}
	}
	sourcePath := filepath.Join(store.Paths.Root, filepath.FromSlash(rawPath))
	if err := copyManagedFile(sourcePath, filepath.Join(jobRoot, "input", "source.md")); err != nil {
		return err
	}
	for _, stage := range stageOrder {
		contents, err := SchemaBytes(stage)
		if err != nil {
			return err
		}
		if err := writePrivateFile(filepath.Join(jobRoot, "schemas", stage+".json"), contents); err != nil {
			return err
		}
	}
	marker := filepath.Join(jobRoot, "input", "source.sha256")
	return writePrivateFile(marker, []byte(contentHash+"\n"))
}

func makeStageReferences(jobRoot, dataRoot string) []stageReference {
	return []stageReference{
		{Stage: "extract", Status: "available", InputFiles: []string{filepath.Join(jobRoot, "input", "source.md")}, SchemaFile: filepath.Join(jobRoot, "schemas", "extract.json"), ResultFile: filepath.Join(jobRoot, "output", "extract.json")},
		{Stage: "classify", Status: "pending", InputFiles: []string{filepath.Join(jobRoot, "output", "extract.json")}, SchemaFile: filepath.Join(jobRoot, "schemas", "classify.json"), ResultFile: filepath.Join(jobRoot, "output", "classify.json")},
		{Stage: "write", Status: "pending", InputFiles: []string{filepath.Join(jobRoot, "output", "classify.json")}, SchemaFile: filepath.Join(jobRoot, "schemas", "write.json"), ResultFile: filepath.Join(jobRoot, "output", "write.json")},
	}
}

func absoluteRefs(root string, refs []string) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		result = append(result, absoluteRef(root, ref))
	}
	return result
}

func absoluteRef(root, ref string) string {
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref)
	}
	return filepath.Join(root, filepath.FromSlash(ref))
}

func copyManagedFile(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("managed source is unavailable")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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

func newID(prefix string) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%s_%s_%s", prefix, time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:]))
}

func hashBytes(contents []byte) string {
	hash := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(hash[:])
}

func stringVersion(value int) string { return strconv.Itoa(value) }

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
