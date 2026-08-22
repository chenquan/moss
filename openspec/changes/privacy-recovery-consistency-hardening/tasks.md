## 1. Privacy boundaries

- [x] 1.1 Gate every requested article history version by its parsed sensitivity and expose per-version sensitivity.
- [x] 1.2 Reject unauthorized sensitive article/fact records while building compile context.
- [x] 1.3 Add history and compile sensitivity regression tests and update Skill guidance.

## 2. Mutation recovery

- [x] 2.1 Add the data-root mutation lock and recovery journal primitives.
- [x] 2.2 Migrate safety, legacy compile, source sensitivity, and backup/restore mutations to the journal.
- [x] 2.3 Add `system.recover`, health details, capabilities, and protocol/Skill documentation.
- [x] 2.4 Add failure-injection, conflict, idempotency, and concurrent mutation tests.

## 3. Projection and storage integrity

- [x] 3.1 Rebuild complete FTS projections for forget and legacy compile undo.
- [x] 3.2 Stale corrupted extraction records before replacement.
- [x] 3.3 Validate backup/restore provenance and snapshot under the mutation lock.
- [x] 3.4 Add metadata drift checks and propagate all query/scan/iteration errors.

## 4. Verification

- [x] 4.1 Run focused tests, `go test ./...`, `go vet ./...`, and strict OpenSpec validation.
- [x] 4.2 Build and execute the complete binary flow including recovery and backup/restore.
- [x] 4.3 Mark tasks complete only after the final worktree and diff checks pass.
