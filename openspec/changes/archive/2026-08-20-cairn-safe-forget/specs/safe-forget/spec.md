## ADDED Requirements

### Requirement: Preview complete forget impact
Cairn SHALL implement `source.forget.plan` as an idempotent read-before-write operation that resolves selected sources and dependent Raw blobs, compile jobs, articles, citations, and actions, returning counts, IDs, risk flags, expiry, and a plan ID without changing active data.

#### Scenario: Preview by source IDs
- **WHEN** the Skill submits one or more existing source IDs
- **THEN** Cairn returns the affected closure and a pending plan while all source/article/action views remain unchanged

#### Scenario: Preview by origin match
- **WHEN** the Skill submits an allowed origin-name match
- **THEN** Cairn returns every matching source and its dependencies in deterministic order without returning source content

#### Scenario: Unknown or empty target
- **WHEN** no selector is provided or no source matches
- **THEN** Cairn returns `REQUEST_INVALID` or `SOURCE_NOT_FOUND` and creates no plan

### Requirement: Apply forget reversibly through the generic gateway
`plan.apply` SHALL apply a confirmed forget plan only when its impact snapshot is current, moving eligible Raw/Wiki files into managed trash, marking dependent records forgotten, and recording an audit event; `plan.undo` SHALL restore only that plan's unchanged trash mappings.

#### Scenario: Confirmed forget
- **WHEN** a pending, unexpired forget plan is confirmed and files/rows match its base snapshot
- **THEN** Cairn moves unshared files to trash, hides affected records, marks the plan applied, and leaves audit evidence

#### Scenario: Shared Raw blob
- **WHEN** a Raw blob is still referenced by an active source outside the forget set
- **THEN** Cairn keeps that blob active while forgetting only the selected source metadata and dependent records

#### Scenario: Stale or drifted forget plan
- **WHEN** a source/article/action changed or a managed file hash differs after preview
- **THEN** Cairn returns `PLAN_STALE` or `WIKI_DRIFT` and performs no partial forget

#### Scenario: Undo unchanged forget
- **WHEN** an applied forget plan's trash files and forgotten rows still match its snapshot
- **THEN** `plan.undo` restores files/paths and clears forgotten markers atomically

### Requirement: Prepare a version-checked knowledge rollback
Cairn SHALL implement `knowledge.rollback.plan` for a selected managed article and historical version, recording the current version/hash and target content without mutating the Wiki until the generic plan gateway applies it.

#### Scenario: Preview rollback
- **WHEN** the selected article is current, hash-verified, and the target historical version exists with a valid stored hash
- **THEN** Cairn returns a pending rollback plan with current/target versions, diff metadata, and expiry

#### Scenario: Apply rollback
- **WHEN** a pending rollback plan is confirmed and the current article still matches its base version/hash
- **THEN** `plan.apply` replaces the managed file atomically, updates the current article pointer, and records audit evidence

#### Scenario: Rollback after later edit
- **WHEN** the article version or Wiki hash changed after rollback preview
- **THEN** Cairn returns `PLAN_STALE` or `WIKI_DRIFT` without overwriting the later edit

### Requirement: Expose bounded audit history
Cairn SHALL implement `audit.query` as a read-only, deterministic, bounded operation that returns operation, request, status, timestamp, and summary metadata for both successful and failed plans without exposing forgotten content.

#### Scenario: Query recent audit
- **WHEN** the Skill requests audit entries with a limit
- **THEN** Cairn returns newest-first entries with stable tie ordering and no Raw/Wiki body data

#### Scenario: Invalid audit limit
- **WHEN** the requested limit is outside the supported range
- **THEN** Cairn returns `REQUEST_INVALID` without reading unbounded rows
