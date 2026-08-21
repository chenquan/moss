## ADDED Requirements

### Requirement: Start a resumable compile job
`compile.start` SHALL create an idempotent single-source job, copy the source into the job input area, create extract/classify/write stage records, and return a job ID with the first available stage.

#### Scenario: Start from an existing source
- **WHEN** a valid source ID is provided with a new idempotency key
- **THEN** Cairn creates a `running` job, preserves the source hash, and returns managed input/schema/result file references for the extract stage

#### Scenario: Unknown source
- **WHEN** `compile.start` references an unknown source ID
- **THEN** Cairn returns `SOURCE_NOT_FOUND` and creates no job

#### Scenario: Retry start
- **WHEN** the same start request is retried with the same idempotency key
- **THEN** Cairn returns the original job ID without creating another job

### Requirement: Expose the next stage
`compile.next` SHALL return only the next valid stage for a job, including its schema file, input files, result file, and stage status, without allowing the caller to select an arbitrary stage.

#### Scenario: Extract is available
- **WHEN** a newly created running job is queried
- **THEN** `compile.next` returns the extract stage and its managed file references

#### Scenario: No stage is available
- **WHEN** a job is applied, aborted, failed, or waiting for a submitted result
- **THEN** `compile.next` returns the current state without advancing it

### Requirement: Validate and submit stage results
`compile.submit` SHALL require the current stage, validate the result file against the embedded stage schema, enforce managed paths and bounded size, validate source citations and sensitivity, record the result hash, and advance the job only after all checks pass.

#### Scenario: Valid extract/classify/write result
- **WHEN** Claude writes a schema-valid result to the exact current stage result file
- **THEN** Cairn marks that stage submitted and exposes the next stage or preview-ready state

#### Scenario: Malformed or invalid result
- **WHEN** the result is invalid JSON, violates its schema, references an unknown source, or is outside the job directory
- **THEN** Cairn returns a stable validation error and leaves the stage available for correction

#### Scenario: Duplicate submission
- **WHEN** an already submitted stage is submitted again
- **THEN** Cairn returns `STAGE_ALREADY_SUBMITTED` or the idempotent original response and does not advance the job twice

### Requirement: Query and abort jobs
`compile.status` SHALL return deterministic job/stage metadata, and `compile.abort` SHALL stop a non-applied job idempotently while preserving audit and diagnostic artifacts.

#### Scenario: Status request
- **WHEN** a caller requests an existing job
- **THEN** the response includes job state, source ID, stage states, result hashes, and timestamps

#### Scenario: Abort running job
- **WHEN** an authorized caller aborts a running or preview-ready job
- **THEN** the job becomes `aborted`, future submissions are refused, and its files remain available for audit

