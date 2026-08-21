## Context

Cairn currently stores immutable sources and managed knowledge, but action candidates have nowhere to live after a conversation. The action ledger is a local SQLite capability used by the Skill; it is not a scheduler, notification service, or human-facing CLI.

## Goals / Non-Goals

**Goals:**

- Make action creation and updates reviewable plans with one atomic apply gateway.
- Support tasks, commitments, and reminders with due dates and waiting-for state.
- Preserve sensitivity, source provenance, idempotency, optimistic concurrency, and audit history.
- Make daily/overdue/waiting queries deterministic and bounded.

**Non-Goals:**

- No notifications, calendar sync, external task provider, background worker, natural-language answer generation, or Web UI.
- No automatic extraction of actions from compile output in this change; a later Skill may create plans from validated candidates.
- No destructive action deletion; cancellation is a status transition and remains auditable.

## Decisions

### Separate action plans from knowledge plans

Add `actions` and `action_plans` tables rather than reusing the compile `plans` row, whose article/job foreign keys are intentionally knowledge-specific. An action plan stores proposed and previous JSON snapshots, a base revision, diff/risk metadata, and expiry. `action.apply` atomically updates both the action ledger and plan state.

Alternative: put nullable action columns in `plans`. That would weaken foreign-key invariants and make one plan table encode two unrelated state machines.

### Integer optimistic revisions

Every action starts at revision 1. An update plan records the current revision; apply uses `WHERE action_id = ? AND revision = ?` and increments it. This avoids relying on timestamp precision for concurrent Claude turns.

### Explicit action model and query semantics

Kinds are `task`, `commitment`, and `reminder`; statuses are `open`, `in_progress`, `done`, `deferred`, and `cancelled`. `action.query` accepts an optional local date (default current local date), returns today, overdue, waiting, and bounded all-items sections, and orders by due time, status, and action ID. A missing due date sorts after dated actions.

### Sensitivity and confirmation

Actions carry normal/sensitive/restricted sensitivity and optional source ID. Query hides sensitive actions unless `allow_sensitive=true`. All plan applications require `confirmed=true` from the Skill; the Skill may treat a direct user instruction as policy authorization, but the CLI remains a two-step state machine.

### Idempotency and audit

`action.create.plan`, `action.update.plan`, and `action.apply` require idempotency keys. Plans and action mutations reserve/complete the foundation idempotency row in the same SQLite transaction and append an audit event. Retrying an identical key returns the original response; a different fingerprint is a stable conflict.

## Risks / Trade-offs

- **[Risk]** A user expects reminders to fire. → Keep the boundary explicit: this change records/query actions; no scheduler claims delivery.
- **[Risk]** Sensitive details leak in a daily view. → Filter rows before assembling any title/details/source fields.
- **[Risk]** A stale update overwrites a later Claude turn. → Require base revision equality and return `ACTION_PLAN_STALE` without mutation.
- **[Risk]** A crash occurs after action update but before plan completion. → The SQLite transaction makes the state change and idempotency completion atomic; no filesystem recovery marker is needed.

## Migration Plan

Bump the additive SQLite schema from version 2 to 3 and create action tables/indexes. Existing version 1/2 databases are upgraded after all `CREATE TABLE IF NOT EXISTS` statements; Raw sources, jobs, articles, and plans remain untouched. Rollback uses a newer binary to read the additive tables; no destructive schema downgrade is attempted.

## Open Questions

Notification delivery, recurring actions, and cross-device synchronization remain future changes. Waiting-for is a searchable field, not an external dependency or state machine.
