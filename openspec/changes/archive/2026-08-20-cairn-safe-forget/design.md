## Context

Cairn has several independently versioned stores: immutable Raw blobs, source metadata, managed Markdown/articles, action rows, compile jobs, and audit events. A privacy request must remove records from active views without silently deleting shared data or leaving a half-moved file. A rollback must use the same confirmation and optimistic plan boundary as forget.

## Goals / Non-Goals

**Goals:**

- Calculate a complete impact plan before any forget/rollback mutation.
- Hide forgotten sources, articles, and actions from normal operations while keeping reversible metadata in SQLite and files in managed trash.
- Ensure shared Raw blobs are moved only when no active source still references them.
- Apply and undo safety plans through one `plan.apply`/`plan.undo` gateway with confirmation, expiry, base snapshots, staging markers, idempotency, and audit.
- Expose bounded audit history without returning arbitrary content.

**Non-Goals:**

- No secure hardware erasure guarantee, remote backup deletion, legal retention policy, or background trash purge.
- No deletion of audit events; audit is the evidence needed to explain what happened.
- No generic plan table rewrite of the compile/article state machine; safety plans are additive and dispatched by plan ID.

## Decisions

### Additive safety plan records

Add `safety_plans` with `kind` (`forget` or `rollback`), target/previous JSON snapshots, impact/diff/risk metadata, expiry, and state. This avoids overloading the compile `plans` table, whose non-null job/article relations are useful invariants for knowledge writes.

### Soft-hide records plus managed trash

Add nullable `forgotten_at` fields to sources, articles, and actions. Active source/knowledge/action queries filter the field. Forget moves unshared Raw files and managed article files to `trash/<plan-id>-...`, updates stored paths, and records the original/trash mapping in the plan snapshot. Undo validates those exact mappings and moves them back. Shared blobs remain at their active path until all referencing sources are forgotten.

### Plan impact closure

`source.forget.plan` accepts explicit source IDs or an origin-name match and resolves dependent blobs, compile jobs, article citations, and action source links. It reports counts and IDs. It never searches or returns unbounded source content. `knowledge.rollback.plan` records current article version/hash/path and a target historical version/content hash after current Wiki drift checks.

### Crash boundary and dispatch

Safety apply/undo writes a private staging marker before moving any file. The marker remains if a process fails across the filesystem/SQLite boundary, making `system.health` return recovery-required and blocking future mutation. The app first routes a generic `plan.*` request to the existing compile plan handler and falls back to the safety handler only for `PLAN_NOT_FOUND`.

### Audit query

`audit.query` returns operation, request ID, status, timestamp, and bounded summary JSON with stable ordering and a limit. It never reads Raw or Wiki content and keeps forgotten-operation evidence visible.

## Risks / Trade-offs

- **[Risk]** A crash leaves files in trash but the DB pending. → Keep a staging marker, block mutation, and require a later maintenance/recovery workflow before retry.
- **[Risk]** A shared Raw blob is moved while another source still uses it. → Compute active reference count at apply and move only unshared blobs; recheck within the transaction.
- **[Risk]** A user expects cryptographic erasure. → State the v1 boundary: active access is removed and files are recoverable in local trash for the retention window; secure erase is not claimed.
- **[Risk]** A later article edit makes rollback unsafe. → Store current version/hash and return `PLAN_STALE`/`WIKI_DRIFT` before touching the file.

## Migration Plan

Bump SQLite schema from version 3 to 4, add forgotten columns and `safety_plans`, and preserve version 1/2/3 data through additive `ALTER TABLE` checks. Update active queries to filter forgotten rows. Rollback is supported through a newer binary and plan undo; no schema downgrade or audit deletion is attempted.

## Open Questions

Trash retention/purge policy and secure-erasure integration remain deployment decisions. This change keeps trash local and recoverable so the next maintenance change can add an explicit purge plan.
