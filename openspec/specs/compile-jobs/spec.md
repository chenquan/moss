# compile-jobs Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Start a resumable compile job
`compile.start` SHALL create an idempotent single-source job, copy the source into the job input area, create extract/classify/write stage records, and return a job ID with the first available stage.

#### Scenario: Start from an existing source
- **WHEN** a valid source ID is provided with a new idempotency key
- **THEN** Moss creates a `running` job, preserves the source hash, and returns managed input/schema/result file references for the extract stage

#### Scenario: Unknown source
- **WHEN** `compile.start` references an unknown source ID
- **THEN** Moss returns `SOURCE_NOT_FOUND` and creates no job

#### Scenario: Retry start
- **WHEN** the same start request is retried with the same idempotency key
- **THEN** Moss returns the original job ID without creating another job

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
- **THEN** Moss marks that stage submitted and exposes the next stage or preview-ready state

#### Scenario: Malformed or invalid result
- **WHEN** the result is invalid JSON, violates its schema, references an unknown source, or is outside the job directory
- **THEN** Moss returns a stable validation error and leaves the stage available for correction

#### Scenario: Duplicate submission
- **WHEN** an already submitted stage is submitted again
- **THEN** Moss returns `STAGE_ALREADY_SUBMITTED` or the idempotent original response and does not advance the job twice

### Requirement: Query and abort jobs
`compile.status` SHALL return deterministic job/stage metadata, and `compile.abort` SHALL stop a non-applied job idempotently while preserving audit and diagnostic artifacts.

#### Scenario: Status request
- **WHEN** a caller requests an existing job
- **THEN** the response includes job state, source ID, stage states, result hashes, and timestamps

#### Scenario: Abort running job
- **WHEN** an authorized caller aborts a running or preview-ready job
- **THEN** the job becomes `aborted`, future submissions are refused, and its files remain available for audit

### Requirement: Apply a reviewed compile plan
`compile.apply` SHALL be the compile-specific gateway for applying a pending knowledge plan and SHALL enforce explicit confirmation, plan state, expiry, article base version, managed Wiki hash, recovery health, audit, and idempotency checks before writing.

#### Scenario: Apply a confirmed compile plan
- **WHEN** the Skill submits an unexpired pending compile plan with `confirmed: true`
- **THEN** Moss applies the managed article atomically, marks the plan and job applied, and returns the article result

#### Scenario: Missing confirmation or drift
- **WHEN** confirmation is absent, the plan is stale/expired, or the Wiki hash has drifted
- **THEN** Moss returns the corresponding stable error and leaves the plan, article, and job unchanged

#### Scenario: Retry compile apply
- **WHEN** the same `compile.apply` request is retried with its idempotency key
- **THEN** Moss returns the original response without duplicating an article version or audit mutation

