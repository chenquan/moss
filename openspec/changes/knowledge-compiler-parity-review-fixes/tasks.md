## 1. Batch lifecycle and validation

- [x] 1.1 Gate multi-source preview on `preview_ready` and reject duplicate article targets during plan construction.
- [x] 1.2 Add complete batch preflight for article/fact optimistic concurrency, fact-key conflicts, provenance references, source sensitivity, and Wiki path safety before any rename.
- [x] 1.3 Retain recovery markers after finalization begins and expose persisted batch diff/risk fields during plan inspection.
- [x] 1.4 Add batch regression tests for invalid state, duplicate targets, conflicts, symlink paths, recovery markers, and restart inspection.

## 2. Extraction and fact reconciliation

- [x] 2.1 Enforce one extraction item per Job source and persist source-specific artifact content and hashes.
- [x] 2.2 Centralize extraction artifact integrity checks and use them for extraction reuse and backfill planning.
- [x] 2.3 Enforce batch source sensitivity and validate extraction/supersession provenance references during preview.
- [x] 2.4 Record forget/retraction as a new fact version and expose fact freshness in compile context.
- [x] 2.5 Add extraction and fact-version regression tests for missing/duplicate items, sensitivity, invalid references, retraction history, and stale freshness.

## 3. Durable storage and indexing

- [x] 3.1 Include extraction artifacts in backup export/restore and verify restored provenance references.
- [x] 3.2 Update FTS projections from legacy article apply, safety rollback, and other article mutation paths.
- [x] 3.3 Add backup and FTS consistency tests.

## 4. Determinism and backfill hardening

- [x] 4.1 Sort normalized source IDs and enforce aggregate source/stage/context byte limits before job creation.
- [x] 4.2 Generate unique backfill manifest IDs while preserving exact idempotent response replay shape.
- [x] 4.3 Propagate citation query/scan/iteration errors and exclude corrupted extraction artifacts from reuse.
- [x] 4.4 Add deterministic ordering, size-limit, backfill replay, manifest identity, query-error, and corruption tests.

## 5. Verification and specification hygiene

- [x] 5.1 Run focused package tests and the full Go test suite with a writable repository-local GOCACHE.
- [x] 5.2 Run `openspec validate --all --strict` and `git diff --check`.
- [x] 5.3 Review the final diff against every P1/P2 review item and update task status only after verification passes.
