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

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

var stageOrder = []string{"extract", "classify", "write"}

const (
	maxStageBytes         = 4 << 20
	maxCompileSourceBytes = 512 << 20
	planLifetime          = 24 * time.Hour
)

var errSensitiveCompileContext = errors.New("sensitive compile context")

type startArguments struct {
	SourceID  string              `json:"source_id,omitempty"`
	SourceIDs []string            `json:"source_ids,omitempty"`
	Pipeline  pipelineFingerprint `json:"pipeline,omitempty"`
}

type pipelineFingerprint struct {
	ExtractorVersion string `json:"extractor_version,omitempty"`
	PromptHash       string `json:"prompt_hash,omitempty"`
	SchemaVersion    string `json:"schema_version,omitempty"`
	Strategy         string `json:"strategy,omitempty"`
}

type compileSource struct {
	id, hash, rawPath, sensitivity string
	size                           int64
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
	JobID        string              `json:"job_id"`
	SourceID     string              `json:"source_id"`
	SourceIDs    []string            `json:"source_ids,omitempty"`
	Pipeline     pipelineFingerprint `json:"pipeline,omitempty"`
	State        string              `json:"state"`
	CurrentStage string              `json:"current_stage,omitempty"`
	CreatedAt    string              `json:"created_at,omitempty"`
	UpdatedAt    string              `json:"updated_at,omitempty"`
	Stages       []stageReference    `json:"stages,omitempty"`
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
	sourceIDs := normalizeSourceIDs(args.SourceID, args.SourceIDs)
	if len(sourceIDs) == 0 {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "source_id or source_ids is required", false, nil)
	}
	if len(sourceIDs) > 100 {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "source_ids cannot contain more than 100 sources", false, nil)
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
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

	sources := make([]compileSource, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		var item compileSource
		err = store.DB.QueryRowContext(ctx, `SELECT s.content_hash, s.byte_size, b.raw_path, s.sensitivity FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.source_id = ? AND s.forgotten_at IS NULL`, sourceID).Scan(&item.hash, &item.size, &item.rawPath, &item.sensitivity)
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.Response{}, protocol.NewCodedError("SOURCE_NOT_FOUND", "source was not found", false, map[string]any{"source_id": sourceID})
		}
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile source", true, nil)
		}
		if !allowSensitive && item.sensitivity != "normal" {
			return protocol.Response{}, protocol.NewCodedError("SENSITIVITY_DENIED", "compile source requires sensitive-content permission", false, map[string]any{"source_id": sourceID, "sensitivity": item.sensitivity})
		}
		item.id = sourceID
		sources = append(sources, item)
	}
	var totalBytes int64
	for _, source := range sources {
		totalBytes += source.size
		if totalBytes > maxCompileSourceBytes {
			return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "compile source set exceeds the aggregate byte limit", false, map[string]any{"max_bytes": maxCompileSourceBytes})
		}
	}
	jobID := newID("job")
	jobRoot := filepath.Join(store.Paths.Jobs, jobID)
	if err := initializeJobFiles(store, jobRoot, sources, allowSensitive); err != nil {
		if errors.Is(err, errSensitiveCompileContext) {
			return protocol.Response{}, protocol.NewCodedError("SENSITIVITY_DENIED", "compile context requires sensitive-content permission", false, nil)
		}
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot initialize compile job files", true, nil)
	}
	stages := makeStageReferences(jobRoot, store.Paths.Root, len(sources) > 1)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	response := protocol.NewSuccessResponse(req, jobResponse{JobID: jobID, SourceID: sourceIDs[0], SourceIDs: sourceIDs, Pipeline: args.Pipeline, State: "running", CurrentStage: "extract", CreatedAt: now, UpdatedAt: now, Stages: stages})
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
	pipelineJSON, _ := json.Marshal(args.Pipeline)
	if _, err := tx.ExecContext(ctx, `INSERT INTO compile_jobs(job_id, source_id, state, current_stage, pipeline_json, created_at, updated_at) VALUES (?, ?, 'running', 'extract', ?, ?, ?)`, jobID, sourceIDs[0], pipelineJSON, now, now); err != nil {
		_ = os.RemoveAll(jobRoot)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create compile job", true, nil)
	}
	for ordinal, sourceID := range sourceIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO compile_job_sources(job_id, source_id, ordinal) VALUES (?, ?, ?)`, jobID, sourceID, ordinal); err != nil {
			_ = os.RemoveAll(jobRoot)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot register compile source set", true, nil)
		}
	}
	for _, stage := range stages {
		inputJSON, _ := json.Marshal(stage.InputFiles)
		if _, err := tx.ExecContext(ctx, `INSERT INTO compile_stages(job_id, stage, status, input_json, schema_path, result_path) VALUES (?, ?, ?, ?, ?, ?)`, jobID, stage.Stage, stage.Status, inputJSON, store.Relative(stage.SchemaFile), store.Relative(stage.ResultFile)); err != nil {
			_ = os.RemoveAll(jobRoot)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create compile stage", true, nil)
		}
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"job_id": jobID, "source_ids": sourceIDs}); err != nil {
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
	var pipelineJSON []byte
	err := store.DB.QueryRowContext(ctx, `SELECT cj.job_id, cj.source_id, cj.state, COALESCE(cj.current_stage, ''), COALESCE(cj.pipeline_json, '{}'), cj.created_at, cj.updated_at FROM compile_jobs cj JOIN sources s ON s.source_id = cj.source_id AND s.forgotten_at IS NULL WHERE cj.job_id = ? AND cj.state <> 'forgotten'`, jobID).Scan(&result.JobID, &result.SourceID, &result.State, &result.CurrentStage, &pipelineJSON, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return jobResponse{}, protocol.NewCodedError("JOB_NOT_FOUND", "compile job was not found", false, nil)
	}
	if err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile job", true, nil)
	}
	if err := json.Unmarshal(pipelineJSON, &result.Pipeline); err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "compile pipeline metadata is invalid", false, nil)
	}
	rowsSources, err := store.DB.QueryContext(ctx, `SELECT source_id FROM compile_job_sources WHERE job_id = ? ORDER BY ordinal ASC`, jobID)
	if err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read compile source set", true, nil)
	}
	defer rowsSources.Close()
	for rowsSources.Next() {
		var sourceID string
		if err := rowsSources.Scan(&sourceID); err != nil {
			return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode compile source set", true, nil)
		}
		result.SourceIDs = append(result.SourceIDs, sourceID)
	}
	if err := rowsSources.Err(); err != nil {
		return jobResponse{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish compile source set read", true, nil)
	}
	if len(result.SourceIDs) == 0 {
		result.SourceIDs = []string{result.SourceID}
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

func initializeJobFiles(store *storage.Storage, jobRoot string, sources []compileSource, allowSensitive bool) error {
	for _, dir := range []string{"input", "schemas", "output"} {
		if err := os.MkdirAll(filepath.Join(jobRoot, dir), 0700); err != nil {
			return err
		}
	}
	for index, source := range sources {
		sourcePath := filepath.Join(store.Paths.Root, filepath.FromSlash(source.rawPath))
		name := "source.md"
		if len(sources) > 1 {
			name = fmt.Sprintf("%03d-%s.md", index+1, safeFilePart(source.id))
		}
		if err := copyManagedFile(sourcePath, filepath.Join(jobRoot, "input", name)); err != nil {
			return err
		}
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
	if err := writeCompileContext(store, jobRoot, sources, allowSensitive); err != nil {
		return err
	}
	hashes := make([]string, 0, len(sources))
	for _, source := range sources {
		hashes = append(hashes, source.hash)
	}
	return writePrivateFile(filepath.Join(jobRoot, "input", "source.sha256"), []byte(strings.Join(hashes, "\n")+"\n"))
}

func makeStageReferences(jobRoot, dataRoot string, multi bool) []stageReference {
	extractInputs := []string{filepath.Join(jobRoot, "input", "source.md")}
	if multi {
		extractInputs = nil
		matches, _ := filepath.Glob(filepath.Join(jobRoot, "input", "*.md"))
		sort.Strings(matches)
		extractInputs = matches
	}
	return []stageReference{
		{Stage: "extract", Status: "available", InputFiles: extractInputs, SchemaFile: filepath.Join(jobRoot, "schemas", "extract.json"), ResultFile: filepath.Join(jobRoot, "output", "extract.json")},
		{Stage: "classify", Status: "pending", InputFiles: []string{filepath.Join(jobRoot, "output", "extract.json"), filepath.Join(jobRoot, "input", "context.json")}, SchemaFile: filepath.Join(jobRoot, "schemas", "classify.json"), ResultFile: filepath.Join(jobRoot, "output", "classify.json")},
		{Stage: "write", Status: "pending", InputFiles: []string{filepath.Join(jobRoot, "output", "classify.json"), filepath.Join(jobRoot, "input", "context.json")}, SchemaFile: filepath.Join(jobRoot, "schemas", "write.json"), ResultFile: filepath.Join(jobRoot, "output", "write.json")},
	}
}

func writeCompileContext(store *storage.Storage, jobRoot string, sources []compileSource, allowSensitive bool) error {
	allowed := make(map[string]bool, len(sources))
	for _, source := range sources {
		allowed[source.id] = true
	}
	type articleContext struct {
		ArticleID, Slug, Title, Summary, Sensitivity string
		Version                                      int
		SourceIDs                                    []string `json:"source_ids"`
	}
	type factContext struct {
		FactID, FactKey, Kind, Text, Status, Freshness string
		Version                                        int
		SourceIDs                                      []string `json:"source_ids"`
	}
	articles := make([]articleContext, 0)
	rows, err := store.DB.Query(`SELECT article_id, slug, title, sensitivity, current_version FROM articles WHERE forgotten_at IS NULL ORDER BY slug, article_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a articleContext
		if err := rows.Scan(&a.ArticleID, &a.Slug, &a.Title, &a.Sensitivity, &a.Version); err != nil {
			return err
		}
		citationRows, err := store.DB.Query(`SELECT DISTINCT source_id FROM article_citations WHERE article_id = ? AND version = ?`, a.ArticleID, a.Version)
		if err != nil {
			return err
		}
		for citationRows.Next() {
			var sourceID string
			if err := citationRows.Scan(&sourceID); err != nil {
				citationRows.Close()
				return err
			}
			if allowed[sourceID] {
				a.SourceIDs = append(a.SourceIDs, sourceID)
			}
		}
		if err := citationRows.Err(); err != nil {
			citationRows.Close()
			return err
		}
		citationRows.Close()
		if len(a.SourceIDs) > 0 {
			if !allowSensitive && a.Sensitivity != "normal" {
				return fmt.Errorf("%w: article %s requires sensitive-content permission", errSensitiveCompileContext, a.ArticleID)
			}
			articles = append(articles, a)
		}
	}
	facts := make([]factContext, 0)
	factRows, err := store.DB.Query(`SELECT fact_id, fact_key, kind, current_version, status, freshness FROM facts WHERE status <> 'retracted' ORDER BY kind, fact_key`)
	if err != nil {
		return err
	}
	defer factRows.Close()
	for factRows.Next() {
		var f factContext
		if err := factRows.Scan(&f.FactID, &f.FactKey, &f.Kind, &f.Version, &f.Status, &f.Freshness); err != nil {
			return err
		}
		var text string
		if err := store.DB.QueryRow(`SELECT text FROM fact_versions WHERE fact_id = ? AND version = ?`, f.FactID, f.Version).Scan(&text); err != nil {
			return err
		}
		f.Text = text
		citationRows, err := store.DB.Query(`SELECT DISTINCT source_id FROM fact_citations WHERE fact_id = ? AND version = ?`, f.FactID, f.Version)
		if err != nil {
			return err
		}
		for citationRows.Next() {
			var sourceID string
			if err := citationRows.Scan(&sourceID); err != nil {
				citationRows.Close()
				return err
			}
			if allowed[sourceID] {
				f.SourceIDs = append(f.SourceIDs, sourceID)
			}
		}
		if err := citationRows.Err(); err != nil {
			citationRows.Close()
			return err
		}
		citationRows.Close()
		if len(f.SourceIDs) > 0 {
			facts = append(facts, f)
		}
	}
	value := map[string]any{"source_ids": make([]string, 0, len(sources)), "articles": articles, "facts": facts}
	for _, source := range sources {
		value["source_ids"] = append(value["source_ids"].([]string), source.id)
	}
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(contents) > maxStageBytes {
		return fmt.Errorf("compile context exceeds %d bytes", maxStageBytes)
	}
	return writePrivateFile(filepath.Join(jobRoot, "input", "context.json"), contents)
}

func normalizeSourceIDs(primary string, values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values)+1)
	for _, value := range append([]string{primary}, values...) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func safeFilePart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "source"
	}
	return b.String()
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
