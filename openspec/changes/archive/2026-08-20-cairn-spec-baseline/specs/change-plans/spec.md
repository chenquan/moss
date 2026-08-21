## ADDED Requirements

### Requirement: Create and inspect plans
`compile.preview` SHALL create an idempotent pending plan containing affected objects, proposed diff metadata, risk flags, base article versions, and an expiry; `plan.inspect` SHALL return that information without applying it.

#### Scenario: Preview new article
- **WHEN** a preview-ready job has a valid write result
- **THEN** Cairn returns a plan ID, a new-article diff, affected source IDs, and no Markdown mutation

#### Scenario: Preview conflict or sensitive content
- **WHEN** preview detects an existing article, overwrite, conflict, or sensitive content
- **THEN** the plan includes explicit risk flags and requires Skill confirmation before apply

### Requirement: Apply plans through one gateway
`plan.apply` SHALL be the only operation that applies a knowledge plan, SHALL enforce plan status, expiry, base versions, Wiki hashes, and recovery health, and SHALL be idempotent.

#### Scenario: Apply confirmed current plan
- **WHEN** a pending plan is valid, unexpired, current, and confirmed by the Skill policy
- **THEN** Cairn atomically writes the managed Markdown/version records, marks the job and plan applied, and records an audit event

#### Scenario: Stale or drifted plan
- **WHEN** the article version changes, the Wiki hash differs, or the plan expires before apply
- **THEN** Cairn returns `PLAN_STALE` or `WIKI_DRIFT` and performs no write

#### Scenario: Repeated apply
- **WHEN** an applied plan is submitted again with the same idempotency key
- **THEN** Cairn returns the original apply response without duplicating the article version

### Requirement: Undo only the plan's own change
`plan.undo` SHALL restore the immediately prior managed version or remove a newly created article only when the current file and version still match the target plan's output.

#### Scenario: Undo an unchanged applied plan
- **WHEN** the plan is applied and the article has not changed since that application
- **THEN** Cairn restores the previous version or moves the new article to managed trash and marks the plan undone

#### Scenario: Undo after later edit
- **WHEN** the article changed after the target plan was applied
- **THEN** Cairn returns `PLAN_STALE` and leaves the article untouched
