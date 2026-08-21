## MODIFIED Requirements

### Requirement: Apply plans through one gateway
`compile.apply` SHALL be the gateway that applies a compile knowledge plan, while `plan.apply` SHALL remain the gateway for generic safety plans such as forget and rollback. Both gateways SHALL enforce plan status, expiry, base versions, Wiki hashes where applicable, recovery health, explicit confirmation, and idempotency.

#### Scenario: Apply confirmed current compile plan
- **WHEN** a pending compile plan is valid, unexpired, current, and confirmed by the Skill policy
- **THEN** `compile.apply` atomically writes the managed Markdown/version records, marks the job and plan applied, and records an audit event

#### Scenario: Apply confirmed safety plan
- **WHEN** a pending forget or rollback plan is valid, unexpired, current, and confirmed by the Skill policy
- **THEN** `plan.apply` atomically applies that safety plan and records an audit event without routing through the compile workflow

#### Scenario: Stale or drifted plan
- **WHEN** the article version changes, the Wiki hash differs, or a plan expires before its gateway is called
- **THEN** Cairn returns `PLAN_STALE`, `WIKI_DRIFT`, or the corresponding expiry error and performs no write

#### Scenario: Repeated apply
- **WHEN** an applied plan is submitted again with the same idempotency key to its gateway
- **THEN** Cairn returns the original apply response without duplicating the article version or safety effect
