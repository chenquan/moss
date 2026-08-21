## Why

Cairn can preserve sources and knowledge but has no durable record of the work those materials imply. The Skill needs a local, auditable action ledger for tasks, commitments, and reminders so “what should I push today?” is a query over explicit state rather than an inference from chat history.

## What Changes

- Add `action.create.plan` and `action.update.plan` to describe action changes without mutating the ledger.
- Add `action.apply` as the only action mutation gateway with idempotency, optimistic revision checks, expiry, confirmation, and audit records.
- Add `action.query` for deterministic today, overdue, waiting, and status-filtered action views.
- Store action kind, title, details, due time, waiting-for party, sensitivity, status, revision, and optional source provenance in SQLite.
- Extend the Cairn Skill with capture/query/update workflows and user-facing confirmation rules while keeping CLI syntax invisible.

## Capabilities

### New Capabilities

- `action-ledger`: Plans, atomic application, optimistic concurrency, sensitive filtering, and deterministic action queries.
- `action-skill`: Claude routing for action capture and daily progress questions.

### Modified Capabilities

<!-- No main specs exist yet; previous changes are archived without syncing. -->

## Impact

- Extends protocol capabilities and app dispatch with three mutating/read-only action operations.
- Adds additive SQLite schema for actions and action plans, including migration from the current schema version.
- Adds Go validation/query code, audit/idempotency integration, Skill guidance, and end-to-end tests.
