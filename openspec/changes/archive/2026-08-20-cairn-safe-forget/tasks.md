## 1. Protocol and schema migration

- [x] 1.1 Add forget/rollback/audit request models, mutating detection, capabilities, and generic plan dispatch fallback.
- [x] 1.2 Bump SQLite migration to schema version 4 with forgotten markers and safety plan records, including upgrades from versions 1–3.
- [x] 1.3 Update source, knowledge, action, and compile queries to hide forgotten records and preserve audit/history visibility.

## 2. Forget and rollback plans

- [x] 2.1 Implement `source.forget.plan` target resolution, dependency closure, impact counts, snapshots, expiry, idempotency, and audit.
- [x] 2.2 Implement `knowledge.rollback.plan` current/hash/version validation, target snapshots, diff metadata, expiry, idempotency, and audit.
- [x] 2.3 Implement generic safety `plan.inspect` responses for forget and rollback plans.
- [x] 2.4 Implement safety `plan.apply` with confirmation, base checks, trash moves, forgotten markers, atomic transaction, and recovery markers.
- [x] 2.5 Implement safety `plan.undo` with unchanged-trash validation, file restoration, marker clearing, and idempotent audit.
- [x] 2.6 Implement bounded `audit.query` with stable ordering and summary redaction boundaries.

## 3. Skill and tests

- [x] 3.1 Extend the Cairn Skill with forget/rollback impact confirmation, recovery-window language, and stale/drift safety rules.
- [x] 3.2 Add unit tests for selectors, dependency closure, version/hash checks, trash naming, and audit limits.
- [x] 3.3 Add integration tests for forget preview/apply/undo, shared blobs, rollback, stale plans, drift, recovery markers, and audit output.

## 4. Validation and handoff

- [x] 4.1 Run tests, race tests, vet, build, strict OpenSpec validation, and whitespace checks.
- [x] 4.2 Review every scenario in both specs, complete verification, and archive the change only after all tasks pass.
