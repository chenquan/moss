package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

type recoverArguments struct {
	RecoveryID string `json:"recovery_id"`
}

type recoveryMarker struct {
	Kind              string            `json:"kind"`
	PlanID            string            `json:"plan_id"`
	SourceID          string            `json:"source_id"`
	BeforeSensitivity string            `json:"before_sensitivity"`
	AfterSensitivity  string            `json:"after_sensitivity"`
	Articles          []recoveryArticle `json:"articles"`
	TargetJSON        string            `json:"target_json"`
	Undo              bool              `json:"undo"`
	ArticleID         string            `json:"article_id"`
	Target            string            `json:"target"`
	Staged            string            `json:"staged"`
	BeforeHash        string            `json:"before_hash"`
	AfterHash         string            `json:"after_hash"`
	BeforeContent     string            `json:"before_content"`
	Phase             string            `json:"phase"`
}

type recoveryArticle struct {
	Path          string `json:"path"`
	BeforeHash    string `json:"before_hash"`
	AfterHash     string `json:"after_hash"`
	BeforeContent string `json:"before_content"`
}

// Recover resolves a supported interrupted article replacement. It never
// deletes an unknown marker or overwrites a file whose hash is not one of the
// journaled before/after states.
func Recover(ctx context.Context, store *storage.Storage, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	args, codedErr := protocol.DecodeArguments[recoverArguments](req)
	if codedErr != nil {
		return protocol.Response{}, codedErr
	}
	name := filepath.Base(strings.TrimSpace(args.RecoveryID))
	if name == "." || name == "" || name != strings.TrimSpace(args.RecoveryID) || !strings.HasSuffix(name, ".json") {
		return protocol.Response{}, protocol.NewCodedError("REQUEST_INVALID", "recovery_id must be a staging marker filename", false, nil)
	}
	markerPath := filepath.Join(store.Paths.Staging, name)
	contents, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_NOT_FOUND", "recovery marker was not found", false, nil)
	}
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "recovery marker cannot be read", true, nil)
	}
	var marker recoveryMarker
	if err := json.Unmarshal(contents, &marker); err != nil {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_UNSUPPORTED", "recovery marker does not contain a supported journal", false, map[string]any{"recovery_id": name})
	}
	if marker.Kind == "sensitivity" {
		return recoverSensitivity(ctx, store, req, name, marker, markerPath)
	}
	if marker.Kind == "forget" {
		return recoverForget(ctx, store, req, name, marker, markerPath)
	}
	if marker.Target == "" || marker.ArticleID == "" || marker.BeforeHash == "" || marker.AfterHash == "" {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_UNSUPPORTED", "recovery marker does not contain a supported article journal", false, map[string]any{"recovery_id": name})
	}
	target := marker.Target
	if !filepath.IsAbs(target) {
		target = filepath.Join(store.Paths.Root, filepath.FromSlash(target))
	}
	target, err = filepath.Abs(target)
	if err != nil || !withinRecoveryPath(target, filepath.Join(store.Paths.Wiki, "articles")) {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "recovery target is outside the managed Wiki", false, nil)
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "recovery target cannot be read", false, nil)
	}
	currentHash := recoveryHash(current)
	var dbHash string
	err = store.DB.QueryRowContext(ctx, `SELECT current_hash FROM articles WHERE article_id = ? AND forgotten_at IS NULL`, marker.ArticleID).Scan(&dbHash)
	if err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect recovery article state", true, nil)
	}
	if dbHash == marker.AfterHash && currentHash == marker.AfterHash {
		_ = os.Remove(markerPath)
		if marker.Staged != "" {
			_ = os.Remove(recoveryManagedPath(store.Paths.Root, marker.Staged))
		}
		return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "committed"}), nil
	}
	if dbHash == marker.BeforeHash && currentHash == marker.BeforeHash {
		_ = os.Remove(markerPath)
		if marker.Staged != "" {
			_ = os.Remove(recoveryManagedPath(store.Paths.Root, marker.Staged))
		}
		return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "cleared"}), nil
	}
	if dbHash != marker.BeforeHash || currentHash != marker.AfterHash {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "recovery state does not match the journal", false, map[string]any{"recovery_id": name, "database_hash": dbHash, "file_hash": currentHash})
	}
	if recoveryHash([]byte(marker.BeforeContent)) != marker.BeforeHash {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "journaled previous article content is invalid", false, nil)
	}
	if err := writeRecoveryFile(target, []byte(marker.BeforeContent)); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore interrupted article", true, nil)
	}
	_ = os.Remove(markerPath)
	if marker.Staged != "" {
		_ = os.Remove(recoveryManagedPath(store.Paths.Root, marker.Staged))
	}
	return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "rolled_back"}), nil
}

func recoverSensitivity(ctx context.Context, store *storage.Storage, req protocol.Request, name string, marker recoveryMarker, markerPath string) (protocol.Response, *protocol.CodedError) {
	if marker.SourceID == "" || marker.BeforeSensitivity == "" || marker.AfterSensitivity == "" || len(marker.Articles) == 0 {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_UNSUPPORTED", "sensitivity recovery marker is incomplete", false, nil)
	}
	var sensitivity string
	if err := store.DB.QueryRowContext(ctx, `SELECT sensitivity FROM sources WHERE source_id = ? AND forgotten_at IS NULL`, marker.SourceID).Scan(&sensitivity); err != nil {
		return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect sensitivity recovery source", true, nil)
	}
	states := make([]string, len(marker.Articles))
	for i, article := range marker.Articles {
		path := recoveryManagedPath(store.Paths.Root, article.Path)
		contents, err := os.ReadFile(path)
		if err != nil || !withinRecoveryPath(path, filepath.Join(store.Paths.Wiki, "articles")) {
			return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "sensitivity recovery article is unavailable", false, nil)
		}
		hash := recoveryHash(contents)
		switch hash {
		case article.AfterHash:
			states[i] = "after"
		case article.BeforeHash:
			states[i] = "before"
		default:
			return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "sensitivity recovery article has unrelated content", false, map[string]any{"path": article.Path})
		}
	}
	allAfter, allBefore := true, true
	for _, state := range states {
		allAfter = allAfter && state == "after"
		allBefore = allBefore && state == "before"
	}
	if sensitivity == marker.AfterSensitivity && allAfter {
		_ = os.Remove(markerPath)
		return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "committed"}), nil
	}
	if sensitivity == marker.BeforeSensitivity && allBefore {
		_ = os.Remove(markerPath)
		return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "cleared"}), nil
	}
	if sensitivity != marker.BeforeSensitivity || !allAfter {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "sensitivity recovery state does not match the journal", false, nil)
	}
	for _, article := range marker.Articles {
		if recoveryHash([]byte(article.BeforeContent)) != article.BeforeHash {
			return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "sensitivity recovery content is invalid", false, nil)
		}
		if err := writeRecoveryFile(recoveryManagedPath(store.Paths.Root, article.Path), []byte(article.BeforeContent)); err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot restore sensitivity article", true, nil)
		}
	}
	_ = os.Remove(markerPath)
	return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "rolled_back"}), nil
}

type recoverForgetTarget struct {
	Sources []struct {
		SourceID string `json:"source_id"`
	} `json:"sources"`
	Blobs    []recoverForgetBlob    `json:"blobs"`
	Articles []recoverForgetArticle `json:"articles"`
}

type recoverForgetBlob struct {
	RawPath   string `json:"raw_path"`
	TrashPath string `json:"trash_path"`
	Move      bool   `json:"move"`
}

type recoverForgetArticle struct {
	Path      string `json:"path"`
	TrashPath string `json:"trash_path"`
}

func recoverForget(ctx context.Context, store *storage.Storage, req protocol.Request, name string, marker recoveryMarker, markerPath string) (protocol.Response, *protocol.CodedError) {
	var target recoverForgetTarget
	if json.Unmarshal([]byte(marker.TargetJSON), &target) != nil || len(target.Sources) == 0 {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_UNSUPPORTED", "forget recovery marker is incomplete", false, nil)
	}
	forgotten := false
	active := false
	for _, source := range target.Sources {
		var value string
		err := store.DB.QueryRowContext(ctx, `SELECT COALESCE(forgotten_at, '') FROM sources WHERE source_id = ?`, source.SourceID).Scan(&value)
		if err != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "cannot inspect forget recovery source", true, nil)
		}
		if value == "" {
			active = true
		} else {
			forgotten = true
		}
	}
	if active && forgotten {
		return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "forget recovery has mixed database state", false, nil)
	}
	moveBack := !marker.Undo && active
	moveForward := marker.Undo && forgotten
	if !moveBack && !moveForward {
		_ = os.Remove(markerPath)
		return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": "committed"}), nil
	}
	for _, blob := range target.Blobs {
		if !blob.Move {
			continue
		}
		from, to := blob.RawPath, blob.TrashPath
		if moveBack {
			from, to = blob.TrashPath, blob.RawPath
		} else if !moveForward {
			continue
		}
		if err := reconcileMove(store.Paths.Root, from, to); err != nil {
			return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "forget recovery blob state conflicts", false, nil)
		}
	}
	for _, article := range target.Articles {
		from, to := article.Path, article.TrashPath
		if moveBack {
			from, to = article.TrashPath, article.Path
		} else if !moveForward {
			continue
		}
		if err := reconcileMove(store.Paths.Root, from, to); err != nil {
			return protocol.Response{}, protocol.NewCodedError("RECOVERY_CONFLICT", "forget recovery article state conflicts", false, nil)
		}
	}
	_ = os.Remove(markerPath)
	state := "committed"
	if moveBack || (!marker.Undo && active) {
		state = "rolled_back"
	}
	return protocol.NewSuccessResponse(req, map[string]any{"recovery_id": name, "state": state}), nil
}

func reconcileMove(root, from, to string) error {
	fromPath := recoveryManagedPath(root, from)
	toPath := recoveryManagedPath(root, to)
	_, fromErr := os.Lstat(fromPath)
	_, toErr := os.Lstat(toPath)
	fromExists := fromErr == nil
	toExists := toErr == nil
	if !fromExists && toExists {
		return nil
	}
	if fromExists && toExists {
		return errors.New("both recovery paths exist")
	}
	if !fromExists && !toExists {
		return errors.New("neither recovery path exists")
	}
	return os.Rename(fromPath, toPath)
}

func recoveryHash(contents []byte) string {
	sum := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func withinRecoveryPath(path, root string) bool {
	path, _ = filepath.Abs(path)
	root, _ = filepath.Abs(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func recoveryManagedPath(root, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(root, filepath.FromSlash(value))
}

func writeRecoveryFile(path string, contents []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".moss-recovery-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
