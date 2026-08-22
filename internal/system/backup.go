package system

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"moss/internal/protocol"
	"moss/internal/storage"
)

const (
	backupFormat       = "moss-backup/v1"
	backupExtension    = ".moss-backup.zip"
	maxBackupEntries   = 100000
	maxBackupBytes     = 4 << 30
	maxBackupFileBytes = 2 << 30
)

type exportArguments struct {
	BackupPath string `json:"backup_path,omitempty"`
}

type restoreArguments struct {
	BackupPath string `json:"backup_path"`
	Confirmed  bool   `json:"confirmed,omitempty"`
}

type backupManifest struct {
	Format        string        `json:"format"`
	BackupID      string        `json:"backup_id"`
	CreatedAt     string        `json:"created_at"`
	CLIVersion    string        `json:"cli_version"`
	SchemaVersion int           `json:"schema_version"`
	Entries       []backupEntry `json:"entries"`
}

type backupEntry struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type backupInfo struct {
	BackupID      string `json:"backup_id"`
	BackupPath    string `json:"backup_path"`
	CreatedAt     string `json:"created_at"`
	SchemaVersion int    `json:"schema_version"`
	FileCount     int    `json:"file_count"`
	Bytes         int64  `json:"bytes"`
}

type extractedBackup struct {
	Manifest backupManifest
	Stage    string
}

// Export creates a private, content-addressed snapshot of Moss's durable state.
func Export(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[exportArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if record, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read export idempotency state", true, nil)
	} else if found {
		return replayIdempotency(record, fingerprint)
	}

	backupID := newBackupID("backup")
	archivePath, codedErr := resolveBackupPath(store.Paths.Backups, args.BackupPath, backupID)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	info, backupErr := createBackup(ctx, store, archivePath, backupID)
	if backupErr != nil {
		return protocol.Response{}, backupErr
	}
	response := protocol.NewSuccessResponse(req, info)
	responseBytes, _ := json.Marshal(response)
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(archivePath)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin export transaction", true, nil)
	}
	defer tx.Rollback()
	if record, found, err := storage.ReserveIdempotencyTx(ctx, tx, req.IdempotencyKey, fingerprint); err != nil {
		_ = os.Remove(archivePath)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve export idempotency key", true, nil)
	} else if found {
		_ = os.Remove(archivePath)
		return replayIdempotency(record, fingerprint)
	}
	if err := storage.InsertAuditTx(ctx, tx, newBackupID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"backup_id": info.BackupID, "file_count": info.FileCount, "bytes": info.Bytes}); err != nil {
		_ = os.Remove(archivePath)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write export audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		_ = os.Remove(archivePath)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete export idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit export", true, nil)
	}
	return response, nil
}

// Restore validates and installs a previously exported archive. The active
// database is closed during the swap; callers must not use the passed store
// after this function returns.
func Restore(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[restoreArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	fingerprint, err := req.Fingerprint()
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "cannot fingerprint request", false, nil)
	}
	if record, found, err := store.ReadIdempotency(ctx, req.IdempotencyKey); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot read restore idempotency state", true, nil)
	} else if found {
		return replayIdempotency(record, fingerprint)
	}
	if !args.Confirmed {
		return protocol.Response{}, protocol.NewCodedError("CONFIRMATION_REQUIRED", "system.restore requires explicit Skill confirmation", false, nil)
	}
	archivePath, codedErr := resolveExistingBackupPath(store.Paths.Backups, args.BackupPath)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	extracted, codedErr := extractAndValidateBackup(archivePath, store.Paths.Backups)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	defer os.RemoveAll(extracted.Stage)

	preBackupID := newBackupID("pre-restore")
	preBackupPath := filepath.Join(store.Paths.Backups, preBackupID+backupExtension)
	_, backupErr := createBackup(ctx, store, preBackupPath, preBackupID)
	if backupErr != nil {
		return protocol.Response{}, backupErr
	}
	restoreID := newBackupID("restore")
	markerPath := filepath.Join(store.Paths.Staging, restoreID+".restore.json")
	oldDir := filepath.Join(store.Paths.Backups, "."+restoreID+"-old")
	marker := map[string]any{"restore_id": restoreID, "archive_path": archivePath, "pre_restore_backup": preBackupPath, "stage": extracted.Stage, "old_state": oldDir}
	markerBytes, _ := json.Marshal(marker)
	if err := writePrivateFile(markerPath, markerBytes); err != nil {
		_ = os.Remove(preBackupPath)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create restore recovery marker", true, nil)
	}

	if err := store.DB.Close(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot close active database for restore", true, nil)
	}
	if err := swapRestoredState(store.Paths, extracted.Stage, oldDir); err != nil {
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, err
	}

	restored, err := storage.Open(ctx)
	if err != nil {
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "restored database could not be opened", true, nil)
	}
	if codedErr := validateRestoredCore(ctx, restored); codedErr != nil {
		_ = restored.Close()
		if rollbackErr := rollbackRestoredState(store.Paths, oldDir); rollbackErr != nil {
			return protocol.Response{}, rollbackErr
		}
		return protocol.Response{}, codedErr
	}
	info := backupInfo{BackupID: extracted.Manifest.BackupID, BackupPath: archivePath, CreatedAt: extracted.Manifest.CreatedAt, SchemaVersion: extracted.Manifest.SchemaVersion, FileCount: len(extracted.Manifest.Entries), Bytes: manifestBytes(extracted.Manifest)}
	data := map[string]any{"backup": info, "pre_restore_backup_path": preBackupPath, "restored_schema_version": extracted.Manifest.SchemaVersion}
	response := protocol.NewSuccessResponse(req, data)
	responseBytes, _ := json.Marshal(response)
	tx, err := restored.DB.BeginTx(ctx, nil)
	if err != nil {
		_ = restored.Close()
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot begin restore audit transaction", true, nil)
	}
	defer tx.Rollback()
	// A restored snapshot can contain an old row for this request key. The
	// restore itself is the new authoritative operation, so replace that row.
	if _, err := tx.ExecContext(ctx, `DELETE FROM idempotency WHERE idempotency_key = ?`, req.IdempotencyKey); err != nil {
		_ = restored.Close()
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reset restore idempotency key", true, nil)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency(idempotency_key, fingerprint, status, created_at, updated_at) VALUES (?, ?, 'processing', ?, ?)`, req.IdempotencyKey, fingerprint, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = restored.Close()
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot reserve restore idempotency key", true, nil)
	}
	if err := storage.InsertAuditTx(ctx, tx, newBackupID("aud"), req.Operation, req.RequestID, req.IdempotencyKey, "succeeded", map[string]any{"backup_id": extracted.Manifest.BackupID, "pre_restore_backup": preBackupPath}); err != nil {
		_ = restored.Close()
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot write restore audit event", true, nil)
	}
	if err := storage.CompleteIdempotencyTx(ctx, tx, req.IdempotencyKey, responseBytes); err != nil {
		_ = restored.Close()
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot complete restore idempotency", true, nil)
	}
	if err := tx.Commit(); err != nil {
		_ = restored.Close()
		_ = rollbackRestoredState(store.Paths, oldDir)
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot commit restore", true, nil)
	}
	if err := restored.Close(); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot close restored database", true, nil)
	}
	if err := os.Remove(markerPath); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot clear restore recovery marker", true, nil)
	}
	_ = os.RemoveAll(oldDir)
	return response, nil
}

func createBackup(ctx context.Context, store *storage.Storage, archivePath, backupID string) (backupInfo, *protocol.CodedError) {
	if archivePath == "" {
		var codedErr *protocol.CodedError
		archivePath, codedErr = resolveBackupPath(store.Paths.Backups, "", backupID)
		if codedErr != nil {
			return backupInfo{}, codedErr
		}
	}
	if _, err := os.Lstat(archivePath); err == nil {
		return backupInfo{}, protocol.NewCodedError("BACKUP_EXISTS", "backup path already exists", false, nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return backupInfo{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect backup path", true, nil)
	}
	workDir := filepath.Join(store.Paths.Backups, "."+backupID+"-work")
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return backupInfo{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create backup staging directory", true, nil)
	}
	defer os.RemoveAll(workDir)
	databaseSnapshot := filepath.Join(workDir, "database", "moss.db")
	if err := os.MkdirAll(filepath.Dir(databaseSnapshot), 0700); err != nil {
		return backupInfo{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create database backup directory", true, nil)
	}
	if _, err := store.DB.ExecContext(ctx, `VACUUM INTO ?`, databaseSnapshot); err != nil {
		return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot create SQLite backup snapshot", true, nil)
	}
	entries := make([]backupEntry, 0, 64)
	databaseEntry, err := fileEntry(databaseSnapshot, "database/moss.db")
	if err != nil {
		return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot hash SQLite backup snapshot", true, nil)
	}
	entries = append(entries, databaseEntry)
	for _, tree := range []struct {
		name string
		path string
	}{
		{name: "raw", path: store.Paths.Raw},
		{name: "wiki", path: store.Paths.Wiki},
		{name: "jobs", path: store.Paths.Jobs},
		{name: "trash", path: store.Paths.Trash},
	} {
		copied, err := copyBackupTree(tree.path, filepath.Join(workDir, tree.name), tree.name)
		if err != nil {
			return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot copy managed backup files", true, nil)
		}
		entries = append(entries, copied...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	manifest := backupManifest{Format: backupFormat, BackupID: backupID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), CLIVersion: CLIVersion, SchemaVersion: storage.SchemaVersion, Entries: entries}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot encode backup manifest", true, nil)
	}
	tmpArchive := archivePath + ".tmp-" + newBackupID("archive")
	if err := writeBackupArchive(tmpArchive, workDir, manifestJSON, entries); err != nil {
		_ = os.Remove(tmpArchive)
		return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot write backup archive", true, nil)
	}
	if err := os.Rename(tmpArchive, archivePath); err != nil {
		_ = os.Remove(tmpArchive)
		return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot publish backup archive", true, nil)
	}
	if err := os.Chmod(archivePath, 0600); err != nil {
		return backupInfo{}, protocol.NewCodedError("BACKUP_FAILED", "cannot restrict backup permissions", true, nil)
	}
	return backupInfo{BackupID: backupID, BackupPath: archivePath, CreatedAt: manifest.CreatedAt, SchemaVersion: manifest.SchemaVersion, FileCount: len(entries), Bytes: manifestBytes(manifest)}, nil
}

func copyBackupTree(sourceRoot, stageRoot, archiveRoot string) ([]backupEntry, error) {
	if err := os.MkdirAll(stageRoot, 0700); err != nil {
		return nil, err
	}
	entries := make([]backupEntry, 0)
	err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in backup: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > maxBackupFileBytes {
			return fmt.Errorf("unsupported or oversized backup file: %s", path)
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid managed relative path")
		}
		destination := filepath.Join(stageRoot, relative)
		if err := copyPrivateFile(path, destination); err != nil {
			return err
		}
		entryPath := filepath.ToSlash(filepath.Join(archiveRoot, relative))
		value, err := fileEntry(destination, entryPath)
		if err != nil {
			return err
		}
		entries = append(entries, value)
		if len(entries) > maxBackupEntries {
			return fmt.Errorf("backup has too many files")
		}
		return nil
	})
	return entries, err
}

func copyPrivateFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, maxBackupFileBytes+1)); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileEntry(path, archivePath string) (backupEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return backupEntry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return backupEntry{}, fmt.Errorf("backup entry is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return backupEntry{}, err
	}
	defer file.Close()
	hash := sha256.New()
	bytes, err := io.Copy(hash, io.LimitReader(file, maxBackupFileBytes+1))
	if err != nil {
		return backupEntry{}, err
	}
	if bytes > maxBackupFileBytes {
		return backupEntry{}, fmt.Errorf("backup file exceeds limit")
	}
	return backupEntry{Path: filepath.ToSlash(archivePath), Bytes: bytes, SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
}

func writeBackupArchive(path, workDir string, manifestBytes []byte, entries []backupEntry) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(file)
	writeEntry := func(name string, source string, contents []byte) error {
		header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
		header.SetMode(0600)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		if contents != nil {
			_, err = writer.Write(contents)
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(writer, input)
		return err
	}
	if err := writeEntry("manifest.json", "", manifestBytes); err != nil {
		_ = zipWriter.Close()
		_ = file.Close()
		return err
	}
	for _, entry := range entries {
		if err := writeEntry(entry.Path, filepath.Join(workDir, filepath.FromSlash(entry.Path)), nil); err != nil {
			_ = zipWriter.Close()
			_ = file.Close()
			return err
		}
	}
	if err := zipWriter.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func extractAndValidateBackup(archivePath, backupRoot string) (extractedBackup, *protocol.CodedError) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup archive cannot be opened", false, nil)
	}
	defer archive.Close()
	if len(archive.File) > maxBackupEntries+2 {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup archive contains too many entries", false, nil)
	}
	restoreID := newBackupID("restore-stage")
	stage := filepath.Join(backupRoot, "."+restoreID)
	if err := os.MkdirAll(stage, 0700); err != nil {
		return extractedBackup{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create restore staging directory", true, nil)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	var manifest backupManifest
	manifestSeen := false
	seen := make(map[string]struct{})
	var totalBytes int64
	for _, entry := range archive.File {
		name := filepath.ToSlash(entry.Name)
		if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) || hasParentPath(name) {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_PATH_INVALID", "backup contains an unsafe path", false, nil)
		}
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_PATH_INVALID", "backup contains a symlink", false, nil)
		}
		if _, exists := seen[name]; exists {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup contains duplicate entries", false, nil)
		}
		seen[name] = struct{}{}
		if name == "manifest.json" {
			contents, err := readZipEntry(entry, maxBackupFileBytes)
			if err != nil {
				return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup manifest cannot be read", false, nil)
			}
			decoder := json.NewDecoder(strings.NewReader(string(contents)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&manifest); err != nil {
				return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup manifest is invalid", false, nil)
			}
			manifestSeen = true
			continue
		}
		if !allowedBackupEntry(name) || entry.FileInfo().IsDir() {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_PATH_INVALID", "backup contains an unsupported entry", false, nil)
		}
		if entry.UncompressedSize64 > uint64(maxBackupFileBytes) || totalBytes > maxBackupBytes-int64(entry.UncompressedSize64) {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_TOO_LARGE", "backup exceeds the supported size", false, nil)
		}
		totalBytes += int64(entry.UncompressedSize64)
		destination := filepath.Join(stage, filepath.FromSlash(name))
		if !pathWithin(destination, stage) {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_PATH_INVALID", "backup entry escapes staging", false, nil)
		}
		if err := extractZipEntry(entry, destination); err != nil {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup entry cannot be extracted", false, nil)
		}
	}
	if !manifestSeen || manifest.Format != backupFormat || manifest.BackupID == "" || manifest.SchemaVersion < 1 || manifest.SchemaVersion > storage.SchemaVersion {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INCOMPATIBLE", "backup manifest is not compatible with this runtime", false, map[string]any{"supported_schema_version": storage.SchemaVersion})
	}
	if len(manifest.Entries) == 0 || len(manifest.Entries) > maxBackupEntries {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup manifest has no valid entries", false, nil)
	}
	manifestPaths := make(map[string]backupEntry, len(manifest.Entries))
	for _, item := range manifest.Entries {
		if !allowedBackupEntry(item.Path) || item.Bytes < 0 || item.Bytes > maxBackupFileBytes || !strings.HasPrefix(item.SHA256, "sha256:") {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup manifest entry is invalid", false, nil)
		}
		if _, exists := manifestPaths[item.Path]; exists {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup manifest contains duplicate paths", false, nil)
		}
		manifestPaths[item.Path] = item
		path := filepath.Join(stage, filepath.FromSlash(item.Path))
		actual, err := fileEntry(path, item.Path)
		if err != nil || actual.Bytes != item.Bytes || actual.SHA256 != item.SHA256 {
			return extractedBackup{}, protocol.NewCodedError("BACKUP_HASH_MISMATCH", "backup manifest hash does not match archive content", false, map[string]any{"path": item.Path})
		}
	}
	if len(seen)-1 != len(manifestPaths) {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "archive entries do not match the manifest", false, nil)
	}
	databasePath := filepath.Join(stage, "database", "moss.db")
	if _, ok := manifestPaths["database/moss.db"]; !ok {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup does not contain a database snapshot", false, nil)
	}
	if err := validateSnapshotDatabase(databasePath); err != nil {
		return extractedBackup{}, protocol.NewCodedError("BACKUP_INVALID", "backup database failed integrity validation", false, nil)
	}
	cleanup = false
	return extractedBackup{Manifest: manifest, Stage: stage}, nil
}

func readZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("entry too large")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, limit+1))
}

func extractZipEntry(entry *zip.File, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, io.LimitReader(reader, maxBackupFileBytes+1)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validateSnapshotDatabase(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("integrity check failed")
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&value); err != nil {
		return err
	}
	var version int
	if _, err := fmt.Sscanf(value, "%d", &version); err != nil || version < 1 || version > storage.SchemaVersion {
		return fmt.Errorf("unsupported schema version")
	}
	return nil
}

func validateRestoredCore(ctx context.Context, store *storage.Storage) *protocol.CodedError {
	for _, path := range []string{store.Paths.Root, store.Paths.Raw, store.Paths.Wiki, store.Paths.Jobs, store.Paths.Responses, store.Paths.Backups, store.Paths.Trash, store.Paths.Locks, store.Paths.Staging} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "restored storage directory is invalid", true, nil)
		}
	}
	var integrity string
	if err := store.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "restored database integrity check failed", true, nil)
	}
	var version string
	if err := store.DB.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil || version != fmt.Sprint(storage.SchemaVersion) {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "restored database migration is incomplete", true, nil)
	}
	return nil
}

func swapRestoredState(paths storage.Paths, stage, oldDir string) *protocol.CodedError {
	if err := os.MkdirAll(oldDir, 0700); err != nil {
		return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create restore rollback directory", true, nil)
	}
	names := []string{"moss.db", "raw", "wiki", "jobs", "trash"}
	completed := make([]string, 0, len(names))
	for _, name := range names {
		active := filepath.Join(paths.Root, name)
		staged := filepath.Join(stage, name)
		if name == "moss.db" {
			staged = filepath.Join(stage, "database", "moss.db")
		}
		old := filepath.Join(oldDir, name)
		if _, err := os.Lstat(active); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "active restore target is unavailable", true, nil)
		}
		if _, err := os.Lstat(staged); errors.Is(err, os.ErrNotExist) {
			if name == "moss.db" {
				return protocol.NewCodedError("BACKUP_INVALID", "restore database snapshot is missing", false, nil)
			}
			if err := os.MkdirAll(staged, 0700); err != nil {
				return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot create empty restored directory", true, nil)
			}
		} else if err != nil {
			return protocol.NewCodedError("BACKUP_INVALID", "restored state entry is unavailable", false, nil)
		}
		if err := os.Rename(active, old); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot stage current state for restore", true, nil)
		}
		if err := os.Rename(staged, active); err != nil {
			_ = os.Rename(old, active)
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot install restored state", true, nil)
		}
		completed = append(completed, name)
	}
	return nil
}

func rollbackRestoredState(paths storage.Paths, oldDir string) *protocol.CodedError {
	names := []string{"moss.db", "raw", "wiki", "jobs", "trash"}
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		active := filepath.Join(paths.Root, name)
		old := filepath.Join(oldDir, name)
		if _, err := os.Lstat(old); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.RemoveAll(active); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot remove failed restored state", true, nil)
		}
		if err := os.Rename(old, active); err != nil {
			return protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore previous state", true, nil)
		}
	}
	return nil
}

func resolveBackupPath(root, requested, backupID string) (string, *protocol.CodedError) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = backupID + backupExtension
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, _ = filepath.Abs(path)
	relative, _ := filepath.Rel(root, path)
	if !pathWithin(path, root) || relative == "." || strings.Contains(relative, string(filepath.Separator)) || filepath.Ext(path) != ".zip" {
		return "", protocol.NewCodedError("PATH_INVALID", "backup path must be a ZIP file inside the private backup directory", false, nil)
	}
	return path, nil
}

func resolveExistingBackupPath(root, requested string) (string, *protocol.CodedError) {
	path, codedErr := resolveBackupPath(root, requested, "restore")
	if codedErr != nil {
		return "", codedErr
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", protocol.NewCodedError("BACKUP_NOT_FOUND", "backup archive was not found", false, nil)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", protocol.NewCodedError("BACKUP_INVALID", "backup path is not a regular file", false, nil)
	}
	return path, nil
}

func allowedBackupEntry(path string) bool {
	path = filepath.ToSlash(path)
	if path == "database/moss.db" {
		return true
	}
	for _, prefix := range []string{"raw/", "wiki/", "jobs/", "trash/"} {
		if strings.HasPrefix(path, prefix) && len(path) > len(prefix) {
			return true
		}
	}
	return false
}

func hasParentPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." || part == "" || part == "." {
			return part == ".." || part == ""
		}
	}
	return false
}

func pathWithin(path, root string) bool {
	path, _ = filepath.Abs(path)
	root, _ = filepath.Abs(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func manifestBytes(manifest backupManifest) int64 {
	var total int64
	for _, entry := range manifest.Entries {
		total += entry.Bytes
	}
	return total
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

func newBackupID(prefix string) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:]))
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
