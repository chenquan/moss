# action-ledger Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Create and update auditable action plans
Cairn SHALL implement `action.create.plan` and `action.update.plan` as idempotent operations that validate action kind, status, due time, sensitivity, source provenance, and optimistic base revision without mutating the action row.

#### Scenario: Create a task plan
- **WHEN** the Skill submits a valid task title and optional due/waiting metadata
- **THEN** Cairn returns a pending action plan with an action ID, diff, risk flags, and expiry while the action ledger remains unchanged

#### Scenario: Update a current action
- **WHEN** the Skill submits a valid update with the action's current revision
- **THEN** Cairn returns a pending update plan containing the previous and proposed snapshots

#### Scenario: Invalid or unknown action
- **WHEN** a plan has an unsupported kind/status, missing title, invalid due time, unknown source, or unknown action ID
- **THEN** Cairn returns a stable validation error and creates no action mutation

### Requirement: Apply actions through one idempotent gateway
`action.apply` SHALL be the only operation that creates or updates action rows, SHALL require explicit Skill confirmation, SHALL enforce plan state, expiry, base revision, and idempotency, and SHALL write an audit event.

#### Scenario: Apply a confirmed create plan
- **WHEN** a pending unexpired create plan is confirmed
- **THEN** Cairn inserts the action at revision 1, marks the plan applied, and returns the action without duplicating it on retry

#### Scenario: Apply a confirmed update plan
- **WHEN** a pending update plan matches the action's current revision
- **THEN** Cairn atomically updates the action, increments its revision, marks the plan applied, and records the previous snapshot in the plan

#### Scenario: Missing confirmation, stale, or expired plan
- **WHEN** confirmation is absent, the base revision changed, or the plan expired
- **THEN** Cairn returns `CONFIRMATION_REQUIRED`, `ACTION_PLAN_STALE`, or `ACTION_PLAN_EXPIRED` and performs no action mutation

### Requirement: Query today's and waiting actions deterministically
Cairn SHALL implement `action.query` as a read-only, bounded operation that returns today, overdue, waiting-for, and status-filtered actions in deterministic order with sensitivity filtering.

#### Scenario: Query today
- **WHEN** the Skill requests a local date
- **THEN** Cairn returns actions due on that date plus open/in-progress actions ordered by due time and action ID

#### Scenario: Query overdue and waiting
- **WHEN** actions have due times before the requested date or a non-empty waiting-for value
- **THEN** the response includes them in the corresponding sections without changing their status

#### Scenario: Sensitive action query
- **WHEN** sensitive/restricted actions exist and `allow_sensitive` is false
- **THEN** those actions are omitted with no title, details, or source leakage

### Requirement: Preserve action provenance and lifecycle
The action ledger SHALL store kind, title, details, due time, waiting-for, sensitivity, optional source ID, revision, lifecycle status, timestamps, and audit references for every applied change.

#### Scenario: Action sourced from a local article
- **WHEN** an action plan includes a valid source ID
- **THEN** the applied action retains that source ID for later Skill answers and audit inspection

#### Scenario: Completion or cancellation
- **WHEN** an update plan changes an action to `done` or `cancelled`
- **THEN** Cairn records the new status and timestamp without deleting the action history

