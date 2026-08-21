## 1. Protocol and schema extension

- [x] 1.1 Add compile, plan, article, stage, and risk response types plus strict operation argument decoding.
- [x] 1.2 Extend mutating-operation detection and capability discovery for compile and plan operations.
- [x] 1.3 Add embedded extract, classify, and write JSON Schemas and bounded schema validation helpers.

## 2. Storage migration and job model

- [x] 2.1 Add additive SQLite migrations for compile jobs, stages, articles, article versions, citations, plans, and plan items.
- [x] 2.2 Add job directory creation, source snapshot copying, stage file references, and managed path checks.
- [x] 2.3 Implement compile job creation, state transitions, stage hashes, and idempotent transition persistence.

## 3. Compile operations

- [x] 3.1 Implement `compile.start` with single-source validation, job initialization, and extract stage response.
- [x] 3.2 Implement `compile.next` and `compile.status` with deterministic stage and job metadata.
- [x] 3.3 Implement schema/citation/sensitivity validation for submitted result files.
- [x] 3.4 Implement `compile.submit` for extract, classify, and write transitions with duplicate-submission handling.
- [x] 3.5 Implement `compile.abort` and rejected/failed job behavior.

## 4. Managed Wiki and plans

- [x] 4.1 Implement deterministic article slugging, frontmatter rendering, article version hashing, and citation persistence.
- [x] 4.2 Implement Wiki drift detection and current-version checks.
- [x] 4.3 Implement `compile.preview` with diff metadata, risk flags, base versions, expiry, and pending plan creation.
- [x] 4.4 Implement `plan.inspect` and `plan.apply` with optimistic concurrency, atomic staged Markdown writes, and audit events.
- [x] 4.5 Implement `plan.undo` with version matching, managed trash for new articles, and idempotent results.

## 5. Skill workflow

- [x] 5.1 Extend the Cairn Skill with compile routing, stage file handling, resume behavior, and confirmation policy.
- [x] 5.2 Add Skill fixture checks for ordered stages, no direct Wiki writes, high-impact confirmation, and source prompt-injection handling.

## 6. Tests and validation

- [x] 6.1 Add schema and stage transition tests for valid, malformed, out-of-order, duplicate, and oversized submissions.
- [x] 6.2 Add plan/Wiki tests for new articles, updates, deterministic Markdown, citations, conflicts, stale versions, drift, apply, undo, and recovery markers.
- [x] 6.3 Add an end-to-end test from source to compile job to preview to applied Markdown and SQLite version.
- [x] 6.4 Run `go test ./...`, `go test -race ./...`, `go vet ./...`, OpenSpec strict validation, and whitespace checks.
- [x] 6.5 Review every scenario in the four specs, complete verification, and archive the change only after all tasks pass.
