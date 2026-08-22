## MODIFIED Requirements

### Requirement: Gate and deterministically bound source-set jobs
`compile.preview` SHALL reject jobs not in `preview_ready`, normalize source IDs as sorted unique values, and reject a source set whose aggregate byte size or bounded stage/context budget exceeds the configured limit before creating job files.

#### Scenario: Reject an unfinished job
- **WHEN** preview is requested while the Job is running, aborted, or already applied
- **THEN** Moss rejects the request without creating a batch plan

### Requirement: Validate a batch before filesystem finalization
Batch planning/apply SHALL reject duplicate article targets, duplicate fact identities, invalid extraction or supersession references, sensitivity violations, stale bases, fact-key conflicts, and unsafe managed paths before the first final file rename.

#### Scenario: Reject a conflict before rename
- **WHEN** a fact key or article target changed after preview
- **THEN** the batch is rejected and no final Wiki file is renamed

### Requirement: Preserve recovery state after finalization begins
If any error occurs after a batch final rename begins, Moss SHALL retain a recovery marker and enough staging state for health/recovery handling; the marker SHALL be removed only after filesystem finalization and SQLite commit both succeed.

#### Scenario: Database failure after rename
- **WHEN** a batch file is renamed but its SQLite transaction fails
- **THEN** the recovery marker remains and health reports recoverable divergence

### Requirement: Persist batch review details
`compile_batch_plans` inspection after restart SHALL return the persisted diff and risk information that was returned by the original preview.

#### Scenario: Inspect after restart
- **WHEN** a pending batch plan is inspected after the process restarts
- **THEN** its diff and risk flags are returned
