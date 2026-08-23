package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chenquan/moss/internal/action"
	"github.com/chenquan/moss/internal/compile"
	"github.com/chenquan/moss/internal/knowledge"
	"github.com/chenquan/moss/internal/protocol"
	"github.com/chenquan/moss/internal/safety"
	"github.com/chenquan/moss/internal/source"
	"github.com/chenquan/moss/internal/storage"
	"github.com/chenquan/moss/internal/system"
)

const (
	ExitOK        = 0
	ExitTransport = 1
	ExitUsage     = 2
)

// RunCallStdio executes one machine request using stdin/stdout. Business
// errors are represented by a structured response and therefore still return
// ExitOK; a non-zero code is reserved for transport failures that prevent a
// response from being delivered.
func RunCallStdio(stdin io.Reader, stdout, stderr io.Writer) int {
	req, decodeErr := protocol.ReadRequest(stdin)
	response := executeRequest(context.Background(), req, decodeErr)
	if err := protocol.WriteResponseStream(stdout, response); err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return ExitTransport
	}
	return ExitOK
}

func executeRequest(ctx context.Context, req protocol.Request, decodeErr error) protocol.Response {
	if decodeErr != nil {
		return protocol.NewErrorResponse(protocol.Request{}, asCodedError(decodeErr))
	}
	if codedErr := req.ValidateBasic(); codedErr != nil {
		return protocol.NewErrorResponse(req, codedErr)
	}
	if req.ProtocolVersion != protocol.SupportedVersion {
		return protocol.NewErrorResponse(req, protocol.NewCodedError("PROTOCOL_VERSION_UNSUPPORTED", "request protocol version is not supported", false, map[string]any{"supported": []string{protocol.SupportedVersion}}))
	}

	response, codedErr := dispatch(ctx, req)
	if codedErr != nil {
		return protocol.NewErrorResponse(req, codedErr)
	}
	return response
}

func dispatch(ctx context.Context, req protocol.Request) (protocol.Response, *protocol.CodedError) {
	switch req.Operation {
	case "system.handshake":
		data, codedErr := system.Handshake(req)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		return protocol.NewSuccessResponse(req, data), nil
	case "system.capabilities":
		return protocol.NewSuccessResponse(req, system.Capabilities()), nil
	case "system.health":
		store, codedErr := openStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		data, healthErr := system.Health(ctx, store)
		if healthErr != nil {
			return protocol.Response{ProtocolVersion: protocol.SupportedVersion, RequestID: req.RequestID, OK: false, Data: data, Error: &protocol.Error{Code: healthErr.Code, Message: healthErr.Message, Retryable: healthErr.Retryable, Details: healthErr.Details}}, healthErr
		}
		return protocol.NewSuccessResponse(req, data), nil
	case "system.recover":
		store, codedErr := openStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		if codedErr := store.AcquireMutationLock(ctx); codedErr != nil {
			return protocol.Response{}, protocol.NewCodedError("STORAGE_UNHEALTHY", "Moss mutation lock could not be acquired", true, nil)
		}
		return system.Recover(ctx, store, req)
	case "system.export", "system.restore":
		store, codedErr := openMutableStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		if req.Operation == "system.export" {
			return system.Export(ctx, store, req)
		}
		return system.Restore(ctx, store, req)
	case "source.ingest", "source.mark_sensitive", "source.get", "source.list":
		var store *storage.Storage
		var codedErr *protocol.CodedError
		if req.Operation == "source.ingest" || req.Operation == "source.mark_sensitive" {
			store, codedErr = openMutableStorage(ctx)
		} else {
			store, codedErr = openStorage(ctx)
		}
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		switch req.Operation {
		case "source.ingest":
			return source.Ingest(ctx, store, req)
		case "source.mark_sensitive":
			return source.MarkSensitive(ctx, store, req)
		case "source.get":
			data, err := source.Get(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		default:
			data, err := source.List(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		}
	case "compile.start", "compile.submit", "compile.preview", "compile.apply", "compile.abort", "plan.apply", "plan.undo", "source.forget.plan", "knowledge.rollback.plan":
		store, codedErr := openMutableStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		switch req.Operation {
		case "compile.start":
			return compile.Start(ctx, store, req)
		case "compile.submit":
			return compile.Submit(ctx, store, req)
		case "compile.preview":
			return compile.Preview(ctx, store, req)
		case "compile.apply":
			return compile.Apply(ctx, store, req)
		case "compile.abort":
			return compile.Abort(ctx, store, req)
		case "source.forget.plan":
			return safety.ForgetPlan(ctx, store, req)
		case "knowledge.rollback.plan":
			return safety.RollbackPlan(ctx, store, req)
		case "plan.apply":
			response, err := compile.Apply(ctx, store, req)
			if err == nil || err.Code != "PLAN_NOT_FOUND" {
				return response, err
			}
			return safety.Apply(ctx, store, req)
		default:
			response, err := compile.Undo(ctx, store, req)
			if err == nil || err.Code != "PLAN_NOT_FOUND" {
				return response, err
			}
			return safety.Undo(ctx, store, req)
		}
	case "compile.next", "compile.status", "plan.inspect", "audit.query":
		store, codedErr := openStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		switch req.Operation {
		case "compile.next":
			data, err := compile.Next(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "compile.status":
			data, err := compile.Status(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "audit.query":
			data, err := safety.AuditQuery(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		default:
			data, err := compile.Inspect(ctx, store, req)
			if err != nil {
				if err.Code != "PLAN_NOT_FOUND" {
					return protocol.Response{}, err
				}
				data, safetyErr := safety.Inspect(ctx, store, req)
				if safetyErr != nil {
					return protocol.Response{}, safetyErr
				}
				return protocol.NewSuccessResponse(req, data), nil
			}
			return protocol.NewSuccessResponse(req, data), nil
		}
	case "knowledge.catalog", "knowledge.candidates", "knowledge.insights", "knowledge.materialize", "knowledge.history", "knowledge.reindex", "knowledge.backfill.plan":
		var store *storage.Storage
		var codedErr *protocol.CodedError
		if req.Operation == "knowledge.reindex" || req.Operation == "knowledge.backfill.plan" {
			store, codedErr = openMutableStorage(ctx)
		} else {
			store, codedErr = openStorage(ctx)
		}
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		switch req.Operation {
		case "knowledge.catalog":
			data, err := knowledge.Catalog(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "knowledge.candidates":
			data, err := knowledge.Candidates(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "knowledge.insights":
			data, err := knowledge.Insights(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "knowledge.materialize":
			data, err := knowledge.Materialize(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "knowledge.reindex":
			data, err := knowledge.Reindex(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		case "knowledge.backfill.plan":
			data, err := knowledge.BackfillPlan(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		default:
			data, err := knowledge.History(ctx, store, req)
			if err != nil {
				return protocol.Response{}, err
			}
			return protocol.NewSuccessResponse(req, data), nil
		}
	case "action.create.plan", "action.update.plan", "action.apply":
		store, codedErr := openMutableStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		switch req.Operation {
		case "action.create.plan":
			return action.CreatePlan(ctx, store, req)
		case "action.update.plan":
			return action.UpdatePlan(ctx, store, req)
		default:
			return action.Apply(ctx, store, req)
		}
	case "action.query":
		store, codedErr := openStorage(ctx)
		if codedErr != nil {
			return protocol.Response{}, codedErr
		}
		defer store.Close()
		data, err := action.Query(ctx, store, req)
		if err != nil {
			return protocol.Response{}, err
		}
		return protocol.NewSuccessResponse(req, data), nil
	default:
		return protocol.Response{}, protocol.NewCodedError("OPERATION_UNSUPPORTED", "operation is not supported by this CLI", false, map[string]any{"operation": req.Operation})
	}
}

func openStorage(ctx context.Context) (*storage.Storage, *protocol.CodedError) {
	store, err := storage.Open(ctx)
	if err != nil {
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "Moss storage could not be opened", true, nil)
	}
	return store, nil
}

func openMutableStorage(ctx context.Context) (*storage.Storage, *protocol.CodedError) {
	store, codedErr := openStorage(ctx)
	if codedErr != nil {
		return nil, codedErr
	}
	if health, err := store.Health(ctx); err != nil {
		_ = store.Close()
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "Moss health checks could not complete", true, nil)
	} else if health.Overall != "healthy" {
		_ = store.Close()
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "mutating operations are unavailable until Moss storage is healthy", true, health)
	}
	if err := store.AcquireMutationLock(ctx); err != nil {
		_ = store.Close()
		return nil, protocol.NewCodedError("STORAGE_UNHEALTHY", "Moss mutation lock could not be acquired", true, nil)
	}
	return store, nil
}

func asCodedError(err error) *protocol.CodedError {
	var coded *protocol.CodedError
	if errors.As(err, &coded) {
		return coded
	}
	return protocol.NewCodedError("INTERNAL_ERROR", strings.TrimSpace(err.Error()), false, nil)
}

func MarshalForTest(value any) []byte {
	b, _ := json.Marshal(value)
	return b
}
