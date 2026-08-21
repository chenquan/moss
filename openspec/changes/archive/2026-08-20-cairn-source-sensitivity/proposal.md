## Why

Cairn's source operation set promises a way to revise a captured source's sensitivity after ingestion, but the current runtime exposes no `source.mark_sensitive` operation. That leaves privacy classification stale and forces users to re-ingest content instead of safely tightening or correcting its access boundary.

## What Changes

- Add the machine operation `source.mark_sensitive` with strict source selection and supported sensitivity values (`normal`, `sensitive`, `restricted`).
- Make the operation idempotent and auditable, hide forgotten sources, and return the previous and new sensitivity without returning source content.
- Require explicit Skill confirmation when reducing a source's sensitivity; allow a tightening change without an extra confirmation while still recording it.
- Route the operation through the mutable-storage health gate and document it in the Skill.

## Capabilities

### New Capabilities

- `source-sensitivity`: Sensitivity updates for active captured sources with privacy and confirmation boundaries.

### Modified Capabilities

- None.

## Impact

- `internal/protocol`, `internal/source`, `internal/app`, and the Cairn Skill gain the operation and request/response contract.
- SQLite source metadata is updated in place; no schema migration or dependency is required.
