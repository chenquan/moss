## 1. Storage and protocol foundation

- [x] 1.1 Add schema-v6 additive migrations for typed relations, action results, compile relation staging, result plans, and nullable fact review deadlines.
- [x] 1.2 Add relation endpoint/type validation, sensitivity resolution, forgotten-state filtering, stable errors, and storage helpers.
- [x] 1.3 Define request/response models and bounded JSON schemas for relation entries, action results, review scans, and context bundles.
- [x] 1.4 Register new operations and mutation classifications in protocol validation, dispatch, and capability discovery without changing existing operation semantics.

## 2. Compile relationship integration

- [x] 2.1 Extend multi-source write validation to accept bounded relations and optional fact review deadlines.
- [x] 2.2 Persist relation entries in compile preview plans with deterministic diffs, risk flags, and source-set validation.
- [x] 2.3 Apply articles, facts, review deadlines, and relations atomically with optimistic version checks and audit/idempotency.
- [x] 2.4 Extend compile undo and forget impact handling to remove or hide only affected relation records safely.
- [x] 2.5 Add compile relation unit and integration tests for duplicate, self, stale, sensitive, atomicity, retry, and undo scenarios.

## 3. Action-result workflow

- [x] 3.1 Implement `action.result.plan` with action revision checks, bounded result fields, source sensitivity validation, diff/risk output, and expiry.
- [x] 3.2 Implement `action.result.apply` with confirmation, idempotency, result persistence, generated `produces` relation, and audit.
- [x] 3.3 Ensure result application never changes action status or facts/articles; add result query helpers for review/context assembly.
- [x] 3.4 Add action-result tests for plan/apply, stale/expired/denied cases, retries, result versions, and unchanged action lifecycle.

## 4. Review scan and context bundle

- [x] 4.1 Implement deterministic `knowledge.review.scan` for lifecycle, evidence, drift, explicit conflict, expiration, missing-result, and feedback-gap signals.
- [x] 4.2 Implement bounded `knowledge.context.bundle` with metadata-first article references, facts, relations, citations, actions, results, review signals, and truncation metadata.
- [x] 4.3 Apply independent sensitivity/forgotten filtering and managed hash checks to both read operations.
- [x] 4.4 Add review/context tests for ordering, limits, empty results, contradiction non-inference, drift, stale evidence, missing results, and privacy.

## 5. Skill, documentation, and verification

- [x] 5.1 Update bundled Skill workflows, protocol guide, operation guide, confirmation guidance, and response routing for all new operations.
- [x] 5.2 Update README operation matrix, data model/product boundary, action-result behavior, relation semantics, and context drill-down guidance.
- [x] 5.3 Build and validate a current OpenSpec change with strict validation and add any required main-spec synchronization notes.
- [x] 5.4 Run `go test ./...`, `go vet ./...`, `git diff --check`, and an end-to-end stdio capability/read/write smoke test; resolve all failures.
