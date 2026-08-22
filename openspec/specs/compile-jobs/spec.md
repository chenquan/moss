# compile-jobs Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Start a resumable compile job
`compile.start` SHALL create an idempotent job for either one legacy source or a bounded source set, copy all sources into managed job inputs, create extract/classify/write stage records, and return the first available stage.

#### Scenario: Start from a source set
- **WHEN** valid source IDs are provided with a new idempotency key
- **THEN** Moss creates one running job with deterministic source membership and managed stage references

#### Scenario: Unknown source
- **WHEN** any requested source ID is unknown or forgotten
- **THEN** Moss returns `SOURCE_NOT_FOUND` and creates no job

#### Scenario: Retry start
- **WHEN** the same start request is retried with the same idempotency key
- **THEN** Moss returns the original job without creating another job

### Requirement: Expose the next stage
`compile.next` SHALL return only the next valid stage for a job, including its schema file, input files, result file, and stage status, without allowing the caller to select an arbitrary stage.

#### Scenario: Extract is available
- **WHEN** a newly created running job is queried
- **THEN** `compile.next` returns the extract stage and its managed file references

#### Scenario: No stage is available
- **WHEN** a job is applied, aborted, failed, or waiting for a submitted result
- **THEN** `compile.next` returns the current state without advancing it

### Requirement: Validate and submit stage results
`compile.submit` SHALL validate source-set references, extraction provenance, fact operations, article operations, sensitivity, managed paths, stage order, and bounded sizes before advancing a job.

#### Scenario: Valid multi-source result
- **WHEN** a stage result references only job sources and satisfies its schema
- **THEN** Moss records its result hash and advances the job exactly once

#### Scenario: Invalid result
- **WHEN** a result is malformed, oversized, out of order, or references an external source
- **THEN** Moss returns a stable validation error and leaves the current stage available

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

