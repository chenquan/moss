package source

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
	"strings"
	"time"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

const (
	maxSourceBytes = 128 << 20
	defaultLimit   = 100
	maxLimit       = 200
)

type ingestArguments struct {
	InputFile   string `json:"input_file"`
	SourceType  string `json:"source_type"`
	Sensitivity string `json:"sensitivity"`
}

type getArguments struct {
	SourceID string `json:"source_id"`
}

type listArguments struct {
	Limit       int    `json:"limit,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
	Sensitivity string `json:"sensitivity,omitempty"`
}

type sourceData struct {
	SourceID    string `json:"source_id"`
	ContentHash string `json:"content_hash"`
	SourceType  string `json:"source_type"`
	Sensitivity string `json:"sensitivity"`
	ByteSize    int64  `json:"byte_size"`
	OriginName  string `json:"origin_name,omitempty"`
	RawFile     string `json:"raw_file"`
	CreatedAt   string `json:"created_at"`
}

type listData struct {
	Sources []sourceData `json:"sources"`
	Count   int          `json:"count"`
}

type markSensitiveArguments struct {
	SourceID    string `json:"source_id"`
	Sensitivity string `json:"sensitivity"`
	Confirmed   bool   `json:"confirmed,omitempty"`
}

type markSensitiveData struct {
	SourceID             string   `json:"source_id"`
	PreviousSensitivity  string   `json:"previous_sensitivity"`
	Sensitivity          string   `json:"sensitivity"`
	Changed              bool     `json:"changed"`
	PropagatedArticleIDs []string `json:"propagated_article_ids,omitempty"`
	PropagatedActionIDs  []string `json:"propagated_action_ids,omitempty"`
}

func Ingest(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[ingestArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	if err := validateIngestArguments(args); err != nil {
		return protocol.Response{}, err
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if record, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read idempotency state", true, nil)
	} else if found {
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

	stage, hash, size, err := stageInput(store.Paths.Staging, args.InputFile)
	if err != nil {
		return protocol.Response{}, fromInputError(err)
	}
	contentHash := "sha256:" + hex.EncodeToString(hash[:])
	rawPath := store.RawPath(contentHash)
	var registeredRawPath string
	blobRegistered := false
	if err := store.DB.QueryRowContext(ctx, `SELECT raw_path FROM blobs WHERE content_hash = ?`, contentHash).Scan(&registeredRawPath); err == nil {
		blobRegistered = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		_ = os.Remove(stage)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect content-addressed Raw metadata", true, nil)
	}
	duplicate := blobRegistered
	if existing, err := os.Lstat(rawPath); err == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			_ = os.Remove(stage)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "content-addressed Raw path is not a regular file", false, nil)
		}
		if verified, err := verifyRawHash(rawPath, hash, size); err != nil || !verified {
			_ = os.Remove(stage)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "content-addressed Raw content failed integrity verification", false, nil)
		}
		_ = os.Remove(stage)
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(rawPath), 0700); err != nil {
			_ = os.Remove(stage)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create Raw directory", true, nil)
		}
		if err := os.Rename(stage, rawPath); err != nil {
			_ = os.Remove(stage)
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finalize Raw content", true, nil)
		}
	} else {
		_ = os.Remove(stage)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect Raw content", true, nil)
	}

	sourceID := newID("src")
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	response := protocol.NewSuccessResponse(req, struct {
		SourceID    string `json:"source_id"`
		ContentHash string `json:"content_hash"`
		Duplicate   bool   `json:"duplicate"`
		ByteSize    int64  `json:"byte_size"`
	}{sourceID, contentHash, duplicate, size})
	responseBytes, err := json.Marshal(response)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("INTERNAL_ERROR", "cannot encode ingestion response", false, nil)
	}

	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin ingestion transaction", true, nil)
	}
	defer tx.Rollback()
	if existing, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve idempotency key", true, nil)
	} else if found {
		if existing.Fingerprint != fingerprint {
			return protocol.Response{}, protocol.NewCodedError("IDEMPOTENCY_CONFLICT", "idempotency key was used with different arguments", false, nil)
		}
		if existing.Status == "processing" {
			return protocol.Response{}, protocol.NewCodedError("IDEMPOTENCY_IN_PROGRESS", "an identical request is already being processed", true, nil)
		}
		var stored protocol.Response
		if err := json.Unmarshal(existing.Response, &stored); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "stored idempotency response is invalid", false, nil)
		}
		return stored, nil
	}
	if !blobRegistered {
		if _, err := tx.ExecContext(ctx, `INSERT INTO blobs(content_hash, byte_size, raw_path, created_at) VALUES (?, ?, ?, ?)`, contentHash, size, store.Relative(rawPath), createdAt); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot register Raw Blob", true, nil)
		}
	} else if registeredRawPath != store.Relative(rawPath) {
		if _, err := tx.ExecContext(ctx, `UPDATE blobs SET raw_path = ? WHERE content_hash = ? AND raw_path = ?`, store.Relative(rawPath), contentHash, registeredRawPath); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore Raw Blob path", true, nil)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sources(source_id, content_hash, source_type, sensitivity, byte_size, origin_name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, sourceID, contentHash, args.SourceType, args.Sensitivity, size, filepath.Base(args.InputFile), createdAt); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot register source", true, nil)
	}
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"source_id": sourceID, "content_hash": contentHash, "duplicate": duplicate}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete idempotency record", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit ingestion transaction", true, nil)
	}
	return response, nil
}

// MarkSensitive changes source metadata while preserving the stricter
// classification of records already derived from that source.
func MarkSensitive(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[markSensitiveArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	args.SourceID = strings.TrimSpace(args.SourceID)
	args.Sensitivity = strings.TrimSpace(args.Sensitivity)
	if args.SourceID == "" {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "source_id is required", false, nil)
	}
	if !supportedSensitivity(args.Sensitivity) {
		return protocol.Response{}, protocol.NewCodedError("SENSITIVITY_INVALID", "sensitivity is not supported", false, map[string]any{"supported": []string{"normal", "sensitive", "restricted"}})
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if record, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read sensitivity idempotency state", true, nil)
	} else if found {
		return replayIdempotency(record, fingerprint)
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin sensitivity transaction", true, nil)
	}
	defer tx.Rollback()
	if record, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve sensitivity idempotency key", true, nil)
	} else if found {
		return replayIdempotency(record, fingerprint)
	}
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT sensitivity FROM sources WHERE source_id = ? AND forgotten_at IS NULL`, args.SourceID).Scan(&previous); errors.Is(err, sql.ErrNoRows) {
		return protocol.Response{}, protocol.NewCodedError("SOURCE_NOT_FOUND", "source was not found", false, nil)
	} else if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read source sensitivity", true, nil)
	}
	previousRank := sensitivityRank(previous)
	nextRank := sensitivityRank(args.Sensitivity)
	if nextRank < previousRank && !args.Confirmed {
		return protocol.Response{}, protocol.NewCodedError("CONFIRMATION_REQUIRED", "lowering source sensitivity requires explicit Skill confirmation", false, map[string]any{"previous": previous, "requested": args.Sensitivity})
	}
	data := markSensitiveData{SourceID: args.SourceID, PreviousSensitivity: previous, Sensitivity: args.Sensitivity, Changed: previous != args.Sensitivity, PropagatedArticleIDs: make([]string, 0), PropagatedActionIDs: make([]string, 0)}
	if data.Changed {
		result, err := tx.ExecContext(ctx, `UPDATE sources SET sensitivity = ? WHERE source_id = ? AND sensitivity = ? AND forgotten_at IS NULL`, args.Sensitivity, args.SourceID, previous)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot update source sensitivity", true, nil)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return protocol.Response{}, protocol.NewCodedError("SOURCE_CHANGED", "source sensitivity changed before update", false, nil)
		}
		if nextRank > previousRank {
			articleRows, err := tx.QueryContext(ctx, `SELECT DISTINCT a.article_id, a.sensitivity FROM articles a JOIN article_citations c ON c.article_id = a.article_id WHERE c.source_id = ? AND a.forgotten_at IS NULL ORDER BY a.article_id`, args.SourceID)
			if err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect linked article sensitivity", true, nil)
			}
			for articleRows.Next() {
				var articleID, sensitivity string
				if err := articleRows.Scan(&articleID, &sensitivity); err != nil {
					articleRows.Close()
					return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode linked article sensitivity", true, nil)
				}
				if sensitivityRank(sensitivity) < nextRank {
					if _, err := tx.ExecContext(ctx, `UPDATE articles SET sensitivity = ?, updated_at = ? WHERE article_id = ? AND forgotten_at IS NULL AND sensitivity = ?`, args.Sensitivity, time.Now().UTC().Format(time.RFC3339Nano), articleID, sensitivity); err != nil {
						articleRows.Close()
						return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot propagate article sensitivity", true, nil)
					}
					data.PropagatedArticleIDs = append(data.PropagatedArticleIDs, articleID)
				}
			}
			if err := articleRows.Err(); err != nil {
				articleRows.Close()
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish linked article sensitivity read", true, nil)
			}
			articleRows.Close()
			actionRows, err := tx.QueryContext(ctx, `SELECT action_id, sensitivity FROM actions WHERE source_id = ? AND forgotten_at IS NULL ORDER BY action_id`, args.SourceID)
			if err != nil {
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect linked action sensitivity", true, nil)
			}
			for actionRows.Next() {
				var actionID, sensitivity string
				if err := actionRows.Scan(&actionID, &sensitivity); err != nil {
					actionRows.Close()
					return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode linked action sensitivity", true, nil)
				}
				if sensitivityRank(sensitivity) < nextRank {
					if _, err := tx.ExecContext(ctx, `UPDATE actions SET sensitivity = ?, updated_at = ? WHERE action_id = ? AND forgotten_at IS NULL AND sensitivity = ?`, args.Sensitivity, time.Now().UTC().Format(time.RFC3339Nano), actionID, sensitivity); err != nil {
						actionRows.Close()
						return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot propagate action sensitivity", true, nil)
					}
					data.PropagatedActionIDs = append(data.PropagatedActionIDs, actionID)
				}
			}
			if err := actionRows.Err(); err != nil {
				actionRows.Close()
				return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish linked action sensitivity read", true, nil)
			}
			actionRows.Close()
		}
	}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	if err := storage.InsertAuditTx(ctx, tx, newID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"source_id": args.SourceID, "previous_sensitivity": previous, "sensitivity": args.Sensitivity, "changed": data.Changed, "propagated_articles": len(data.PropagatedArticleIDs), "propagated_actions": len(data.PropagatedActionIDs)}); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write sensitivity audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete sensitivity idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit source sensitivity", true, nil)
	}
	return response, nil
}

func Get(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[getArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	if strings.TrimSpace(args.SourceID) == "" {
		return nil, protocol.NewCodedError("REQUEST_INVALID", "source_id is required", false, nil)
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	var data sourceData
	var rawPath string
	err := store.DB.QueryRowContext(ctx, `SELECT s.source_id, s.content_hash, s.source_type, s.sensitivity, s.byte_size, s.origin_name, s.created_at, b.raw_path FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.source_id = ? AND s.forgotten_at IS NULL`, args.SourceID).Scan(&data.SourceID, &data.ContentHash, &data.SourceType, &data.Sensitivity, &data.ByteSize, &data.OriginName, &data.CreatedAt, &rawPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, protocol.NewCodedError("SOURCE_NOT_FOUND", "source was not found", false, nil)
	}
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read source metadata", true, nil)
	}
	if isSensitive(data.Sensitivity) && !allowSensitive {
		return nil, protocol.NewCodedError("SENSITIVITY_DENIED", "source metadata requires sensitive-content permission", false, nil)
	}
	managedRaw := filepath.Join(store.Paths.Root, filepath.FromSlash(rawPath))
	if info, err := os.Lstat(managedRaw); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "source Raw content is unavailable", true, nil)
	}
	data.RawFile = rawPath
	if !allowSensitive {
		data.OriginName = ""
	}
	return data, nil
}

func List(ctx context.Context, store *storage.Storage, req protocol.Request) (any, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[listArguments](req)
	if codedErr != nil {
		return nil, codedErr
	}
	if args.Limit == 0 {
		args.Limit = defaultLimit
	}
	if args.Limit < 1 || args.Limit > maxLimit {
		return nil, protocol.NewCodedError("REQUEST_INVALID", fmt.Sprintf("limit must be between 1 and %d", maxLimit), false, nil)
	}
	allowSensitive, codedErr := protocol.OptionBool(req, "allow_sensitive")
	if codedErr != nil {
		return nil, codedErr
	}
	query := `SELECT s.source_id, s.content_hash, s.source_type, s.sensitivity, s.byte_size, s.origin_name, s.created_at, b.raw_path FROM sources s JOIN blobs b ON b.content_hash = s.content_hash WHERE s.forgotten_at IS NULL`
	params := make([]any, 0, 4)
	if args.SourceType != "" {
		query += ` AND s.source_type = ?`
		params = append(params, args.SourceType)
	}
	if args.Sensitivity != "" {
		query += ` AND s.sensitivity = ?`
		params = append(params, args.Sensitivity)
	}
	if !allowSensitive {
		query += ` AND s.sensitivity = 'normal'`
	}
	query += ` ORDER BY s.created_at ASC, s.source_id ASC LIMIT ?`
	params = append(params, args.Limit)
	rows, err := store.DB.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot list sources", true, nil)
	}
	defer rows.Close()
	result := listData{Sources: make([]sourceData, 0)}
	for rows.Next() {
		var data sourceData
		if err := rows.Scan(&data.SourceID, &data.ContentHash, &data.SourceType, &data.Sensitivity, &data.ByteSize, &data.OriginName, &data.CreatedAt, &data.RawFile); err != nil {
			return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot decode source list", true, nil)
		}
		if !allowSensitive {
			data.OriginName = ""
		}
		result.Sources = append(result.Sources, data)
	}
	if err := rows.Err(); err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot finish source list", true, nil)
	}
	result.Count = len(result.Sources)
	return result, nil
}

func validateIngestArguments(args ingestArguments) *protocol.CodedError {
	if strings.TrimSpace(args.InputFile) == "" || strings.TrimSpace(args.SourceType) == "" || strings.TrimSpace(args.Sensitivity) == "" {
		return protocol.NewCodedError("REQUEST_INVALID", "input_file, source_type, and sensitivity are required", false, nil)
	}
	if !supportedSourceType(args.SourceType) {
		return protocol.NewCodedError("SOURCE_TYPE_UNSUPPORTED", "source_type is not supported", false, map[string]any{"supported": []string{"document", "markdown", "text"}})
	}
	if !supportedSensitivity(args.Sensitivity) {
		return protocol.NewCodedError("SENSITIVITY_INVALID", "sensitivity is not supported", false, map[string]any{"supported": []string{"normal", "sensitive", "restricted"}})
	}
	return nil
}

func supportedSourceType(value string) bool {
	return value == "document" || value == "markdown" || value == "text"
}

func supportedSensitivity(value string) bool {
	return value == "normal" || value == "sensitive" || value == "restricted"
}

func isSensitive(value string) bool { return value == "sensitive" || value == "restricted" }

func sensitivityRank(value string) int {
	switch value {
	case "restricted":
		return 2
	case "sensitive":
		return 1
	default:
		return 0
	}
}

func replayIdempotency(record storage.IdempotencyRecord, fingerprint string) (protocol.Response, *protocol.CodedError) {
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

func stageInput(stagingDir, inputPath string) (string, [32]byte, int64, error) {
	var zero [32]byte
	info, err := os.Lstat(inputPath)
	if err != nil {
		return "", zero, 0, fmt.Errorf("input file cannot be inspected: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", zero, 0, errors.New("input must be a regular non-symlink file")
	}
	if info.Size() > maxSourceBytes {
		return "", zero, 0, fmt.Errorf("input exceeds %d bytes", maxSourceBytes)
	}
	in, err := os.Open(inputPath)
	if err != nil {
		return "", zero, 0, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(stagingDir, "source-stage-*")
	if err != nil {
		return "", zero, 0, err
	}
	name := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(name) }
	if err := tmp.Chmod(0600); err != nil {
		cleanup()
		return "", zero, 0, err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(in, maxSourceBytes+1))
	if err != nil {
		cleanup()
		return "", zero, 0, err
	}
	if written > maxSourceBytes {
		cleanup()
		return "", zero, 0, fmt.Errorf("input exceeds %d bytes", maxSourceBytes)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", zero, 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", zero, 0, err
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return name, digest, written, nil
}

func fromInputError(err error) *protocol.CodedError {
	if strings.Contains(err.Error(), "exceeds") {
		return protocol.NewCodedError("SOURCE_TOO_LARGE", err.Error(), false, nil)
	}
	if strings.Contains(err.Error(), "regular non-symlink") {
		return protocol.NewCodedError("SOURCE_INPUT_INVALID", err.Error(), false, nil)
	}
	return protocol.NewCodedError("SOURCE_INPUT_UNREADABLE", "source input cannot be read", false, nil)
}

func verifyRawHash(path string, expected [32]byte, expectedSize int64) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != expectedSize {
		return false, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxSourceBytes+1)); err != nil {
		return false, err
	}
	var actual [32]byte
	copy(actual[:], hash.Sum(nil))
	return actual == expected, nil
}

func newID(prefix string) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%s_%s_%s", prefix, time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:]))
}
