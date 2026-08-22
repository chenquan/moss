## MODIFIED Requirements

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

### Requirement: Validate and submit stage results
`compile.submit` SHALL validate source-set references, extraction provenance, fact operations, article operations, sensitivity, managed paths, stage order, and bounded sizes before advancing a job.

#### Scenario: Valid multi-source result
- **WHEN** a stage result references only job sources and satisfies its schema
- **THEN** Moss records its result hash and advances the job exactly once

#### Scenario: Invalid result
- **WHEN** a result is malformed, oversized, out of order, or references an external source
- **THEN** Moss returns a stable validation error and leaves the current stage available
