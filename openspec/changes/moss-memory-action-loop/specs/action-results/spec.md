## ADDED Requirements

### Requirement: Create confirmed action-result plans

Moss SHALL implement `action.result.plan` as an idempotent, non-mutating plan operation that validates an existing action, bounded outcome status, summary, optional live source, sensitivity, and result metadata.

#### Scenario: Plan a successful result

- **WHEN** the Skill submits a valid result for an existing action
- **THEN** Moss returns a pending result plan with result ID, action ID, diff, risk flags, base action revision, and expiry while the action and result tables remain unchanged

#### Scenario: Unknown action or source

- **WHEN** the result references an unknown/forgotten action or source
- **THEN** Moss returns a stable validation error and creates no result plan

### Requirement: Apply results through a confirmed gateway

Moss SHALL implement `action.result.apply` as the only operation that creates action-result rows, SHALL require explicit confirmation, and SHALL enforce plan state, expiry, optimistic action revision, sensitivity, audit, and idempotency checks.

#### Scenario: Apply a result

- **WHEN** a pending unexpired result plan is confirmed against the current action revision
- **THEN** Moss inserts the result at the next action-result version (version 1 for the first result), creates an `action -> produces -> action_result` relation, marks the plan applied, and leaves the action lifecycle status unchanged

#### Scenario: Retry result apply

- **WHEN** the same result apply request is retried with its idempotency key
- **THEN** Moss returns the original response without duplicating the result or relation

#### Scenario: Stale or missing confirmation

- **WHEN** confirmation is absent, the plan is expired, or the action revision changed
- **THEN** Moss returns the corresponding stable error and writes no result or relation

### Requirement: Keep results separate from fact mutation

Action-result application SHALL NOT create or update facts, articles, or action lifecycle status; later fact/relationship changes SHALL use an explicit compile plan.

#### Scenario: Result does not complete action

- **WHEN** a result has status `succeeded`
- **THEN** the action remains at its prior status and must be changed through the existing action update workflow

#### Scenario: Result provenance

- **WHEN** a result includes a permitted source ID
- **THEN** the result and generated relation retain that source reference for review/context filtering
