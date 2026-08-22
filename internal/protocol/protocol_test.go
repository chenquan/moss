package protocol

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestReadRequestRejectsTrailingDocumentsAndOversizedInput(t *testing.T) {
	if _, err := ReadRequest(bytes.NewBufferString(`{"protocol_version":"1.0"}{}`)); err == nil || err.(*CodedError).Code != "REQUEST_INVALID" {
		t.Fatalf("trailing document error = %v", err)
	}
	over := bytes.Repeat([]byte("x"), MaxRequestBytes+1)
	if _, err := ReadRequest(bytes.NewReader(over)); err == nil || err.(*CodedError).Code != "REQUEST_TOO_LARGE" {
		t.Fatalf("oversized stream error = %v", err)
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

func TestWriteResponseStreamWritesOneJSONDocument(t *testing.T) {
	var output bytes.Buffer
	response := Response{ProtocolVersion: SupportedVersion, RequestID: "stream-1", OK: true}
	if err := WriteResponseStream(&output, response); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 || output.Bytes()[output.Len()-1] != '\n' {
		t.Fatalf("stream output must end with newline: %q", output.String())
	}
	var decoded Response
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("stream output is not JSON: %v", err)
	}
	if decoded.RequestID != response.RequestID || !decoded.OK {
		t.Fatalf("decoded response = %+v", decoded)
	}
}

func TestWriteResponseStreamPropagatesShortWriter(t *testing.T) {
	if err := WriteResponseStream(shortWriter{}, Response{ProtocolVersion: SupportedVersion, RequestID: "stream-short", OK: true}); err != io.ErrShortWrite {
		t.Fatalf("short writer error = %v", err)
	}
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

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
