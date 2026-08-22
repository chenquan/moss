package storage

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

	_ "modernc.org/sqlite"
)

const SchemaVersion = 4

const (
	dataDirEnv = "MOSS_DATA_DIR"
)

type Paths struct {
	Root      string
	Database  string
	Raw       string
	Wiki      string
	Jobs      string
	Responses string
	Backups   string
	Trash     string
	Locks     string
	Staging   string
}

func ResolvePaths() (Paths, error) {
	root := strings.TrimSpace(os.Getenv(dataDirEnv))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve user home: %w", err)
		}
		root = filepath.Join(home, ".moss")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve data root: %w", err)
	}
	return Paths{
		Root:      root,
		Database:  filepath.Join(root, "moss.db"),
		Raw:       filepath.Join(root, "raw"),
		Wiki:      filepath.Join(root, "wiki"),
		Jobs:      filepath.Join(root, "jobs"),
		Responses: filepath.Join(root, "responses"),
		Backups:   filepath.Join(root, "backups"),
		Trash:     filepath.Join(root, "trash"),
		Locks:     filepath.Join(root, "locks"),
		Staging:   filepath.Join(root, "staging"),
	}, nil
}

type Storage struct {
	Paths Paths
	DB    *sql.DB
}

func Open(ctx context.Context) (*Storage, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, err
	}
	if err := ensureLayout(paths); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	store := &Storage{Paths: paths, DB: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(paths.Database, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("restrict database permissions: %w", err)
	}
	return store, nil
}

func (s *Storage) Close() error { return s.DB.Close() }

func ensureLayout(paths Paths) error {
	dirs := []string{paths.Root, paths.Raw, paths.Wiki, paths.Jobs, paths.Responses, paths.Backups, paths.Trash, paths.Locks, paths.Staging}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create data directory %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("restrict data directory %q: %w", dir, err)
		}
	}
	if info, err := os.Stat(paths.Database); err == nil && info.Mode().IsRegular() {
		if err := os.Chmod(paths.Database, 0600); err != nil {
			return fmt.Errorf("restrict database file %q: %w", paths.Database, err)
		}
	}
	return nil
}

func (s *Storage) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS blobs (
			content_hash TEXT PRIMARY KEY,
			byte_size INTEGER NOT NULL,
			raw_path TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sources (
			source_id TEXT PRIMARY KEY,
			content_hash TEXT NOT NULL REFERENCES blobs(content_hash),
			source_type TEXT NOT NULL,
			sensitivity TEXT NOT NULL,
			byte_size INTEGER NOT NULL,
			origin_name TEXT,
			forgotten_at TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_created ON sources(created_at, source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_hash ON sources(content_hash)`,
		`CREATE TABLE IF NOT EXISTS idempotency (
			idempotency_key TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('processing', 'done')),
			response_json BLOB,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			audit_id TEXT PRIMARY KEY,
			operation TEXT NOT NULL,
			request_id TEXT NOT NULL,
			idempotency_key TEXT,
			status TEXT NOT NULL,
			summary_json BLOB,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS compile_jobs (
			job_id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL REFERENCES sources(source_id),
			state TEXT NOT NULL,
			current_stage TEXT,
			applied_plan_id TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS compile_stages (
			job_id TEXT NOT NULL REFERENCES compile_jobs(job_id) ON DELETE CASCADE,
			stage TEXT NOT NULL,
			status TEXT NOT NULL,
			input_json BLOB NOT NULL,
			schema_path TEXT NOT NULL,
			result_path TEXT NOT NULL,
			result_hash TEXT,
			rejected_reason TEXT,
			submitted_at TEXT,
			PRIMARY KEY(job_id, stage)
		)`,
		`CREATE TABLE IF NOT EXISTS articles (
			article_id TEXT PRIMARY KEY,
			slug TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			path TEXT NOT NULL,
			sensitivity TEXT NOT NULL,
			current_version INTEGER NOT NULL,
			current_hash TEXT NOT NULL,
			forgotten_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS article_versions (
			article_id TEXT NOT NULL REFERENCES articles(article_id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			path TEXT NOT NULL,
			content TEXT NOT NULL,
			job_id TEXT REFERENCES compile_jobs(job_id),
			created_at TEXT NOT NULL,
			PRIMARY KEY(article_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS article_citations (
			article_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			source_id TEXT NOT NULL REFERENCES sources(source_id),
			locator TEXT NOT NULL,
			PRIMARY KEY(article_id, version, source_id, locator),
			FOREIGN KEY(article_id, version) REFERENCES article_versions(article_id, version) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS plans (
			plan_id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL REFERENCES compile_jobs(job_id),
			kind TEXT NOT NULL,
			state TEXT NOT NULL,
			article_id TEXT NOT NULL,
			base_version INTEGER NOT NULL,
			base_hash TEXT,
			proposed_version INTEGER NOT NULL,
			proposed_hash TEXT NOT NULL,
			proposed_content TEXT NOT NULL,
			previous_content TEXT,
			diff_json BLOB NOT NULL,
			risk_json BLOB NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			applied_at TEXT,
			undone_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS plan_items (
			plan_id TEXT NOT NULL REFERENCES plans(plan_id) ON DELETE CASCADE,
			item_type TEXT NOT NULL,
			item_id TEXT NOT NULL,
			detail_json BLOB,
			PRIMARY KEY(plan_id, item_type, item_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_compile_jobs_updated ON compile_jobs(updated_at, job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_compile_stages_status ON compile_stages(status, job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_article_versions_hash ON article_versions(content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_state ON plans(state, created_at)`,
		`CREATE TABLE IF NOT EXISTS actions (
			action_id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('task', 'commitment', 'reminder')),
			title TEXT NOT NULL,
			details TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('open', 'in_progress', 'done', 'deferred', 'cancelled')),
			due_at TEXT,
			waiting_for TEXT,
			sensitivity TEXT NOT NULL CHECK (sensitivity IN ('normal', 'sensitive', 'restricted')),
			source_id TEXT REFERENCES sources(source_id),
			revision INTEGER NOT NULL,
			forgotten_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			completed_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS action_plans (
			plan_id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('create', 'update')),
			state TEXT NOT NULL CHECK (state IN ('pending', 'applied', 'expired')),
			action_id TEXT NOT NULL,
			base_revision INTEGER NOT NULL,
			proposed_json BLOB NOT NULL,
			previous_json BLOB,
			diff_json BLOB NOT NULL,
			risk_json BLOB NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			applied_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_actions_status_due ON actions(status, due_at, action_id)`,
		`CREATE INDEX IF NOT EXISTS idx_actions_source ON actions(source_id, action_id)`,
		`CREATE INDEX IF NOT EXISTS idx_action_plans_state ON action_plans(state, created_at)`,
		`CREATE TABLE IF NOT EXISTS safety_plans (
			plan_id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('forget', 'rollback')),
			state TEXT NOT NULL CHECK (state IN ('pending', 'applied', 'undone', 'expired')),
			target_json BLOB NOT NULL,
			previous_json BLOB,
			impact_json BLOB NOT NULL,
			diff_json BLOB NOT NULL,
			risk_json BLOB NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			applied_at TEXT,
			undone_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_safety_plans_state ON safety_plans(state, created_at)`,
	}
	for _, statement := range statements {
		if _, err := s.DB.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}
	for _, column := range []struct {
		table string
		name  string
		def   string
	}{
		{table: "sources", name: "forgotten_at", def: "TEXT"},
		{table: "articles", name: "forgotten_at", def: "TEXT"},
		{table: "actions", name: "forgotten_at", def: "TEXT"},
	} {
		if err := ensureColumn(ctx, s.DB, column.table, column.name, column.def); err != nil {
			return err
		}
	}
	var value string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO schema_meta(key, value) VALUES ('schema_version', ?)`, fmt.Sprint(SchemaVersion)); err != nil {
			return fmt.Errorf("write schema version: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if (value == "1" || value == "2" || value == "3") && SchemaVersion == 4 {
		if _, err := s.DB.ExecContext(ctx, `UPDATE schema_meta SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(SchemaVersion)); err != nil {
			return fmt.Errorf("upgrade schema version: %w", err)
		}
		return nil
	}
	if value != fmt.Sprint(SchemaVersion) {
		return fmt.Errorf("unsupported schema version %q", value)
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("decode %s columns: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("finish %s column inspection: %w", table, err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

type Health struct {
	Overall       string            `json:"overall"`
	Components    map[string]string `json:"components"`
	SchemaVersion int               `json:"schema_version"`
	Recovery      []string          `json:"recovery,omitempty"`
}

func (s *Storage) Health(ctx context.Context) (Health, error) {
	result := Health{Overall: "healthy", Components: map[string]string{}, SchemaVersion: SchemaVersion}
	for name, path := range map[string]string{
		"root": s.Paths.Root, "raw": s.Paths.Raw, "wiki": s.Paths.Wiki, "jobs": s.Paths.Jobs,
		"responses": s.Paths.Responses, "backups": s.Paths.Backups, "trash": s.Paths.Trash,
		"locks": s.Paths.Locks, "staging": s.Paths.Staging,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			result.Components[name] = "unhealthy"
			result.Overall = "unhealthy"
			continue
		}
		result.Components[name] = "healthy"
	}
	var integrity string
	if err := s.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		result.Components["sqlite"] = "unhealthy"
		result.Overall = "unhealthy"
	} else {
		result.Components["sqlite"] = "healthy"
	}
	var version string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil || version != fmt.Sprint(SchemaVersion) {
		result.Components["schema"] = "unhealthy"
		result.Overall = "unhealthy"
	} else {
		result.Components["schema"] = "healthy"
	}
	if info, err := os.Lstat(s.Paths.Database); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		result.Components["database-permissions"] = "unhealthy"
		result.Overall = "unhealthy"
	} else {
		result.Components["database-permissions"] = "healthy"
	}
	entries, err := os.ReadDir(s.Paths.Staging)
	if err != nil {
		return result, fmt.Errorf("inspect staging: %w", err)
	}
	for _, entry := range entries {
		result.Recovery = append(result.Recovery, entry.Name())
	}
	if len(result.Recovery) > 0 {
		result.Components["recovery"] = "needs-attention"
		result.Overall = "recovery-required"
	} else {
		result.Components["recovery"] = "healthy"
	}
	return result, nil
}

type IdempotencyRecord struct {
	Key         string
	Fingerprint string
	Status      string
	Response    []byte
}

func (s *Storage) ReadIdempotency(ctx context.Context, key string) (IdempotencyRecord, bool, error) {
	var record IdempotencyRecord
	err := s.DB.QueryRowContext(ctx, `SELECT idempotency_key, fingerprint, status, response_json FROM idempotency WHERE idempotency_key = ?`, key).Scan(&record.Key, &record.Fingerprint, &record.Status, &record.Response)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	return record, true, nil
}

func ReserveIdempotencyTx(ctx context.Context, tx *sql.Tx, key, fingerprint string) (IdempotencyRecord, bool, error) {
	var record IdempotencyRecord
	err := tx.QueryRowContext(ctx, `SELECT idempotency_key, fingerprint, status, response_json FROM idempotency WHERE idempotency_key = ?`, key).Scan(&record.Key, &record.Fingerprint, &record.Status, &record.Response)
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency(idempotency_key, fingerprint, status, created_at, updated_at) VALUES (?, ?, 'processing', ?, ?)`, key, fingerprint, now, now); err != nil {
			return IdempotencyRecord{}, false, err
		}
		return IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	return record, true, nil
}

func CompleteIdempotencyTx(ctx context.Context, tx *sql.Tx, key string, response []byte) error {
	_, err := tx.ExecContext(ctx, `UPDATE idempotency SET status = 'done', response_json = ?, updated_at = ? WHERE idempotency_key = ?`, response, time.Now().UTC().Format(time.RFC3339Nano), key)
	return err
}

func InsertAuditTx(ctx context.Context, tx *sql.Tx, auditID, operation, requestID, idempotencyKey, status string, summary any) error {
	var summaryJSON []byte
	var err error
	if summary != nil {
		summaryJSON, err = json.Marshal(summary)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(audit_id, operation, request_id, idempotency_key, status, summary_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, auditID, operation, requestID, idempotencyKey, status, summaryJSON, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Storage) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := s.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite integrity check: %s", result)
	}
	return nil
}

func (s *Storage) RawPath(contentHash string) string {
	prefix := contentHash
	if strings.HasPrefix(prefix, "sha256:") {
		prefix = strings.TrimPrefix(prefix, "sha256:")
	}
	if len(prefix) < 2 {
		return filepath.Join(s.Paths.Raw, "sha256", "invalid", prefix)
	}
	return filepath.Join(s.Paths.Raw, "sha256", prefix[:2], prefix)
}

func (s *Storage) Relative(path string) string {
	rel, err := filepath.Rel(s.Paths.Root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
