## ADDED Requirements

### Requirement: Apply a reviewed compile plan
`compile.apply` SHALL be the compile-specific gateway for applying a pending knowledge plan and SHALL enforce explicit confirmation, plan state, expiry, article base version, managed Wiki hash, recovery health, audit, and idempotency checks before writing.

#### Scenario: Apply a confirmed compile plan
- **WHEN** the Skill submits an unexpired pending compile plan with `confirmed: true`
- **THEN** Cairn applies the managed article atomically, marks the plan and job applied, and returns the article result

#### Scenario: Missing confirmation or drift
- **WHEN** confirmation is absent, the plan is stale/expired, or the Wiki hash has drifted
- **THEN** Cairn returns the corresponding stable error and leaves the plan, article, and job unchanged

#### Scenario: Retry compile apply
- **WHEN** the same `compile.apply` request is retried with its idempotency key
- **THEN** Cairn returns the original response without duplicating an article version or audit mutation
