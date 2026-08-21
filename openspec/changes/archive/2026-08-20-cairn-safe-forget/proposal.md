## Why

Cairn currently has no safe way to honor “forget everything about project A” or recover a knowledge article to an earlier managed version. These are high-impact local mutations and need explicit plans, impact inspection, optimistic boundaries, recycle-area recovery, and audit evidence instead of ad-hoc row/file deletion.

## What Changes

- Add `source.forget.plan` to calculate affected sources, raw blobs, articles, actions, and compile records without mutating them.
- Add `knowledge.rollback.plan` to prepare a version-checked rollback of a managed article.
- Extend `plan.inspect`, `plan.apply`, and `plan.undo` into a single gateway for forget and rollback plans as well as existing knowledge plans.
- Move forgotten Raw/Wiki files into the managed local trash, mark records hidden, and support reversible undo within plan validity.
- Add stable optimistic-concurrency, expiry, confirmation, recovery-marker, and audit behavior for high-impact plans.
- Add `audit.query` for bounded local operation history.
- Update source, knowledge, and action queries to exclude forgotten records by default.

## Capabilities

### New Capabilities

- `safe-forget`: Impact plans, reversible recycle-area forget, knowledge rollback plans, generic plan gateway behavior, and audit queries.
- `forget-skill`: Claude confirmation and privacy workflow for forget/rollback requests.

### Modified Capabilities

<!-- No main specs exist yet; previous changes are archived without syncing. -->

## Impact

- Adds SQLite schema version 4 fields/tables for forgotten records and safety plans.
- Extends protocol capability discovery and app routing for forget, rollback, generic plans, and audit.
- Adds local file moves under the managed trash/staging directories with crash recovery blocking.
- Changes active source/Wiki/action queries to hide forgotten records while preserving audit/history data.
