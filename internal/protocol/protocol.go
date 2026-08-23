package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	SupportedVersion = "1.0"
	MaxRequestBytes  = 2 << 20
	MaxResponseBytes = 8 << 20
)

type Actor struct {
	Type         string `json:"type"`
	SkillVersion string `json:"skill_version"`
}

type Request struct {
	ProtocolVersion string                     `json:"protocol_version"`
	RequestID       string                     `json:"request_id"`
	IdempotencyKey  string                     `json:"idempotency_key,omitempty"`
	Operation       string                     `json:"operation"`
	Actor           Actor                      `json:"actor"`
	Arguments       map[string]json.RawMessage `json:"arguments"`
	Options         map[string]json.RawMessage `json:"options,omitempty"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
}

type Response struct {
	ProtocolVersion string    `json:"protocol_version"`
	RequestID       string    `json:"request_id"`
	OK              bool      `json:"ok"`
	Data            any       `json:"data,omitempty"`
	Warnings        []Warning `json:"warnings,omitempty"`
	Next            any       `json:"next,omitempty"`
	Error           *Error    `json:"error,omitempty"`
}

type Capability struct {
	Operation   string `json:"operation"`
	Mutating    bool   `json:"mutating"`
	Description string `json:"description"`
}

type CodedError struct {
	Code      string
	Message   string
	Retryable bool
	Details   any
}

func (e *CodedError) Error() string { return e.Code + ": " + e.Message }

func NewCodedError(code, message string, retryable bool, details any) *CodedError {
	return &CodedError{Code: code, Message: message, Retryable: retryable, Details: details}
}

func NewErrorResponse(req Request, err *CodedError) Response {
	return Response{
		ProtocolVersion: SupportedVersion,
		RequestID:       req.RequestID,
		OK:              false,
		Error: &Error{
			Code:      err.Code,
			Message:   err.Message,
			Retryable: err.Retryable,
			Details:   err.Details,
		},
	}
}

func NewSuccessResponse(req Request, data any) Response {
	return Response{ProtocolVersion: SupportedVersion, RequestID: req.RequestID, OK: true, Data: data}
}

func DecodeRequest(data []byte) (Request, error) {
	if len(data) > MaxRequestBytes {
		return Request{}, NewCodedError("REQUEST_TOO_LARGE", "request exceeds the maximum size", false, nil)
	}
	var req Request
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return Request{}, NewCodedError("REQUEST_INVALID", "request is not valid JSON", false, err.Error())
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Request{}, NewCodedError("REQUEST_INVALID", "request must contain exactly one JSON document", false, nil)
	}
	return req, nil
}

// ReadRequest reads one bounded JSON request document from a stream. The
// stream transport is deliberately strict: a caller cannot smuggle a second
// document after the first one, and oversized input is rejected before any
// operation validation or dispatch occurs.
func ReadRequest(r io.Reader) (Request, error) {
	if r == nil {
		return Request{}, NewCodedError("REQUEST_INVALID", "request stream is unavailable", false, nil)
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxRequestBytes+1))
	if err != nil {
		return Request{}, NewCodedError("REQUEST_INVALID", "request stream could not be read", true, err.Error())
	}
	return DecodeRequest(data)
}

func (r Request) ValidateBasic() *CodedError {
	if strings.TrimSpace(r.ProtocolVersion) == "" || strings.TrimSpace(r.RequestID) == "" || strings.TrimSpace(r.Operation) == "" {
		return NewCodedError("REQUEST_INVALID", "protocol_version, request_id, and operation are required", false, nil)
	}
	if r.Actor.Type == "" || r.Actor.SkillVersion == "" {
		return NewCodedError("REQUEST_INVALID", "actor.type and actor.skill_version are required", false, nil)
	}
	if r.Arguments == nil {
		return NewCodedError("REQUEST_INVALID", "arguments must be a JSON object", false, nil)
	}
	if IsMutating(r.Operation) && strings.TrimSpace(r.IdempotencyKey) == "" {
		return NewCodedError("IDEMPOTENCY_REQUIRED", "mutating operations require idempotency_key", false, nil)
	}
	return nil
}

func (r Request) Fingerprint() (string, error) {
	payload := struct {
		Operation string                     `json:"operation"`
		Arguments map[string]json.RawMessage `json:"arguments"`
		Options   map[string]json.RawMessage `json:"options,omitempty"`
	}{r.Operation, r.Arguments, r.Options}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

func DecodeArguments[T any](r Request) (T, *CodedError) {
	var value T
	b, err := json.Marshal(r.Arguments)
	if err != nil {
		return value, NewCodedError("REQUEST_INVALID", "arguments cannot be encoded", false, nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, NewCodedError("REQUEST_INVALID", "operation arguments are invalid", false, err.Error())
	}
	return value, nil
}

func OptionBool(r Request, key string) (bool, *CodedError) {
	raw, ok := r.Options[key]
	if !ok {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, NewCodedError("REQUEST_INVALID", fmt.Sprintf("option %q must be boolean", key), false, nil)
	}
	return value, nil
}

func IsMutating(operation string) bool {
	switch operation {
	case "source.ingest", "source.mark_sensitive", "compile.start", "compile.submit", "compile.preview", "compile.apply", "compile.abort", "plan.apply", "plan.undo", "action.create.plan", "action.update.plan", "action.apply", "action.result.plan", "action.result.apply", "source.forget.plan", "knowledge.rollback.plan", "knowledge.reindex", "knowledge.backfill.plan", "system.export", "system.restore", "system.recover":
		return true
	default:
		return false
	}
}

func SupportedCapabilities() []Capability {
	return []Capability{
		{Operation: "compile.abort", Mutating: true, Description: "Abort a compile job"},
		{Operation: "compile.apply", Mutating: true, Description: "Apply a confirmed compile knowledge plan"},
		{Operation: "compile.next", Description: "Read the next compile stage"},
		{Operation: "compile.preview", Mutating: true, Description: "Create a knowledge change preview"},
		{Operation: "compile.start", Mutating: true, Description: "Start a staged source compilation job"},
		{Operation: "compile.status", Description: "Read compile job status"},
		{Operation: "compile.submit", Mutating: true, Description: "Submit a validated compile stage result"},
		{Operation: "system.capabilities", Description: "List supported Moss operations"},
		{Operation: "system.export", Mutating: true, Description: "Create a private local backup archive"},
		{Operation: "system.handshake", Description: "Check Skill and CLI protocol compatibility"},
		{Operation: "system.health", Description: "Validate local Moss storage and runtime health"},
		{Operation: "system.restore", Mutating: true, Description: "Restore a confirmed local backup archive"},
		{Operation: "system.recover", Mutating: true, Description: "Resolve an interrupted managed-file and SQLite mutation"},
		{Operation: "plan.apply", Mutating: true, Description: "Apply a reviewed knowledge plan"},
		{Operation: "plan.inspect", Description: "Inspect a knowledge plan"},
		{Operation: "plan.undo", Mutating: true, Description: "Undo an applied knowledge plan"},
		{Operation: "knowledge.catalog", Description: "List the managed knowledge catalogue"},
		{Operation: "knowledge.candidates", Description: "Find local knowledge candidates"},
		{Operation: "knowledge.history", Description: "Read managed article history"},
		{Operation: "knowledge.insights", Description: "Read deterministic decision review insights"},
		{Operation: "knowledge.review.scan", Description: "Scan explicit knowledge and action follow-up review signals"},
		{Operation: "knowledge.context.bundle", Description: "Assemble a bounded metadata-first evidence context"},
		{Operation: "knowledge.materialize", Description: "Read a verified managed article"},
		{Operation: "knowledge.reindex", Mutating: true, Description: "Rebuild the local article search index without model calls"},
		{Operation: "knowledge.backfill.plan", Mutating: true, Description: "Create an explicit legacy backfill manifest; model compilation remains Skill-driven"},
		{Operation: "action.apply", Mutating: true, Description: "Apply a confirmed action plan"},
		{Operation: "action.create.plan", Mutating: true, Description: "Create a pending action plan"},
		{Operation: "action.query", Description: "Query today's and waiting actions"},
		{Operation: "action.update.plan", Mutating: true, Description: "Create a pending action update plan"},
		{Operation: "action.result.apply", Mutating: true, Description: "Apply a confirmed action result"},
		{Operation: "action.result.plan", Mutating: true, Description: "Create a pending action result plan"},
		{Operation: "source.forget.plan", Mutating: true, Description: "Preview forgetting local source information"},
		{Operation: "knowledge.rollback.plan", Mutating: true, Description: "Preview a managed article rollback"},
		{Operation: "audit.query", Description: "Query local audit history"},
		{Operation: "source.mark_sensitive", Mutating: true, Description: "Update an active source sensitivity classification"},
		{Operation: "source.get", Description: "Read source metadata"},
		{Operation: "source.ingest", Mutating: true, Description: "Copy and register an immutable local source"},
		{Operation: "source.list", Description: "List permitted source metadata"},
	}
}

func MarshalResponse(response Response) ([]byte, error) {
	b, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxResponseBytes {
		return nil, NewCodedError("RESPONSE_TOO_LARGE", "response exceeds the maximum size", false, nil)
	}
	return b, nil
}

// WriteResponseStream writes exactly one response document to a business
// output stream. The newline makes the response frame unambiguous for shell
// callers while keeping stdout free of any diagnostics or formatting.
func WriteResponseStream(w io.Writer, response Response) error {
	b, err := MarshalResponse(response)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAll(w, b)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
