# change-plans Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Create and inspect plans
`compile.preview` SHALL create an idempotent pending plan containing affected objects, proposed diff metadata, risk flags, base article versions, and an expiry; `plan.inspect` SHALL return that information without applying it.

#### Scenario: Preview new article
- **WHEN** a preview-ready job has a valid write result
- **THEN** Moss returns a plan ID, a new-article diff, affected source IDs, and no Markdown mutation

#### Scenario: Preview conflict or sensitive content
- **WHEN** preview detects an existing article, overwrite, conflict, or sensitive content
- **THEN** the plan includes explicit risk flags and requires Skill confirmation before apply

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
- **THEN** Moss returns `PLAN_STALE`, `WIKI_DRIFT`, or the corresponding expiry error and performs no write

#### Scenario: Repeated apply
- **WHEN** an applied plan is submitted again with the same idempotency key to its gateway
- **THEN** Moss returns the original apply response without duplicating the article version or safety effect

### Requirement: Undo only the plan's own change
`plan.undo` SHALL restore the immediately prior managed version or remove a newly created article only when the current file and version still match the target plan's output.

#### Scenario: Undo an unchanged applied plan
- **WHEN** the plan is applied and the article has not changed since that application
- **THEN** Moss restores the previous version or moves the new article to managed trash and marks the plan undone

#### Scenario: Undo after later edit
- **WHEN** the article changed after the target plan was applied
- **THEN** Moss returns `PLAN_STALE` and leaves the article untouched

