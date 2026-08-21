## 1. Protocol and storage migration

- [x] 1.1 Add action request/response models, strict argument validation, mutating-operation detection, capability entries, and app dispatch.
- [x] 1.2 Bump additive SQLite migration to schema version 3 and create actions, action plans, indexes, and source foreign-key fields.
- [x] 1.3 Add bounded validation for kinds, statuses, title/details, due timestamps, waiting-for, sensitivity, and source IDs.

## 2. Action plans and application

- [x] 2.1 Implement `action.create.plan` with proposed action snapshots, risk flags, expiry, idempotency, and audit.
- [x] 2.2 Implement `action.update.plan` with current revision checks, previous snapshots, diff metadata, and idempotency.
- [x] 2.3 Implement `action.apply` for atomic create/update, explicit confirmation, revision concurrency, plan expiry/state, idempotency, and audit.

## 3. Action queries

- [x] 3.1 Implement `action.query` date windows, overdue/today/waiting sections, status filters, deterministic ordering, limits, and sensitive filtering.
- [x] 3.2 Preserve optional source provenance and lifecycle timestamps/revisions in action responses and audit summaries.

## 4. Skill and tests

- [x] 4.1 Extend the Cairn Skill with action capture, apply confirmation, daily query, waiting-state explanation, and prompt-injection boundaries.
- [x] 4.2 Add unit tests for validation, date bucketing, ordering, sensitivity, and malformed plans.
- [x] 4.3 Add integration tests for create/update/apply, retries, stale/expired/confirmation failures, query sections, provenance, and completion/cancellation.

## 5. Validation and handoff

- [x] 5.1 Run tests, race tests, vet, build, strict OpenSpec validation, and whitespace checks.
- [x] 5.2 Review every scenario in both specs, complete verification, and archive the change only after all tasks pass.
