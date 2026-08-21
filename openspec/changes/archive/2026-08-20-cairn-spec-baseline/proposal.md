## Why

The Cairn implementation was completed through several OpenSpec changes, but the early changes were archived without syncing their delta specs into `openspec/specs/`. The repository's main specification set therefore understates the implemented product contract and makes a strict completion review harder to trust.

## What Changes

- Promote the already-reviewed, archived requirements for the foundation, capture, compile, retrieval, and action capabilities into the main OpenSpec catalog.
- Keep this as a specification-only reconciliation; it does not add a human CLI, Web UI, MCP integration, or runtime behavior.
- Preserve the archived change records as the historical source for when each capability was implemented.

## Capabilities

### New Capabilities

- `cairn-skill`: Claude Code Skill as the sole user interface.
- `cli-protocol`: file-based machine request/response protocol.
- `local-storage`: private SQLite and managed filesystem layout.
- `source-capture`: content-addressed source ingestion and retrieval.
- `system-health`: handshake, health, and capability reporting.
- `action-ledger`: durable tasks, commitments, reminders, and waiting-for records.
- `action-skill`: Claude routing for action-ledger operations.
- `change-plans`: preview, apply, undo, and drift-safe change plans.
- `compile-jobs`: resumable extract/classify/write compilation jobs.
- `compile-skill`: Claude routing for compilation workflows.
- `managed-wiki`: deterministic Markdown Wiki materialization and recovery.
- `knowledge-retrieval`: local catalog, candidate selection, materialization, and history.
- `retrieve-skill`: Claude routing for evidence-based retrieval answers.

### Modified Capabilities

<!-- No requirement behavior changes; this change reconciles missing main specs only. -->

## Impact

- Adds main specification files under `openspec/specs/` for behavior already implemented and tested in `internal/` and documented in `.claude/skills/cairn/`.
- Does not change Go code, the JSON protocol, dependencies, or the user-facing interaction boundary.
