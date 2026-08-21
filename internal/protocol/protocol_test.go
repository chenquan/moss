package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeRequestRejectsUnknownTopLevelField(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"protocol_version":"1.0","request_id":"r","operation":"system.health","actor":{"type":"claude-skill","skill_version":"0.1.0"},"arguments":{},"unexpected":true}`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if got := err.(*CodedError).Code; got != "REQUEST_INVALID" {
		t.Fatalf("code = %s", got)
	}
}

func TestFingerprintStableForEquivalentMaps(t *testing.T) {
	one := Request{Operation: "source.ingest", Arguments: map[string]json.RawMessage{"b": json.RawMessage(`2`), "a": json.RawMessage(`1`)}}
	two := Request{Operation: "source.ingest", Arguments: map[string]json.RawMessage{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`)}}
	a, err := one.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	b, err := two.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("fingerprints differ: %s != %s", a, b)
	}
}

func TestWriteResponseAtomicallyCreatesPrivateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "response.json")
	if err := WriteResponse(path, Response{ProtocolVersion: SupportedVersion, RequestID: "r", OK: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("permissions = %o", got)
	}
	if entries, err := os.ReadDir(dir); err != nil {
		t.Fatal(err)
	} else if len(entries) != 1 {
		t.Fatalf("temporary response files remain: %d", len(entries))
	}
}

func TestValidateResponseRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	request := filepath.Join(dir, "request.json")
	response := filepath.Join(dir, "response.json")
	if err := os.WriteFile(request, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.json"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.json", response); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResponsePath(response, request, filepath.Join(dir, "data")); err == nil || err.(*CodedError).Code != "PATH_INVALID" {
		t.Fatalf("expected PATH_INVALID, got %v", err)
	}
}

func TestBackupOperationsAreMachineCapabilities(t *testing.T) {
	if !IsMutating("system.export") || !IsMutating("system.restore") {
		t.Fatal("backup operations must be classified as mutating")
	}
	capabilities := SupportedCapabilities()
	seen := map[string]bool{}
	for _, capability := range capabilities {
		seen[capability.Operation] = capability.Mutating
	}
	if !seen["system.export"] || !seen["system.restore"] {
		t.Fatalf("backup capabilities missing or non-mutating: %#v", seen)
	}
}

func TestCompileApplyIsMutatingCapability(t *testing.T) {
	if !IsMutating("compile.apply") {
		t.Fatal("compile.apply must be classified as mutating")
	}
	request := Request{
		ProtocolVersion: SupportedVersion,
		RequestID:       "req-compile-apply-no-idempotency",
		Operation:       "compile.apply",
		Actor:           Actor{Type: "claude-skill", SkillVersion: "0.1.0"},
		Arguments:       map[string]json.RawMessage{},
	}
	if err := request.ValidateBasic(); err == nil || err.Code != "IDEMPOTENCY_REQUIRED" {
		t.Fatalf("missing idempotency key = %+v", err)
	}
	seen := map[string]Capability{}
	for _, capability := range SupportedCapabilities() {
		seen[capability.Operation] = capability
	}
	capability, ok := seen["compile.apply"]
	if !ok || !capability.Mutating {
		t.Fatalf("compile.apply capability = %+v, present=%v", capability, ok)
	}
}
