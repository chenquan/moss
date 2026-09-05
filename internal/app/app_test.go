package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/storage"
)

func TestSystemHandshakeAndHealth(t *testing.T) {
	dir := t.TempDir()
	response := runRequest(t, dir, protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		RequestID:       "req-handshake",
		Operation:       "system.handshake",
		Actor:           protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"},
		Arguments:       map[string]json.RawMessage{},
	})
	if !response.OK {
		t.Fatalf("handshake failed: %+v", response.Error)
	}

	response = runRequest(t, dir, protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		RequestID:       "req-health",
		Operation:       "system.health",
		Actor:           protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"},
		Arguments:       map[string]json.RawMessage{},
	})
	if !response.OK {
		t.Fatalf("health failed: %+v", response.Error)
	}
	data := response.Data.(map[string]any)
	if data["overall"] != "healthy" {
		t.Fatalf("overall = %v", data["overall"])
	}
}

func TestRunCallStdioWritesStructuredResponseWithoutEnvelopeFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOSS_DATA_DIR", filepath.Join(dir, "data"))
	request := protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		RequestID:       "req-stdio-capabilities",
		Operation:       "system.capabilities",
		Actor:           protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"},
		Arguments:       map[string]json.RawMessage{},
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunCallStdio(bytes.NewReader(requestBytes), &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
	var response protocol.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout=%q: %v", stdout.String(), err)
	}
	if !response.OK || response.RequestID != request.RequestID {
		t.Fatalf("response=%+v", response)
	}
	if _, err := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(err) {
		t.Fatalf("stdio capabilities unexpectedly created data directory: %v", err)
	}
}

func TestRunCallStdioReturnsStructuredDecodeError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCallStdio(strings.NewReader(`{"protocol_version":"1.0"}{}`), &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	var response protocol.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout=%q: %v", stdout.String(), err)
	}
	if response.OK || response.Error == nil || response.Error.Code != "REQUEST_INVALID" {
		t.Fatalf("decode response=%+v", response)
	}
}

func TestSystemExportRestoreAndIdempotency(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	firstInput := filepath.Join(dir, "first.md")
	secondInput := filepath.Join(dir, "second.md")
	if err := os.WriteFile(firstInput, []byte("first durable source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondInput, []byte("second source to restore away"), 0600); err != nil {
		t.Fatal(err)
	}
	first := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-first", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(firstInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-backup-first"})
	if !first.OK {
		t.Fatalf("first ingest failed: %+v", first.Error)
	}
	firstID := first.Data.(map[string]any)["source_id"].(string)
	export := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-export", Operation: "system.export", Actor: actor, Arguments: map[string]json.RawMessage{}, IdempotencyKey: "idem-backup-export"}
	exported := runRequest(t, dir, export)
	if !exported.OK {
		t.Fatalf("system.export failed: %+v", exported.Error)
	}
	exportData := exported.Data.(map[string]any)
	backupPath := exportData["backup_path"].(string)
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup archive missing: %v", err)
	}
	if exportData["file_count"].(float64) < 1 || int(exportData["schema_version"].(float64)) != storage.SchemaVersion {
		t.Fatalf("backup metadata = %+v", exportData)
	}
	retry := export
	retry.RequestID = "req-backup-export-retry"
	retried := runRequest(t, dir, retry)
	if !retried.OK || retried.Data.(map[string]any)["backup_path"] != backupPath {
		t.Fatalf("export retry = %+v", retried)
	}
	second := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-second", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(secondInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-backup-second"})
	if !second.OK {
		t.Fatalf("second ingest failed: %+v", second.Error)
	}
	secondID := second.Data.(map[string]any)["source_id"].(string)

	needsConfirmation := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-restore-no-confirm", Operation: "system.restore", Actor: actor, Arguments: map[string]json.RawMessage{"backup_path": json.RawMessage(mustJSON(backupPath)), "confirmed": json.RawMessage(`false`)}, IdempotencyKey: "idem-backup-restore-no-confirm"}
	if response := runRequest(t, dir, needsConfirmation); response.OK || response.Error == nil || response.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("restore confirmation = %+v", response)
	}
	tamperedPath := filepath.Join(dir, "data", "backups", "tampered.moss-backup.zip")
	archiveBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes[len(archiveBytes)-1] ^= 0xff
	if err := os.WriteFile(tamperedPath, archiveBytes, 0600); err != nil {
		t.Fatal(err)
	}
	tampered := needsConfirmation
	tampered.RequestID = "req-backup-restore-tampered"
	tampered.IdempotencyKey = "idem-backup-restore-tampered"
	tampered.Arguments = map[string]json.RawMessage{"backup_path": json.RawMessage(mustJSON(tamperedPath)), "confirmed": json.RawMessage(`true`)}
	if response := runRequest(t, dir, tampered); response.OK || response.Error == nil || response.Error.Code != "BACKUP_INVALID" {
		t.Fatalf("tampered restore = %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-second-still-there", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(secondID))}}); !response.OK {
		t.Fatalf("tampered restore changed state: %+v", response.Error)
	}

	restore := needsConfirmation
	restore.RequestID = "req-backup-restore"
	restore.IdempotencyKey = "idem-backup-restore"
	restore.Arguments = map[string]json.RawMessage{"backup_path": json.RawMessage(mustJSON(backupPath)), "confirmed": json.RawMessage(`true`)}
	restored := runRequest(t, dir, restore)
	if !restored.OK {
		t.Fatalf("system.restore failed: %+v", restored.Error)
	}
	preRestore := restored.Data.(map[string]any)["pre_restore_backup_path"].(string)
	if _, err := os.Stat(preRestore); err != nil {
		t.Fatalf("pre-restore backup missing: %v", err)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-first-restored", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(firstID))}}); !response.OK {
		t.Fatalf("restored source missing: %+v", response.Error)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-second-restored-away", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(secondID))}}); response.OK || response.Error == nil || response.Error.Code != "SOURCE_NOT_FOUND" {
		t.Fatalf("source from after backup survived restore: %+v", response)
	}
	restoreRetry := restore
	restoreRetry.RequestID = "req-backup-restore-retry"
	if response := runRequest(t, dir, restoreRetry); !response.OK || response.Data.(map[string]any)["pre_restore_backup_path"] != preRestore {
		t.Fatalf("restore retry = %+v", response)
	}
	health := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-health", Operation: "system.health", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !health.OK || health.Data.(map[string]any)["overall"] != "healthy" {
		t.Fatalf("post-restore health = %+v", health)
	}
}

func TestSourceCaptureDeduplicatesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "notes.md")
	content := []byte("Moss source content\n")
	if err := os.WriteFile(input, content, 0600); err != nil {
		t.Fatal(err)
	}
	base := protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		Operation:       "source.ingest",
		Actor:           protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"},
		Arguments: map[string]json.RawMessage{
			"input_file":  json.RawMessage(mustJSON(input)),
			"source_type": json.RawMessage(`"markdown"`),
			"sensitivity": json.RawMessage(`"normal"`),
		},
		IdempotencyKey: "idem-source-1",
	}
	base.RequestID = "req-source-1"
	first := runRequest(t, dir, base)
	if !first.OK {
		t.Fatalf("first ingest failed: %+v", first.Error)
	}
	firstData := first.Data.(map[string]any)
	if firstData["duplicate"] != false {
		t.Fatalf("first duplicate = %v", firstData["duplicate"])
	}
	second := base
	second.RequestID = "req-source-retry"
	retry := runRequest(t, dir, second)
	if !retry.OK {
		t.Fatalf("retry failed: %+v", retry.Error)
	}
	if retry.Data.(map[string]any)["source_id"] != firstData["source_id"] {
		t.Fatal("retry did not return original source")
	}

	third := base
	third.RequestID = "req-source-duplicate"
	third.IdempotencyKey = "idem-source-2"
	duplicate := runRequest(t, dir, third)
	if !duplicate.OK || duplicate.Data.(map[string]any)["duplicate"] != true {
		t.Fatalf("duplicate ingest = %+v", duplicate)
	}

	conflict := base
	conflict.RequestID = "req-source-conflict"
	conflict.Arguments = map[string]json.RawMessage{
		"input_file":  json.RawMessage(mustJSON(input)),
		"source_type": json.RawMessage(`"text"`),
		"sensitivity": json.RawMessage(`"normal"`),
	}
	conflictResponse := runRequest(t, dir, conflict)
	if conflictResponse.OK || conflictResponse.Error == nil || conflictResponse.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict response = %+v", conflictResponse)
	}

	get := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-get", Operation: "source.get", Actor: base.Actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(firstData["source_id"].(string)))}}
	getResponse := runRequest(t, dir, get)
	if !getResponse.OK {
		t.Fatalf("get failed: %+v", getResponse.Error)
	}
	getData := getResponse.Data.(map[string]any)
	rawPath := filepath.Join(filepath.Join(dir, "data"), getData["raw_file"].(string))
	if got, err := os.ReadFile(rawPath); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("raw content mismatch: %v", err)
	}

	list := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-list", Operation: "source.list", Actor: base.Actor, Arguments: map[string]json.RawMessage{}}
	listResponse := runRequest(t, dir, list)
	if !listResponse.OK || listResponse.Data.(map[string]any)["count"] != float64(2) {
		t.Fatalf("list response = %+v", listResponse)
	}
}

func TestSensitiveSourceRequiresPermission(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(input, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	req := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-sensitive", Operation: "source.ingest", Actor: protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"text"`), "sensitivity": json.RawMessage(`"sensitive"`)}, IdempotencyKey: "idem-sensitive"}
	created := runRequest(t, dir, req)
	if !created.OK {
		t.Fatalf("sensitive ingest failed: %+v", created.Error)
	}
	sourceID := created.Data.(map[string]any)["source_id"].(string)
	get := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-sensitive-get", Operation: "source.get", Actor: req.Actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}}
	denied := runRequest(t, dir, get)
	if denied.OK || denied.Error == nil || denied.Error.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("denied = %+v", denied)
	}
}

func TestSourceMarkSensitivePropagatesAndRequiresLoweringConfirmation(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	sourceID, _, compilePlan, _, articleID := compilePreviewForTest(t, dir, actor, "sensitivity", "A source-derived article.", "", "sensitivity-target")
	applyCompile := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-compile", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(compilePlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-mark-sensitive-compile"}
	if response := runRequest(t, dir, applyCompile); !response.OK {
		t.Fatalf("compile apply failed: %+v", response.Error)
	}
	createAction := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-action-plan", Operation: "action.create.plan", Actor: actor, Arguments: map[string]json.RawMessage{"kind": json.RawMessage(`"task"`), "title": json.RawMessage(`"Sensitive linked task"`), "source_id": json.RawMessage(mustJSON(sourceID))}, IdempotencyKey: "idem-mark-sensitive-action-plan"})
	if !createAction.OK {
		t.Fatalf("action plan failed: %+v", createAction.Error)
	}
	applyAction := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-action-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(createAction.Data.(map[string]any)["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-mark-sensitive-action-apply"}
	if response := runRequest(t, dir, applyAction); !response.OK {
		t.Fatalf("action apply failed: %+v", response.Error)
	}

	mark := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive", Operation: "source.mark_sensitive", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID)), "sensitivity": json.RawMessage(`"restricted"`)}, IdempotencyKey: "idem-mark-sensitive"}
	marked := runRequest(t, dir, mark)
	if !marked.OK {
		t.Fatalf("mark sensitive failed: %+v", marked.Error)
	}
	markedData := marked.Data.(map[string]any)
	if markedData["previous_sensitivity"] != "normal" || markedData["sensitivity"] != "restricted" || markedData["changed"] != true || len(markedData["propagated_article_ids"].([]any)) != 1 || markedData["propagated_article_ids"].([]any)[0] != articleID || len(markedData["propagated_action_ids"].([]any)) != 1 {
		t.Fatalf("mark response = %+v", markedData)
	}
	retry := mark
	retry.RequestID = "req-mark-sensitive-retry"
	if response := runRequest(t, dir, retry); !response.OK || response.Data.(map[string]any)["sensitivity"] != "restricted" {
		t.Fatalf("mark retry = %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-source-denied", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}}); response.OK || response.Error == nil || response.Error.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("restricted source access = %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-article-denied", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}}); response.OK || response.Error == nil || response.Error.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("propagated article access = %+v", response)
	}
	materializeSensitive := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-article-allowed", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}, Options: map[string]json.RawMessage{"allow_sensitive": json.RawMessage(`true`)}}
	if response := runRequest(t, dir, materializeSensitive); !response.OK || response.Data.(map[string]any)["version"] != float64(2) {
		t.Fatalf("propagated article materialization = %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-action-hidden", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"include_completed": json.RawMessage(`true`)}}); !response.OK || len(response.Data.(map[string]any)["actions"].([]any)) != 0 {
		t.Fatalf("propagated action query = %+v", response)
	}

	lower := mark
	lower.RequestID = "req-mark-sensitive-lower"
	lower.IdempotencyKey = "idem-mark-sensitive-lower"
	lower.Arguments = map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID)), "sensitivity": json.RawMessage(`"normal"`)}
	if response := runRequest(t, dir, lower); response.OK || response.Error == nil || response.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("lowering confirmation = %+v", response)
	}
	lower.Arguments["confirmed"] = json.RawMessage(`true`)
	lower.RequestID = "req-mark-sensitive-lower-confirmed"
	lower.IdempotencyKey = "idem-mark-sensitive-lower-confirmed"
	if response := runRequest(t, dir, lower); !response.OK || response.Data.(map[string]any)["sensitivity"] != "normal" {
		t.Fatalf("confirmed lowering = %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-mark-sensitive-derived-stays-restricted", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}}); response.OK || response.Error == nil || response.Error.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("derived article was relaxed unexpectedly = %+v", response)
	}
	materializeSensitive.RequestID = "req-mark-sensitive-derived-stays-consistent"
	materializeSensitive.IdempotencyKey = "idem-mark-sensitive-derived-stays-consistent"
	if response := runRequest(t, dir, materializeSensitive); !response.OK || response.Data.(map[string]any)["sensitivity"] != "restricted" {
		t.Fatalf("derived article drifted after source lowering = %+v", response)
	}
	invalid := mark
	invalid.RequestID = "req-mark-sensitive-invalid"
	invalid.IdempotencyKey = "idem-mark-sensitive-invalid"
	invalid.Arguments = map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID)), "sensitivity": json.RawMessage(`"unknown"`)}
	if response := runRequest(t, dir, invalid); response.OK || response.Error == nil || response.Error.Code != "SENSITIVITY_INVALID" {
		t.Fatalf("invalid sensitivity = %+v", response)
	}
}

func TestSystemExportRestoreIncludesExtractionArtifacts(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	sourceID, _, planID, _, _ := compilePreviewForTest(t, dir, actor, "backup-extraction", "Extraction-backed article.", "", "backup-extraction")
	apply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-extraction-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-backup-extraction-apply"}
	if response := runRequest(t, dir, apply); !response.OK {
		t.Fatalf("extraction compile apply failed: %+v", response.Error)
	}
	exported := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-extraction-export", Operation: "system.export", Actor: actor, Arguments: map[string]json.RawMessage{}, IdempotencyKey: "idem-backup-extraction-export"})
	if !exported.OK {
		t.Fatalf("extraction export failed: %+v", exported.Error)
	}
	backupPath := exported.Data.(map[string]any)["backup_path"].(string)
	archive, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	foundExtraction := false
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "extractions/") {
			foundExtraction = true
			break
		}
	}
	if !foundExtraction {
		t.Fatalf("backup archive has no extraction artifact")
	}
	postInput := filepath.Join(dir, "post-backup.md")
	if err := os.WriteFile(postInput, []byte("post backup"), 0600); err != nil {
		t.Fatal(err)
	}
	post := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-extraction-post", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(postInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-backup-extraction-post"})
	if !post.OK {
		t.Fatalf("post-backup ingest failed: %+v", post.Error)
	}
	restore := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-extraction-restore", Operation: "system.restore", Actor: actor, Arguments: map[string]json.RawMessage{"backup_path": json.RawMessage(mustJSON(backupPath)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-backup-extraction-restore"})
	if !restore.OK {
		t.Fatalf("extraction restore failed: %+v", restore.Error)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backup-extraction-health", Operation: "system.health", Actor: actor, Arguments: map[string]json.RawMessage{}}); !response.OK || response.Data.(map[string]any)["overall"] != "healthy" {
		t.Fatalf("post-extraction-restore health = %+v", response)
	}
	_ = sourceID
}

func TestProtocolAndSourceValidationErrors(t *testing.T) {
	dir := t.TempDir()
	badVersion := protocol.Request{ProtocolVersion: "9.0", RequestID: "req-version", Operation: "system.capabilities", Actor: protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}, Arguments: map[string]json.RawMessage{}}
	versionResponse := runRequest(t, dir, badVersion)
	if versionResponse.OK || versionResponse.Error == nil || versionResponse.Error.Code != "PROTOCOL_VERSION_UNSUPPORTED" {
		t.Fatalf("version response = %+v", versionResponse)
	}

	directoryRequest := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-directory", Operation: "source.ingest", Actor: badVersion.Actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(dir)), "source_type": json.RawMessage(`"text"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-directory"}
	directoryResponse := runRequest(t, dir, directoryRequest)
	if directoryResponse.OK || directoryResponse.Error == nil || directoryResponse.Error.Code != "SOURCE_INPUT_INVALID" {
		t.Fatalf("directory response = %+v", directoryResponse)
	}

	unknown := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-unknown", Operation: "source.get", Actor: badVersion.Actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(`"src_missing"`)}}
	unknownResponse := runRequest(t, dir, unknown)
	if unknownResponse.OK || unknownResponse.Error == nil || unknownResponse.Error.Code != "SOURCE_NOT_FOUND" {
		t.Fatalf("unknown response = %+v", unknownResponse)
	}
}

func TestHealthReportsRecoveryAndCorruptStorage(t *testing.T) {
	dir := t.TempDir()
	health := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-health-initial", Operation: "system.health", Actor: protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}, Arguments: map[string]json.RawMessage{}}
	if response := runRequest(t, dir, health); !response.OK {
		t.Fatalf("initial health failed: %+v", response.Error)
	}
	staging := filepath.Join(dir, "data", "staging", "orphan.stage")
	if err := os.WriteFile(staging, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	health.RequestID = "req-health-recovery"
	recovery := runRequest(t, dir, health)
	if recovery.OK || recovery.Error == nil || recovery.Error.Code != "STORAGE_UNHEALTHY" {
		t.Fatalf("recovery response = %+v", recovery)
	}
	input := filepath.Join(dir, "blocked.txt")
	if err := os.WriteFile(input, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-blocked-write", Operation: "source.ingest", Actor: health.Actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"text"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-blocked-write"}
	blockedResponse := runRequest(t, dir, blocked)
	if blockedResponse.OK || blockedResponse.Error == nil || blockedResponse.Error.Code != "STORAGE_UNHEALTHY" {
		t.Fatalf("blocked write response = %+v", blockedResponse)
	}
	if err := os.Remove(staging); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "moss.db"), []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	health.RequestID = "req-health-corrupt"
	corrupt := runRequest(t, dir, health)
	if corrupt.OK || corrupt.Error == nil || corrupt.Error.Code != "STORAGE_UNHEALTHY" {
		t.Fatalf("corrupt response = %+v", corrupt)
	}
}

func TestCompilePreviewApplyAndUndo(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "design.md")
	if err := os.WriteFile(input, []byte("The assistant uses a local Skill and CLI.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	ingest := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-ingest", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{
		"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`),
	}, IdempotencyKey: "idem-e2e-ingest"}
	ingestResponse := runRequest(t, dir, ingest)
	if !ingestResponse.OK {
		t.Fatalf("ingest failed: %+v", ingestResponse.Error)
	}
	sourceID := ingestResponse.Data.(map[string]any)["source_id"].(string)

	start := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-start", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}, IdempotencyKey: "idem-e2e-start"}
	startResponse := runRequest(t, dir, start)
	if !startResponse.OK {
		t.Fatalf("compile.start failed: %+v", startResponse.Error)
	}
	jobID := startResponse.Data.(map[string]any)["job_id"].(string)

	stages := map[string]map[string]any{}
	for _, stage := range []string{"extract", "classify", "write"} {
		next := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-next-" + stage, Operation: "compile.next", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}}
		nextResponse := runRequest(t, dir, next)
		if !nextResponse.OK {
			t.Fatalf("compile.next %s failed: %+v", stage, nextResponse.Error)
		}
		stageData := nextResponse.Data.(map[string]any)
		if stageData["stage"] != stage {
			t.Fatalf("next stage = %v, want %s", stageData["stage"], stage)
		}
		stages[stage] = stageData
		resultFile := stageData["result_file"].(string)
		var result any
		switch stage {
		case "extract":
			result = map[string]any{
				"facts":     []any{map[string]any{"text": "The assistant uses a local Skill and CLI.", "source_ids": []string{sourceID}}},
				"decisions": []any{}, "preferences": []any{}, "projects": []any{}, "people": []any{}, "relationships": []any{}, "actions": []any{}, "conflicts": []any{},
				"citations": []any{map[string]any{"source_id": sourceID, "locator": "line 1"}},
			}
		case "classify":
			result = map[string]any{
				"categories": []any{map[string]any{"kind": "decision", "label": "local execution", "source_ids": []string{sourceID}}},
				"outline":    []any{map[string]any{"heading": "Decision", "bullets": []string{"Use a local Skill and CLI."}}},
			}
		case "write":
			result = map[string]any{
				"title": "Local execution decision", "slug": "local-execution-decision", "summary": "Why the assistant runs locally.", "body": "The assistant is driven by a local Claude Skill and an internal CLI.", "sensitivity": "normal", "tags": []string{"architecture", "moss"}, "source_ids": []string{sourceID}, "citations": []any{map[string]any{"source_id": sourceID, "locator": "line 1"}},
			}
		}
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(resultFile, payload, 0600); err != nil {
			t.Fatal(err)
		}
		submit := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-submit-" + stage, Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{
			"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(mustJSON(stage)), "result_file": json.RawMessage(mustJSON(resultFile)),
		}, IdempotencyKey: "idem-e2e-submit-" + stage}
		submitResponse := runRequest(t, dir, submit)
		if !submitResponse.OK {
			t.Fatalf("compile.submit %s failed: %+v", stage, submitResponse.Error)
		}
	}

	preview := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-preview", Operation: "compile.preview", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}, IdempotencyKey: "idem-e2e-preview"}
	previewResponse := runRequest(t, dir, preview)
	if !previewResponse.OK {
		t.Fatalf("compile.preview failed: %+v", previewResponse.Error)
	}
	previewData := previewResponse.Data.(map[string]any)
	planID := previewData["plan_id"].(string)
	articlePath := previewData["path"].(string)
	if _, err := os.Stat(articlePath); !os.IsNotExist(err) {
		t.Fatalf("preview mutated article path: err=%v", err)
	}
	needsConfirmation := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-apply-needs-confirmation", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`false`)}, IdempotencyKey: "idem-e2e-apply-needs-confirmation"}
	if response := runRequest(t, dir, needsConfirmation); response.OK || response.Error == nil || response.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("missing confirmation response = %+v", response)
	}

	inspect := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-inspect", Operation: "plan.inspect", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID))}}
	inspectResponse := runRequest(t, dir, inspect)
	if !inspectResponse.OK || inspectResponse.Data.(map[string]any)["state"] != "pending" {
		t.Fatalf("inspect response = %+v", inspectResponse)
	}

	apply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-e2e-apply"}
	applyResponse := runRequest(t, dir, apply)
	if !applyResponse.OK {
		t.Fatalf("plan.apply failed: %+v", applyResponse.Error)
	}
	applyRetry := apply
	applyRetry.RequestID = "req-e2e-apply-retry"
	if response := runRequest(t, dir, applyRetry); !response.OK || response.Data.(map[string]any)["plan_id"] != planID {
		t.Fatalf("idempotent plan.apply retry = %+v", response)
	}
	article, err := os.ReadFile(articlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(article), "moss_article_id:") || !strings.HasSuffix(string(article), "internal CLI.\n") {
		t.Fatalf("unexpected article content: %s", article)
	}

	undo := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-e2e-undo", Operation: "plan.undo", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-e2e-undo"}
	undoResponse := runRequest(t, dir, undo)
	if !undoResponse.OK {
		t.Fatalf("plan.undo failed: %+v", undoResponse.Error)
	}
	if _, err := os.Stat(articlePath); !os.IsNotExist(err) {
		t.Fatalf("article still exists after undo: err=%v", err)
	}
	trashEntries, err := os.ReadDir(filepath.Join(dir, "data", "trash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(trashEntries) != 1 {
		t.Fatalf("trash entries = %d, want 1", len(trashEntries))
	}
}

func TestCompileApplyOperationUsesCompileGateway(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	_, _, planID, articlePath, _ := compilePreviewForTest(t, dir, actor, "compile-apply-operation", "A compile gateway article.", "", "compile-apply-target")

	missingConfirmation := protocol.Request{
		ProtocolVersion: protocol.SupportedVersion,
		RequestID:       "req-compile-apply-missing-confirmation",
		Operation:       "compile.apply",
		Actor:           actor,
		Arguments:       map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`false`)},
		IdempotencyKey:  "idem-compile-apply-missing-confirmation",
	}
	if response := runRequest(t, dir, missingConfirmation); response.OK || response.Error == nil || response.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("compile.apply confirmation response = %+v", response)
	}

	apply := missingConfirmation
	apply.RequestID = "req-compile-apply"
	apply.IdempotencyKey = "idem-compile-apply"
	apply.Arguments = map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}
	response := runRequest(t, dir, apply)
	if !response.OK || response.Data.(map[string]any)["plan_id"] != planID {
		t.Fatalf("compile.apply response = %+v", response)
	}
	retry := apply
	retry.RequestID = "req-compile-apply-retry"
	retried := runRequest(t, dir, retry)
	if !retried.OK || retried.Data.(map[string]any)["plan_id"] != planID {
		t.Fatalf("compile.apply retry = %+v", retried)
	}
	if _, err := os.Stat(articlePath); err != nil {
		t.Fatalf("compile.apply did not materialize article: %v", err)
	}
}

func TestCompileRejectsOutOfOrderAndInvalidStageResult(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(input, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	ingest := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-ingest", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"text"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-invalid-ingest"})
	sourceID := ingest.Data.(map[string]any)["source_id"].(string)
	started := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-start", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}, IdempotencyKey: "idem-invalid-start"})
	jobID := started.Data.(map[string]any)["job_id"].(string)
	wrong := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-order", Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(`"classify"`), "result_file": json.RawMessage(`"/tmp/nope"`)}, IdempotencyKey: "idem-invalid-order"})
	if wrong.OK || wrong.Error == nil || wrong.Error.Code != "STAGE_OUT_OF_ORDER" {
		t.Fatalf("out-of-order response = %+v", wrong)
	}
	next := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-next", Operation: "compile.next", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}})
	resultFile := next.Data.(map[string]any)["result_file"].(string)
	if err := os.WriteFile(resultFile, []byte(`{"facts":`), 0600); err != nil {
		t.Fatal(err)
	}
	bad := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-json", Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(`"extract"`), "result_file": json.RawMessage(mustJSON(resultFile))}, IdempotencyKey: "idem-invalid-json"})
	if bad.OK || bad.Error == nil || bad.Error.Code != "STAGE_RESULT_INVALID" {
		t.Fatalf("invalid stage response = %+v", bad)
	}
	validExtract := map[string]any{"facts": []any{}, "decisions": []any{}, "preferences": []any{}, "projects": []any{}, "people": []any{}, "relationships": []any{}, "actions": []any{}, "conflicts": []any{}, "citations": []any{}}
	payload, _ := json.Marshal(validExtract)
	if err := os.WriteFile(resultFile, payload, 0600); err != nil {
		t.Fatal(err)
	}
	validSubmit := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-valid-submit", Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(`"extract"`), "result_file": json.RawMessage(mustJSON(resultFile))}, IdempotencyKey: "idem-invalid-valid-submit"}
	first := runRequest(t, dir, validSubmit)
	if !first.OK {
		t.Fatalf("valid extract failed: %+v", first.Error)
	}
	retry := validSubmit
	retry.RequestID = "req-invalid-valid-retry"
	if response := runRequest(t, dir, retry); !response.OK {
		t.Fatalf("idempotent stage retry failed: %+v", response.Error)
	}
	duplicate := validSubmit
	duplicate.RequestID = "req-invalid-duplicate"
	duplicate.IdempotencyKey = "idem-invalid-duplicate"
	duplicateResponse := runRequest(t, dir, duplicate)
	if duplicateResponse.OK || duplicateResponse.Error == nil || duplicateResponse.Error.Code != "STAGE_ALREADY_SUBMITTED" {
		t.Fatalf("duplicate stage response = %+v code=%v message=%v", duplicateResponse, duplicateResponse.Error.Code, duplicateResponse.Error.Message)
	}

	// The next stage's exact managed result path must reject an oversized payload before parsing.
	classifyNext := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-classify-next", Operation: "compile.next", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}})
	classifyResultFile := classifyNext.Data.(map[string]any)["result_file"].(string)
	if err := os.WriteFile(classifyResultFile, []byte(strings.Repeat("x", 4<<20+1)), 0600); err != nil {
		t.Fatal(err)
	}
	overSized := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-invalid-oversized", Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(`"classify"`), "result_file": json.RawMessage(mustJSON(classifyResultFile))}, IdempotencyKey: "idem-invalid-oversized"})
	if overSized.OK || overSized.Error == nil || overSized.Error.Code != "STAGE_RESULT_UNREADABLE" {
		t.Fatalf("oversized stage response = %+v", overSized)
	}
}

func TestCompileUnknownSourceRetryAndAbort(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	unknown := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-abort-unknown", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(`"src_missing"`)}, IdempotencyKey: "idem-abort-unknown"})
	if unknown.OK || unknown.Error == nil || unknown.Error.Code != "SOURCE_NOT_FOUND" {
		t.Fatalf("unknown source response = %+v", unknown)
	}
	input := filepath.Join(dir, "abort.txt")
	if err := os.WriteFile(input, []byte("abort me"), 0600); err != nil {
		t.Fatal(err)
	}
	ingest := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-abort-ingest", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"text"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-abort-ingest"})
	sourceID := ingest.Data.(map[string]any)["source_id"].(string)
	start := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-abort-start", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}, IdempotencyKey: "idem-abort-start"}
	first := runRequest(t, dir, start)
	jobID := first.Data.(map[string]any)["job_id"].(string)
	retry := start
	retry.RequestID = "req-abort-start-retry"
	if response := runRequest(t, dir, retry); !response.OK || response.Data.(map[string]any)["job_id"] != jobID {
		t.Fatalf("compile.start retry = %+v", response)
	}
	abort := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-abort-job", Operation: "compile.abort", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}, IdempotencyKey: "idem-abort-job"})
	if !abort.OK || abort.Data.(map[string]any)["state"] != "aborted" {
		t.Fatalf("abort response = %+v", abort)
	}
	next := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-abort-next", Operation: "compile.next", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}})
	if !next.OK || next.Data.(map[string]any)["state"] != "aborted" {
		t.Fatalf("aborted next response = %+v", next)
	}
}

func TestKnowledgeRetrievalCatalogCandidatesMaterializeAndHistory(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	_, _, planID, articlePath, articleID := compilePreviewForTest(t, dir, actor, "retrieve", "The local decision is versioned and cited.", "", "retrieval-decision")
	apply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-retrieve-apply"}
	if response := runRequest(t, dir, apply); !response.OK {
		t.Fatalf("retrieve article apply failed: %+v", response.Error)
	}

	catalog := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-catalog", Operation: "knowledge.catalog", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !catalog.OK {
		t.Fatalf("catalog failed: %+v", catalog.Error)
	}
	catalogData := catalog.Data.(map[string]any)
	if catalogData["count"] != float64(1) || len(catalogData["topics"].([]any)) != 1 {
		t.Fatalf("catalog data = %+v", catalogData)
	}

	candidates := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-candidates", Operation: "knowledge.candidates", Actor: actor, Arguments: map[string]json.RawMessage{"query": json.RawMessage(`"versioned"`), "limit": json.RawMessage(`5`)}})
	if !candidates.OK {
		t.Fatalf("candidates failed: %+v", candidates.Error)
	}
	candidateList := candidates.Data.(map[string]any)["candidates"].([]any)
	if len(candidateList) != 1 || candidateList[0].(map[string]any)["article_id"] != articleID {
		t.Fatalf("candidate list = %+v", candidateList)
	}

	materialize := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-materialize", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}})
	if !materialize.OK {
		t.Fatalf("materialize failed: %+v", materialize.Error)
	}
	materializedData := materialize.Data.(map[string]any)
	if !strings.Contains(materializedData["content"].(string), "versioned and cited") || materializedData["bytes"].(float64) <= 0 || len(materializedData["citations"].([]any)) != 1 {
		t.Fatalf("materialized data = %+v", materializedData)
	}
	materializePathOnly := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-materialize-path-only", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}, Options: map[string]json.RawMessage{"inline_content": json.RawMessage(`false`)}})
	if !materializePathOnly.OK {
		t.Fatalf("path-only materialize failed: %+v", materializePathOnly.Error)
	}
	pathOnlyData := materializePathOnly.Data.(map[string]any)
	if _, present := pathOnlyData["content"]; present || pathOnlyData["path"] != articlePath || pathOnlyData["bytes"].(float64) <= 0 {
		t.Fatalf("path-only materialized data = %+v", pathOnlyData)
	}

	history := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-history", Operation: "knowledge.history", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID)), "include_content": json.RawMessage(`true`)}})
	if !history.OK {
		t.Fatalf("history failed: %+v", history.Error)
	}
	versions := history.Data.(map[string]any)["versions"].([]any)
	if len(versions) != 1 || !strings.Contains(versions[0].(map[string]any)["content"].(string), "versioned and cited") {
		t.Fatalf("history versions = %+v", versions)
	}

	if err := os.WriteFile(articlePath, append(mustReadFile(t, articlePath), []byte("manual drift\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	driftCandidates := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-drift-candidates", Operation: "knowledge.candidates", Actor: actor, Arguments: map[string]json.RawMessage{"query": json.RawMessage(`"retrieval"`)}})
	if !driftCandidates.OK {
		t.Fatalf("drift candidates failed: %+v", driftCandidates.Error)
	}
	driftList := driftCandidates.Data.(map[string]any)["candidates"].([]any)
	if len(driftList) != 1 || driftList[0].(map[string]any)["drift"] != true || driftList[0].(map[string]any)["snippet"] != nil {
		t.Fatalf("drift candidate = %+v", driftList)
	}
	driftMaterialize := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-drift-materialize", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}})
	if driftMaterialize.OK || driftMaterialize.Error == nil || driftMaterialize.Error.Code != "WIKI_DRIFT" {
		t.Fatalf("drift materialize = %+v", driftMaterialize)
	}

	dataDir := filepath.Join(dir, "data")
	t.Setenv("MOSS_DATA_DIR", dataDir)
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE articles SET sensitivity = 'sensitive' WHERE article_id = ?`, articleID); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_ = store.Close()
	sensitiveCatalog := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-sensitive-catalog", Operation: "knowledge.catalog", Actor: actor, Arguments: map[string]json.RawMessage{}})
	if !sensitiveCatalog.OK || sensitiveCatalog.Data.(map[string]any)["count"] != float64(0) {
		t.Fatalf("sensitive catalog = %+v", sensitiveCatalog)
	}
	sensitiveMaterialize := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-sensitive-materialize", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}})
	if sensitiveMaterialize.OK || sensitiveMaterialize.Error == nil || sensitiveMaterialize.Error.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("sensitive materialize = %+v", sensitiveMaterialize)
	}
	sensitiveHistory := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-retrieve-sensitive-history", Operation: "knowledge.history", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}})
	if sensitiveHistory.OK || sensitiveHistory.Error == nil || sensitiveHistory.Error.Code != "SENSITIVITY_DENIED" {
		t.Fatalf("sensitive history = %+v", sensitiveHistory)
	}
}

func TestMultiSourceBatchPlanAppliesArticlesAndFactsAtomically(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	inputs := []string{filepath.Join(dir, "one.md"), filepath.Join(dir, "two.md")}
	for i, input := range inputs {
		if err := os.WriteFile(input, []byte(fmt.Sprintf("source %d", i+1)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sourceIDs := make([]string, 0, 2)
	for i, input := range inputs {
		response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: fmt.Sprintf("req-multi-ingest-%d", i), Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`), "origin_key": json.RawMessage(`"notes/project"`)}, IdempotencyKey: fmt.Sprintf("idem-multi-ingest-%d", i)})
		if !response.OK {
			t.Fatalf("ingest failed: %+v", response.Error)
		}
		sourceIDs = append(sourceIDs, response.Data.(map[string]any)["source_id"].(string))
	}
	start := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-start", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_ids": json.RawMessage(mustJSON(sourceIDs)), "pipeline": json.RawMessage(`{"extractor_version":"test","prompt_hash":"p1","schema_version":"1","strategy":"multi"}`)}, IdempotencyKey: "idem-multi-start"})
	if !start.OK {
		t.Fatalf("multi start failed: %+v", start.Error)
	}
	data := start.Data.(map[string]any)
	jobID := data["job_id"].(string)
	stages := data["stages"].([]any)
	writeStage := func(stage int, contents string, key string) {
		path := stages[stage].(map[string]any)["result_file"].(string)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-" + key, Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(mustJSON([]string{"extract", "classify", "write"}[stage])), "result_file": json.RawMessage(mustJSON(path))}, IdempotencyKey: "idem-multi-" + key})
		if !response.OK {
			t.Fatalf("multi submit %s failed: %+v", key, response.Error)
		}
	}
	writeStage(0, fmt.Sprintf(`{"facts":[{"text":"shared fact","source_ids":[%q,%q]}],"decisions":[],"preferences":[],"projects":[],"people":[],"relationships":[],"actions":[],"conflicts":[],"citations":[]}`, sourceIDs[0], sourceIDs[1]), "extract")
	writeStage(1, `{"categories":[],"outline":[]}`, "classify")
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var extractionID string
	if err := store.DB.QueryRow(`SELECT extraction_id FROM extractions ORDER BY created_at LIMIT 1`).Scan(&extractionID); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_ = store.Close()
	writeStage(2, fmt.Sprintf(`{"articles":[{"operation":"create","title":"Shared Note","slug":"shared-note","summary":"A shared note","body":"Both sources agree.","sensitivity":"normal","tags":["project"],"source_ids":[%q,%q],"citations":[{"source_id":%q,"locator":"line 1"}]}],"facts":[{"operation":"create","fact_key":"project:shared","kind":"fact","text":"Both sources agree.","status":"active","source_ids":[%q,%q],"extraction_id":%q}],"relations":[{"relation_type":"supports","from":{"type":"source","id":%q},"to":{"type":"source","id":%q},"source_id":%q}]}`, sourceIDs[0], sourceIDs[1], sourceIDs[0], sourceIDs[0], sourceIDs[1], extractionID, sourceIDs[0], sourceIDs[1], sourceIDs[0]), "write")
	preview := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-preview", Operation: "compile.preview", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}, IdempotencyKey: "idem-multi-preview"})
	if !preview.OK {
		t.Fatalf("multi preview failed: %+v", preview.Error)
	}
	planID := preview.Data.(map[string]any)["plan_id"].(string)
	apply := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-apply", Operation: "compile.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-multi-apply"})
	if !apply.OK {
		t.Fatalf("multi apply failed: %+v", apply.Error)
	}
	store, err = storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var revision int
	if err := store.DB.QueryRow(`SELECT origin_revision FROM sources WHERE source_id = ?`, sourceIDs[1]).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("origin revision = %d, err = %v", revision, err)
	}
	var extractionCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM extractions WHERE status = 'active'`).Scan(&extractionCount); err != nil || extractionCount != 2 {
		t.Fatalf("active extraction count = %d, err = %v", extractionCount, err)
	}
	var factStatus string
	if err := store.DB.QueryRow(`SELECT status FROM facts WHERE kind = 'fact' AND fact_key = 'project:shared'`).Scan(&factStatus); err != nil || factStatus != "active" {
		t.Fatalf("fact status = %q, err = %v", factStatus, err)
	}
	var factCitationCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM fact_citations fc JOIN facts f ON f.fact_id = fc.fact_id AND f.current_version = fc.version WHERE f.kind = 'fact' AND f.fact_key = 'project:shared'`).Scan(&factCitationCount); err != nil || factCitationCount != 2 {
		t.Fatalf("current fact citation count = %d, err = %v", factCitationCount, err)
	}
	var relationCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM relations WHERE relation_type = 'supports' AND from_id = ? AND to_id = ?`, sourceIDs[0], sourceIDs[1]).Scan(&relationCount); err != nil || relationCount != 1 {
		t.Fatalf("batch relation count = %d, err = %v", relationCount, err)
	}
	reindex := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-reindex", Operation: "knowledge.reindex", Actor: actor, Arguments: map[string]json.RawMessage{}, IdempotencyKey: "idem-multi-reindex"})
	if !reindex.OK {
		t.Fatalf("reindex failed: %+v", reindex.Error)
	}
	candidates := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-candidates", Operation: "knowledge.candidates", Actor: actor, Arguments: map[string]json.RawMessage{"query": json.RawMessage(`"agree"`)}})
	if !candidates.OK || len(candidates.Data.(map[string]any)["candidates"].([]any)) != 1 {
		t.Fatalf("FTS candidates = %+v", candidates)
	}
	newInput := filepath.Join(dir, "three.md")
	if err := os.WriteFile(newInput, []byte("new revision"), 0600); err != nil {
		t.Fatal(err)
	}
	newSource := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-revision", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(newInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`), "origin_key": json.RawMessage(`"notes/project"`)}, IdempotencyKey: "idem-multi-revision"})
	if !newSource.OK {
		t.Fatalf("revision ingest failed: %+v", newSource.Error)
	}
	var freshness, extractionStatus string
	if err := store.DB.QueryRow(`SELECT freshness FROM facts WHERE fact_key = 'project:shared'`).Scan(&freshness); err != nil || freshness != "stale" {
		t.Fatalf("fact freshness = %q, err = %v", freshness, err)
	}
	if err := store.DB.QueryRow(`SELECT status FROM extractions WHERE extraction_id = ?`, extractionID).Scan(&extractionStatus); err != nil || extractionStatus != "stale" {
		t.Fatalf("extraction status = %q, err = %v", extractionStatus, err)
	}
	var factID string
	if err := store.DB.QueryRow(`SELECT fact_id FROM facts WHERE fact_key = 'project:shared'`).Scan(&factID); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	start2 := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-update-start", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_ids": json.RawMessage(mustJSON(sourceIDs))}, IdempotencyKey: "idem-multi-update-start"})
	if !start2.OK {
		t.Fatalf("fact update start failed: %+v", start2.Error)
	}
	data2 := start2.Data.(map[string]any)
	jobID2 := data2["job_id"].(string)
	stages2 := data2["stages"].([]any)
	submitStage2 := func(index int, contents string, key string) {
		path := stages2[index].(map[string]any)["result_file"].(string)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-update-" + key, Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID2)), "stage": json.RawMessage(mustJSON([]string{"extract", "classify", "write"}[index])), "result_file": json.RawMessage(mustJSON(path))}, IdempotencyKey: "idem-multi-update-" + key})
		if !response.OK {
			t.Fatalf("fact update submit %s failed: %+v", key, response.Error)
		}
	}
	submitStage2(0, fmt.Sprintf(`{"facts":[],"decisions":[],"preferences":[],"projects":[],"people":[],"relationships":[],"actions":[],"conflicts":[],"citations":[]}`), "extract")
	submitStage2(1, `{"categories":[],"outline":[]}`, "classify")
	submitStage2(2, fmt.Sprintf(`{"articles":[],"facts":[{"operation":"update","fact_id":%q,"fact_key":"project:shared","kind":"fact","text":"The revised sources still agree.","status":"active","source_ids":[%q,%q]}]}`, factID, sourceIDs[0], sourceIDs[1]), "write")
	preview2 := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-update-preview", Operation: "compile.preview", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID2))}, IdempotencyKey: "idem-multi-update-preview"})
	if !preview2.OK {
		t.Fatalf("fact update preview failed: %+v", preview2.Error)
	}
	plan2 := preview2.Data.(map[string]any)["plan_id"].(string)
	apply2 := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-update-apply", Operation: "compile.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(plan2)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-multi-update-apply"})
	if !apply2.OK {
		t.Fatalf("fact update apply failed: %+v", apply2.Error)
	}
	store, err = storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var versionCount, supersededCount int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM fact_versions WHERE fact_id = ?`, factID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM fact_versions WHERE fact_id = ? AND status = 'superseded'`, factID).Scan(&supersededCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 2 || supersededCount != 1 {
		t.Fatalf("fact versions = %d, superseded = %d", versionCount, supersededCount)
	}
	article := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-materialize", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"slug": json.RawMessage(`"shared-note"`)}})
	if !article.OK || !strings.Contains(article.Data.(map[string]any)["content"].(string), "Both sources agree") {
		t.Fatalf("materialized batch article = %+v", article)
	}

	// A relation-only batch must be undone atomically: undo removes only the
	// relations its own plan created and leaves other batch relations intact.
	start3 := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-relation-start", Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_ids": json.RawMessage(mustJSON(sourceIDs))}, IdempotencyKey: "idem-multi-relation-start"})
	if !start3.OK {
		t.Fatalf("relation batch start failed: %+v", start3.Error)
	}
	data3 := start3.Data.(map[string]any)
	jobID3 := data3["job_id"].(string)
	stages3 := data3["stages"].([]any)
	submitStage3 := func(index int, contents string, key string) {
		path := stages3[index].(map[string]any)["result_file"].(string)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-relation-" + key, Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID3)), "stage": json.RawMessage(mustJSON([]string{"extract", "classify", "write"}[index])), "result_file": json.RawMessage(mustJSON(path))}, IdempotencyKey: "idem-multi-relation-" + key})
		if !response.OK {
			t.Fatalf("relation batch submit %s failed: %+v", key, response.Error)
		}
	}
	submitStage3(0, `{"facts":[],"decisions":[],"preferences":[],"projects":[],"people":[],"relationships":[],"actions":[],"conflicts":[],"citations":[]}`, "extract")
	submitStage3(1, `{"categories":[],"outline":[]}`, "classify")
	submitStage3(2, fmt.Sprintf(`{"articles":[],"facts":[],"relations":[{"relation_type":"depends_on","from":{"type":"source","id":%q},"to":{"type":"source","id":%q}}]}`, sourceIDs[1], sourceIDs[0]), "write")
	preview3 := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-relation-preview", Operation: "compile.preview", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID3))}, IdempotencyKey: "idem-multi-relation-preview"})
	if !preview3.OK {
		t.Fatalf("relation batch preview failed: %+v", preview3.Error)
	}
	plan3 := preview3.Data.(map[string]any)["plan_id"].(string)
	apply3 := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-relation-apply", Operation: "compile.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(plan3)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-multi-relation-apply"})
	if !apply3.OK {
		t.Fatalf("relation batch apply failed: %+v", apply3.Error)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM relations WHERE relation_type = 'depends_on' AND from_id = ? AND to_id = ?`, sourceIDs[1], sourceIDs[0]).Scan(&relationCount); err != nil || relationCount != 1 {
		t.Fatalf("relation batch count = %d, err = %v", relationCount, err)
	}
	undoRelationBatch := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-multi-relation-undo", Operation: "plan.undo", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(plan3)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-multi-relation-undo"})
	if !undoRelationBatch.OK {
		t.Fatalf("relation batch undo failed: %+v", undoRelationBatch.Error)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM relations WHERE relation_type = 'depends_on'`).Scan(&relationCount); err != nil || relationCount != 0 {
		t.Fatalf("batch-created relation survived undo: count = %d, err = %v", relationCount, err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM relations WHERE relation_type = 'supports' AND from_id = ? AND to_id = ?`, sourceIDs[0], sourceIDs[1]).Scan(&relationCount); err != nil || relationCount != 1 {
		t.Fatalf("first batch relation was removed by another batch undo: count = %d, err = %v", relationCount, err)
	}
}

func TestLegacyBackfillPlanIsExplicitAndModelFree(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	input := filepath.Join(dir, "legacy.md")
	if err := os.WriteFile(input, []byte("legacy source"), 0600); err != nil {
		t.Fatal(err)
	}
	ingest := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backfill-ingest", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-backfill-ingest"})
	if !ingest.OK {
		t.Fatalf("backfill ingest failed: %+v", ingest.Error)
	}
	sourceID := ingest.Data.(map[string]any)["source_id"].(string)
	plan := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-backfill-plan", Operation: "knowledge.backfill.plan", Actor: actor, Arguments: map[string]json.RawMessage{"source_ids": json.RawMessage(mustJSON([]string{sourceID}))}, IdempotencyKey: "idem-backfill-plan"})
	if !plan.OK {
		t.Fatalf("backfill plan failed: %+v", plan.Error)
	}
	data := plan.Data.(map[string]any)
	if data["requires_model"] != true || data["automatic_on_upgrade"] != false || len(data["source_ids"].([]any)) != 1 {
		t.Fatalf("backfill manifest = %+v", data)
	}
}

func TestActionLedgerPlansApplyQueryAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	input := filepath.Join(dir, "action-source.md")
	if err := os.WriteFile(input, []byte("Action provenance"), 0600); err != nil {
		t.Fatal(err)
	}
	ingest := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-source", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-action-source"})
	sourceID := ingest.Data.(map[string]any)["source_id"].(string)
	today := time.Now().Local().Format("2006-01-02")
	create := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-create", Operation: "action.create.plan", Actor: actor, Arguments: map[string]json.RawMessage{
		"kind": json.RawMessage(`"task"`), "title": json.RawMessage(`"Review local decision"`), "details": json.RawMessage(`"Check the cited article"`), "due_at": json.RawMessage(mustJSON(today)), "waiting_for": json.RawMessage(`"Chen"`), "source_id": json.RawMessage(mustJSON(sourceID)),
	}, IdempotencyKey: "idem-action-create"}
	createdPlan := runRequest(t, dir, create)
	if !createdPlan.OK {
		t.Fatalf("action.create.plan failed: %+v", createdPlan.Error)
	}
	planID := createdPlan.Data.(map[string]any)["plan_id"].(string)
	actionID := createdPlan.Data.(map[string]any)["action_id"].(string)
	missingConfirmation := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-missing-confirm", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`false`)}, IdempotencyKey: "idem-action-missing-confirm"}
	if response := runRequest(t, dir, missingConfirmation); response.OK || response.Error == nil || response.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("missing action confirmation = %+v", response)
	}
	apply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-action-apply"}
	applied := runRequest(t, dir, apply)
	if !applied.OK || applied.Data.(map[string]any)["action_id"] != actionID || applied.Data.(map[string]any)["revision"] != float64(1) {
		t.Fatalf("action.apply = %+v", applied)
	}
	retry := apply
	retry.RequestID = "req-action-apply-retry"
	if response := runRequest(t, dir, retry); !response.OK || response.Data.(map[string]any)["action_id"] != actionID {
		t.Fatalf("action.apply retry = %+v", response)
	}
	query := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-query", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"date": json.RawMessage(mustJSON(today))}})
	if !query.OK {
		t.Fatalf("action.query failed: %+v", query.Error)
	}
	queryData := query.Data.(map[string]any)
	if len(queryData["today"].([]any)) != 1 || len(queryData["waiting"].([]any)) != 1 {
		t.Fatalf("action query = %+v", queryData)
	}

	update := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-update", Operation: "action.update.plan", Actor: actor, Arguments: map[string]json.RawMessage{"action_id": json.RawMessage(mustJSON(actionID)), "status": json.RawMessage(`"done"`)}, IdempotencyKey: "idem-action-update"}
	updatePlan := runRequest(t, dir, update)
	if !updatePlan.OK {
		t.Fatalf("action.update.plan failed: %+v", updatePlan.Error)
	}
	updateID := updatePlan.Data.(map[string]any)["plan_id"].(string)
	applyUpdate := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-update-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(updateID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-action-update-apply"}
	if response := runRequest(t, dir, applyUpdate); !response.OK || response.Data.(map[string]any)["status"] != "done" {
		t.Fatalf("done action apply = %+v", response)
	}
	completedQuery := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-query-completed", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"date": json.RawMessage(mustJSON(today)), "include_completed": json.RawMessage(`true`)}})
	if !completedQuery.OK || len(completedQuery.Data.(map[string]any)["actions"].([]any)) != 1 {
		t.Fatalf("completed action query = %+v", completedQuery)
	}

	staleA := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-stale-a", Operation: "action.update.plan", Actor: actor, Arguments: map[string]json.RawMessage{"action_id": json.RawMessage(mustJSON(actionID)), "status": json.RawMessage(`"open"`)}, IdempotencyKey: "idem-action-stale-a"})
	staleB := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-stale-b", Operation: "action.update.plan", Actor: actor, Arguments: map[string]json.RawMessage{"action_id": json.RawMessage(mustJSON(actionID)), "status": json.RawMessage(`"deferred"`)}, IdempotencyKey: "idem-action-stale-b"})
	applyStaleA := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-stale-a-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(staleA.Data.(map[string]any)["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-action-stale-a-apply"}
	if response := runRequest(t, dir, applyStaleA); !response.OK {
		t.Fatalf("stale A apply failed: %+v", response.Error)
	}
	applyStaleB := applyStaleA
	applyStaleB.RequestID = "req-action-stale-b-apply"
	applyStaleB.IdempotencyKey = "idem-action-stale-b-apply"
	applyStaleB.Arguments = map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(staleB.Data.(map[string]any)["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}
	if response := runRequest(t, dir, applyStaleB); response.OK || response.Error == nil || response.Error.Code != "ACTION_PLAN_STALE" {
		t.Fatalf("stale B apply = %+v", response)
	}
	expiredPlanResponse := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-expired-plan", Operation: "action.create.plan", Actor: actor, Arguments: map[string]json.RawMessage{"kind": json.RawMessage(`"reminder"`), "title": json.RawMessage(`"Expired reminder"`)}, IdempotencyKey: "idem-action-expired-plan"})
	expiredPlanID := expiredPlanResponse.Data.(map[string]any)["plan_id"].(string)
	t.Setenv("MOSS_DATA_DIR", filepath.Join(dir, "data"))
	store, err := storage.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`UPDATE action_plans SET expires_at = ? WHERE plan_id = ?`, "2000-01-01T00:00:00Z", expiredPlanID); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_ = store.Close()
	expiredApply := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-expired-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(expiredPlanID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-action-expired-apply"})
	if expiredApply.OK || expiredApply.Error == nil || expiredApply.Error.Code != "ACTION_PLAN_EXPIRED" {
		t.Fatalf("expired action apply = %+v", expiredApply)
	}

	sensitive := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-sensitive", Operation: "action.create.plan", Actor: actor, Arguments: map[string]json.RawMessage{"kind": json.RawMessage(`"reminder"`), "title": json.RawMessage(`"Sensitive reminder"`), "sensitivity": json.RawMessage(`"sensitive"`)}, IdempotencyKey: "idem-action-sensitive"})
	sensitiveApply := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-sensitive-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(sensitive.Data.(map[string]any)["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-action-sensitive-apply"})
	if !sensitiveApply.OK {
		t.Fatalf("sensitive action apply failed: %+v", sensitiveApply.Error)
	}
	normalOnly := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-normal-only", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"include_completed": json.RawMessage(`true`)}})
	if !normalOnly.OK || len(normalOnly.Data.(map[string]any)["actions"].([]any)) != 1 {
		t.Fatalf("normal action query = %+v", normalOnly)
	}
	allowSensitive := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-action-allow-sensitive", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"include_completed": json.RawMessage(`true`)}, Options: map[string]json.RawMessage{"allow_sensitive": json.RawMessage(`true`)}}
	if response := runRequest(t, dir, allowSensitive); !response.OK || len(response.Data.(map[string]any)["actions"].([]any)) != 2 {
		t.Fatalf("allow sensitive action query = %+v", response)
	}
}

func TestSafetyForgetPlanApplyUndoSharedBlobAndAudit(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	sourceID, _, compilePlan, articlePath, articleID := compilePreviewForTest(t, dir, actor, "forget", "This article should be forgotten.", "", "forget-target")
	applyCompile := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-compile-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(compilePlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-forget-compile-apply"}
	if response := runRequest(t, dir, applyCompile); !response.OK {
		t.Fatalf("compile apply failed: %+v", response.Error)
	}

	// A second source with identical bytes keeps the content-addressed Raw blob active.
	duplicateInput := filepath.Join(dir, "forget.md")
	duplicate := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-duplicate", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(duplicateInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-forget-duplicate"})
	if !duplicate.OK {
		t.Fatalf("duplicate ingest failed: %+v", duplicate.Error)
	}
	secondSourceID := duplicate.Data.(map[string]any)["source_id"].(string)
	if secondSourceID == sourceID || duplicate.Data.(map[string]any)["duplicate"] != true {
		t.Fatalf("unexpected duplicate source: %+v", duplicate.Data)
	}

	createAction := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-action-plan", Operation: "action.create.plan", Actor: actor, Arguments: map[string]json.RawMessage{"kind": json.RawMessage(`"task"`), "title": json.RawMessage(`"Forget-linked task"`), "source_id": json.RawMessage(mustJSON(sourceID))}, IdempotencyKey: "idem-forget-action-plan"})
	if !createAction.OK {
		t.Fatalf("action plan failed: %+v", createAction.Error)
	}
	actionID := createAction.Data.(map[string]any)["action_id"].(string)
	actionApply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-action-apply", Operation: "action.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(createAction.Data.(map[string]any)["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-forget-action-apply"}
	if response := runRequest(t, dir, actionApply); !response.OK {
		t.Fatalf("action apply failed: %+v", response.Error)
	}

	forget := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-plan", Operation: "source.forget.plan", Actor: actor, Arguments: map[string]json.RawMessage{"source_ids": json.RawMessage(mustJSON([]string{sourceID}))}, IdempotencyKey: "idem-forget-plan"})
	if !forget.OK {
		t.Fatalf("source.forget.plan failed: %+v", forget.Error)
	}
	forgetData := forget.Data.(map[string]any)
	forgetPlanID := forgetData["plan_id"].(string)
	impact := forgetData["impact"].(map[string]any)
	if impact["sources"] != float64(1) || impact["articles"] != float64(1) || impact["actions"] != float64(1) || impact["raw_blobs_to_trash"] != float64(0) {
		t.Fatalf("forget impact = %+v", impact)
	}
	inspect := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-inspect", Operation: "plan.inspect", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(forgetPlanID))}})
	if !inspect.OK || inspect.Data.(map[string]any)["kind"] != "forget" {
		t.Fatalf("forget inspect = %+v error=%+v", inspect, inspect.Error)
	}
	missingConfirmation := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-missing-confirm", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(forgetPlanID)), "confirmed": json.RawMessage(`false`)}, IdempotencyKey: "idem-forget-missing-confirm"}
	if response := runRequest(t, dir, missingConfirmation); response.OK || response.Error == nil || response.Error.Code != "CONFIRMATION_REQUIRED" {
		t.Fatalf("forget confirmation gate = %+v", response)
	}
	applyForget := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(forgetPlanID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-forget-apply"}
	if response := runRequest(t, dir, applyForget); !response.OK {
		t.Fatalf("forget apply failed: %+v", response.Error)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-source-hidden", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}}); response.OK || response.Error == nil || response.Error.Code != "SOURCE_NOT_FOUND" {
		t.Fatalf("forgotten source still visible: %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-source-shared", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(secondSourceID))}}); !response.OK {
		t.Fatalf("shared source was hidden: %+v", response.Error)
	}
	if _, err := os.Stat(articlePath); !os.IsNotExist(err) {
		t.Fatalf("forgotten article remains at managed path: %v", err)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-article-hidden", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"slug": json.RawMessage(`"forget-target"`)}}); response.OK || response.Error == nil || response.Error.Code != "ARTICLE_NOT_FOUND" {
		t.Fatalf("forgotten article still visible: %+v", response)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-action-hidden", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"include_completed": json.RawMessage(`true`)}}); !response.OK || len(response.Data.(map[string]any)["actions"].([]any)) != 0 {
		t.Fatalf("forgotten action still visible: %+v", response)
	}

	undo := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-undo", Operation: "plan.undo", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(forgetPlanID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-forget-undo"}
	if response := runRequest(t, dir, undo); !response.OK {
		t.Fatalf("forget undo failed: %+v", response.Error)
	}
	if _, err := os.Stat(articlePath); err != nil {
		t.Fatalf("article was not restored: %v", err)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-article-restored", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}}); !response.OK || !strings.Contains(response.Data.(map[string]any)["content"].(string), "forgotten") {
		t.Fatalf("restored article = %+v", response)
	}
	query := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-action-restored", Operation: "action.query", Actor: actor, Arguments: map[string]json.RawMessage{"include_completed": json.RawMessage(`true`)}})
	if !query.OK || len(query.Data.(map[string]any)["actions"].([]any)) != 1 || query.Data.(map[string]any)["actions"].([]any)[0].(map[string]any)["action_id"] != actionID {
		t.Fatalf("restored action = %+v", query)
	}
	forgetAll := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-origin-plan", Operation: "source.forget.plan", Actor: actor, Arguments: map[string]json.RawMessage{"origin_contains": json.RawMessage(`"forget.md"`)}, IdempotencyKey: "idem-forget-origin-plan"})
	if !forgetAll.OK || forgetAll.Data.(map[string]any)["impact"].(map[string]any)["sources"] != float64(2) {
		t.Fatalf("origin selector plan = %+v", forgetAll)
	}
	forgetAllApply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-origin-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(forgetAll.Data.(map[string]any)["plan_id"].(string))), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-forget-origin-apply"}
	if response := runRequest(t, dir, forgetAllApply); !response.OK {
		t.Fatalf("origin selector apply = %+v", response.Error)
	}
	// Re-capturing identical bytes after the blob was moved to trash must create a new active Raw path.
	recaptured := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-recapture", Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(duplicateInput)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-forget-recapture"})
	if !recaptured.OK || recaptured.Data.(map[string]any)["duplicate"] != true {
		t.Fatalf("recapture after forget = %+v", recaptured)
	}
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-recapture-get", Operation: "source.get", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(recaptured.Data.(map[string]any)["source_id"].(string)))}}); !response.OK {
		t.Fatalf("recaptured source unavailable: %+v", response.Error)
	}
	audit := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-audit", Operation: "audit.query", Actor: actor, Arguments: map[string]json.RawMessage{"limit": json.RawMessage(`200`)}})
	if !audit.OK || audit.Data.(map[string]any)["count"].(float64) < 3 {
		t.Fatalf("audit query = %+v", audit)
	}
	invalidAudit := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-forget-audit-invalid", Operation: "audit.query", Actor: actor, Arguments: map[string]json.RawMessage{"limit": json.RawMessage(`201`)}})
	if invalidAudit.OK || invalidAudit.Error == nil || invalidAudit.Error.Code != "REQUEST_INVALID" {
		t.Fatalf("invalid audit limit = %+v", invalidAudit)
	}
}

func TestSafetyRollbackPlanDetectsDriftApplyAndUndo(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	_, _, firstPlan, articlePath, articleID := compilePreviewForTest(t, dir, actor, "rollback-v1", "Version one.", "", "rollback-target")
	applyFirst := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-v1-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(firstPlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-rollback-v1-apply"}
	if response := runRequest(t, dir, applyFirst); !response.OK {
		t.Fatalf("version one apply failed: %+v", response.Error)
	}
	_, _, secondPlan, _, _ := compilePreviewForTest(t, dir, actor, "rollback-v2", "Version two.", articleID, "rollback-target")
	applySecond := applyFirst
	applySecond.RequestID = "req-rollback-v2-apply"
	applySecond.IdempotencyKey = "idem-rollback-v2-apply"
	applySecond.Arguments = map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(secondPlan)), "confirmed": json.RawMessage(`true`)}
	if response := runRequest(t, dir, applySecond); !response.OK {
		t.Fatalf("version two apply failed: %+v", response.Error)
	}
	v2Content := mustReadFile(t, articlePath)
	rollback := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-plan", Operation: "knowledge.rollback.plan", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID)), "target_version": json.RawMessage(`1`)}, IdempotencyKey: "idem-rollback-plan"})
	if !rollback.OK {
		t.Fatalf("rollback plan failed: %+v", rollback.Error)
	}
	rollbackPlanID := rollback.Data.(map[string]any)["plan_id"].(string)
	if response := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-inspect", Operation: "plan.inspect", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(rollbackPlanID))}}); !response.OK || response.Data.(map[string]any)["kind"] != "rollback" {
		t.Fatalf("rollback inspect = %+v", response)
	}
	if err := os.WriteFile(articlePath, append(v2Content, []byte("manual drift\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	applyRollback := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-drift-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(rollbackPlanID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-rollback-drift-apply"}
	if response := runRequest(t, dir, applyRollback); response.OK || response.Error == nil || response.Error.Code != "WIKI_DRIFT" {
		t.Fatalf("rollback drift = %+v", response)
	}
	if err := os.WriteFile(articlePath, v2Content, 0600); err != nil {
		t.Fatal(err)
	}
	rollback = runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-plan-retry", Operation: "knowledge.rollback.plan", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID)), "target_version": json.RawMessage(`1`)}, IdempotencyKey: "idem-rollback-plan-retry"})
	if !rollback.OK {
		t.Fatalf("retry rollback plan failed: %+v", rollback.Error)
	}
	rollbackPlanID = rollback.Data.(map[string]any)["plan_id"].(string)
	applyRollback.RequestID = "req-rollback-apply"
	applyRollback.IdempotencyKey = "idem-rollback-apply"
	applyRollback.Arguments = map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(rollbackPlanID)), "confirmed": json.RawMessage(`true`)}
	if response := runRequest(t, dir, applyRollback); !response.OK {
		t.Fatalf("rollback apply failed: %+v", response.Error)
	}
	materialized := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-materialize", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}})
	if !materialized.OK || !strings.Contains(materialized.Data.(map[string]any)["content"].(string), "Version one.") {
		t.Fatalf("rollback materialized = %+v", materialized)
	}
	undo := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-undo", Operation: "plan.undo", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(rollbackPlanID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-rollback-undo"}
	if response := runRequest(t, dir, undo); !response.OK {
		t.Fatalf("rollback undo failed: %+v", response.Error)
	}
	if got := mustReadFile(t, articlePath); !bytes.Equal(got, v2Content) {
		t.Fatalf("rollback undo content = %q", got)
	}
}

func TestSafetyRollbackRestoresSensitivityMetadata(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	sourceID, _, compilePlan, _, articleID := compilePreviewForTest(t, dir, actor, "rollback-sensitivity", "Sensitive rollback body.", "", "rollback-sensitivity")
	applyCompile := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-sensitivity-v1", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(compilePlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-rollback-sensitivity-v1"}
	if response := runRequest(t, dir, applyCompile); !response.OK {
		t.Fatalf("sensitivity rollback v1 apply failed: %+v", response.Error)
	}
	mark := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-sensitivity-mark", Operation: "source.mark_sensitive", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID)), "sensitivity": json.RawMessage(`"restricted"`)}, IdempotencyKey: "idem-rollback-sensitivity-mark"}
	if response := runRequest(t, dir, mark); !response.OK {
		t.Fatalf("sensitivity rollback mark failed: %+v", response.Error)
	}
	rollback := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-sensitivity-plan", Operation: "knowledge.rollback.plan", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID)), "target_version": json.RawMessage(`1`)}, IdempotencyKey: "idem-rollback-sensitivity-plan"})
	if !rollback.OK {
		t.Fatalf("sensitivity rollback plan failed: %+v", rollback.Error)
	}
	planID := rollback.Data.(map[string]any)["plan_id"].(string)
	applyRollback := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-sensitivity-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-rollback-sensitivity-apply"}
	if response := runRequest(t, dir, applyRollback); !response.OK {
		t.Fatalf("sensitivity rollback apply failed: %+v", response.Error)
	}
	materialize := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-sensitivity-normal", Operation: "knowledge.materialize", Actor: actor, Arguments: map[string]json.RawMessage{"article_id": json.RawMessage(mustJSON(articleID))}}
	if response := runRequest(t, dir, materialize); !response.OK || response.Data.(map[string]any)["sensitivity"] != "normal" || response.Data.(map[string]any)["version"] != float64(1) {
		t.Fatalf("sensitivity rollback metadata = %+v", response)
	}
	undo := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-rollback-sensitivity-undo", Operation: "plan.undo", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(planID)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-rollback-sensitivity-undo"}
	if response := runRequest(t, dir, undo); !response.OK {
		t.Fatalf("sensitivity rollback undo failed: %+v", response.Error)
	}
	materialize.Options = map[string]json.RawMessage{"allow_sensitive": json.RawMessage(`true`)}
	materialize.RequestID = "req-rollback-sensitivity-restored"
	materialize.IdempotencyKey = "idem-rollback-sensitivity-restored"
	if response := runRequest(t, dir, materialize); !response.OK || response.Data.(map[string]any)["sensitivity"] != "restricted" || response.Data.(map[string]any)["version"] != float64(2) {
		t.Fatalf("sensitivity rollback undo metadata = %+v", response)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestPlanUpdateDetectsWikiDriftAndUndo(t *testing.T) {
	dir := t.TempDir()
	actor := protocol.Actor{Type: "claude-skill", SkillVersion: "0.1.0"}
	_, _, firstPlan, articlePath, articleID := compilePreviewForTest(t, dir, actor, "first", "Initial article body.", "", "update-target")
	firstApply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-update-first-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(firstPlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-update-first-apply"}
	if response := runRequest(t, dir, firstApply); !response.OK {
		t.Fatalf("first apply failed: %+v", response.Error)
	}
	firstContent, err := os.ReadFile(articlePath)
	if err != nil {
		t.Fatal(err)
	}

	_, _, driftPlan, _, _ := compilePreviewForTest(t, dir, actor, "drift", "Updated article body.", articleID, "update-target")
	if err := os.WriteFile(articlePath, append(firstContent, []byte("manual edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	driftApply := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-update-drift-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(driftPlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-update-drift-apply"}
	driftResponse := runRequest(t, dir, driftApply)
	if driftResponse.OK || driftResponse.Error == nil || driftResponse.Error.Code != "WIKI_DRIFT" {
		t.Fatalf("drift response = %+v", driftResponse)
	}
	if got, err := os.ReadFile(articlePath); err != nil || !strings.HasSuffix(string(got), "manual edit\n") {
		t.Fatalf("drift apply changed article: %v %q", err, got)
	}

	if err := os.WriteFile(articlePath, firstContent, 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "data", "staging", "recovery.marker")
	if err := os.WriteFile(marker, []byte(`{"plan_id":"orphan"}`), 0600); err != nil {
		t.Fatal(err)
	}
	blockedApply := driftApply
	blockedApply.RequestID = "req-update-recovery-block"
	blockedApply.IdempotencyKey = "idem-update-recovery-block"
	blocked := runRequest(t, dir, blockedApply)
	if blocked.OK || blocked.Error == nil || blocked.Error.Code != "STORAGE_UNHEALTHY" {
		t.Fatalf("recovery marker did not block apply: %+v", blocked)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	applyDriftPlan := driftApply
	applyDriftPlan.RequestID = "req-update-apply-after-restore"
	applyDriftPlan.IdempotencyKey = "idem-update-apply-after-restore"
	if response := runRequest(t, dir, applyDriftPlan); !response.OK {
		t.Fatalf("restored apply failed: %+v", response.Error)
	}
	updatedContent, err := os.ReadFile(articlePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedContent), "Updated article body.") {
		t.Fatalf("updated content missing: %s", updatedContent)
	}

	undo := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-update-undo", Operation: "plan.undo", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(driftPlan)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-update-undo"}
	if response := runRequest(t, dir, undo); !response.OK {
		t.Fatalf("update undo failed: %+v", response.Error)
	}
	if got, err := os.ReadFile(articlePath); err != nil || string(got) != string(firstContent) {
		t.Fatalf("undo did not restore prior content: %v %q", err, got)
	}

	// Two previews share the same base version; only the first one may apply.
	_, _, staleA, _, _ := compilePreviewForTest(t, dir, actor, "stale-a", "Stale winner.", articleID, "update-target")
	_, _, staleB, _, _ := compilePreviewForTest(t, dir, actor, "stale-b", "Stale loser.", articleID, "update-target")
	applyA := protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-stale-a-apply", Operation: "plan.apply", Actor: actor, Arguments: map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(staleA)), "confirmed": json.RawMessage(`true`)}, IdempotencyKey: "idem-stale-a-apply"}
	if response := runRequest(t, dir, applyA); !response.OK {
		t.Fatalf("stale winner apply failed: %+v", response.Error)
	}
	applyB := applyA
	applyB.RequestID = "req-stale-b-apply"
	applyB.IdempotencyKey = "idem-stale-b-apply"
	applyB.Arguments = map[string]json.RawMessage{"plan_id": json.RawMessage(mustJSON(staleB)), "confirmed": json.RawMessage(`true`)}
	staleResponse := runRequest(t, dir, applyB)
	if staleResponse.OK || staleResponse.Error == nil || staleResponse.Error.Code != "PLAN_STALE" {
		t.Fatalf("stale loser response = %+v", staleResponse)
	}
}

func compilePreviewForTest(t *testing.T, dir string, actor protocol.Actor, suffix, body, articleID, slug string) (sourceID, jobID, planID, articlePath, resolvedArticleID string) {
	t.Helper()
	input := filepath.Join(dir, suffix+".md")
	if err := os.WriteFile(input, []byte("Source for "+suffix), 0600); err != nil {
		t.Fatal(err)
	}
	ingest := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-helper-ingest-" + suffix, Operation: "source.ingest", Actor: actor, Arguments: map[string]json.RawMessage{"input_file": json.RawMessage(mustJSON(input)), "source_type": json.RawMessage(`"markdown"`), "sensitivity": json.RawMessage(`"normal"`)}, IdempotencyKey: "idem-helper-ingest-" + suffix})
	if !ingest.OK {
		t.Fatalf("helper ingest failed: %+v", ingest.Error)
	}
	sourceID = ingest.Data.(map[string]any)["source_id"].(string)
	start := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-helper-start-" + suffix, Operation: "compile.start", Actor: actor, Arguments: map[string]json.RawMessage{"source_id": json.RawMessage(mustJSON(sourceID))}, IdempotencyKey: "idem-helper-start-" + suffix})
	if !start.OK {
		t.Fatalf("helper start failed: %+v", start.Error)
	}
	jobID = start.Data.(map[string]any)["job_id"].(string)
	for _, stage := range []string{"extract", "classify", "write"} {
		next := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-helper-next-" + suffix + "-" + stage, Operation: "compile.next", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}})
		if !next.OK {
			t.Fatalf("helper next failed: %+v", next.Error)
		}
		stageData := next.Data.(map[string]any)
		var result any
		switch stage {
		case "extract":
			result = map[string]any{"facts": []any{}, "decisions": []any{}, "preferences": []any{}, "projects": []any{}, "people": []any{}, "relationships": []any{}, "actions": []any{}, "conflicts": []any{}, "citations": []any{map[string]any{"source_id": sourceID, "locator": "source"}}}
		case "classify":
			result = map[string]any{"categories": []any{}, "outline": []any{}}
		case "write":
			write := map[string]any{"title": "Update target", "slug": slug, "summary": "A versioned article.", "body": body, "sensitivity": "normal", "tags": []string{"test"}, "source_ids": []string{sourceID}, "citations": []any{map[string]any{"source_id": sourceID, "locator": "source"}}}
			if articleID != "" {
				write["article_id"] = articleID
			}
			result = write
		}
		payload, _ := json.Marshal(result)
		resultFile := stageData["result_file"].(string)
		if err := os.WriteFile(resultFile, payload, 0600); err != nil {
			t.Fatal(err)
		}
		submit := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-helper-submit-" + suffix + "-" + stage, Operation: "compile.submit", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID)), "stage": json.RawMessage(mustJSON(stage)), "result_file": json.RawMessage(mustJSON(resultFile))}, IdempotencyKey: "idem-helper-submit-" + suffix + "-" + stage})
		if !submit.OK {
			t.Fatalf("helper submit %s failed: %+v", stage, submit.Error)
		}
	}
	preview := runRequest(t, dir, protocol.Request{ProtocolVersion: protocol.SupportedVersion, RequestID: "req-helper-preview-" + suffix, Operation: "compile.preview", Actor: actor, Arguments: map[string]json.RawMessage{"job_id": json.RawMessage(mustJSON(jobID))}, IdempotencyKey: "idem-helper-preview-" + suffix})
	if !preview.OK {
		t.Fatalf("helper preview failed: %+v", preview.Error)
	}
	data := preview.Data.(map[string]any)
	planID = data["plan_id"].(string)
	articlePath = data["path"].(string)
	resolvedArticleID = data["article_id"].(string)
	return
}

func runRequest(t *testing.T, dir string, request protocol.Request) protocol.Response {
	t.Helper()
	t.Setenv("MOSS_DATA_DIR", filepath.Join(dir, "data"))
	b, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunCallStdio(bytes.NewReader(b), &stdout, &stderr); code != ExitOK {
		t.Fatalf("stdio exit=%d stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stdio stderr=%s", stderr.String())
	}
	var response protocol.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdio response=%q: %v", stdout.String(), err)
	}
	return response
}

func mustJSON(value any) string {
	b, _ := json.Marshal(value)
	return string(b)
}
