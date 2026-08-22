## Why

The first knowledge compiler parity implementation added multi-source compilation, provenance, batch apply, and indexing, but review found failure-path gaps that can leave filesystem, SQLite, provenance, or replay state inconsistent. These issues must be fixed before the capability is safe for production use.

## What Changes

- Make batch preview/apply state-gated, fully preflighted, symlink-safe, and recoverable after filesystem finalization starts.
- Enforce source sensitivity, exact per-source extraction mapping, provenance reference validity, and versioned fact retractions.
- Include extraction artifacts in backup/restore and keep article FTS projections current for every mutation path.
- Make source ordering and size limits deterministic and harden backfill identity, replay, query-error handling, and artifact integrity checks.
- Add regression coverage for all reviewed failure, replay, recovery, and stale-state scenarios.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `multi-source-compilation`: strengthen state gates, deterministic input limits, validation, and recoverable atomic apply.
- `extraction-provenance`: require one valid artifact per source item and verify managed artifact integrity before reuse.
- `fact-reconciliation`: represent forget operations as versioned retractions and expose freshness in compile context.
- `backup-restore`: preserve extraction artifacts together with SQLite provenance state.
- `knowledge-indexing`: update FTS for every article mutation path.
- `knowledge-retrieval`: make backfill planning and idempotent replay complete and error-safe.

## Impact

Affected areas include `internal/compile`, `internal/knowledge`, `internal/storage`, `internal/system`, `internal/safety`, and their tests. No public protocol operation is removed; error responses become stricter for invalid or unsafe plans.
