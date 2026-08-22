## MODIFIED Requirements

### Requirement: Create a backward-compatible source-set compile job
`compile.start` SHALL accept either one legacy `source_id` or a non-empty `source_ids` array, normalize both to a sorted unique source set, and create an idempotent resumable job.

#### Scenario: Start a multi-source job
- **WHEN** valid source IDs are provided
- **THEN** Moss creates one job with all source memberships and exposes the extract stage with bounded managed inputs

#### Scenario: Preserve legacy start
- **WHEN** a request provides only `source_id`
- **THEN** Moss creates the same observable single-source job semantics as v1

### Requirement: Produce multiple article and fact operations
The write stage SHALL support an ordered collection of article operations and fact operations, with each referenced source belonging to the job.

#### Scenario: Multi-article output
- **WHEN** a valid write result contains multiple create or update operations
- **THEN** preview includes every operation in one batch plan

#### Scenario: Foreign source reference
- **WHEN** any extraction, fact, article, or citation references a source outside the job
- **THEN** submission fails with a stable source-reference error and changes no authoritative state

### Requirement: Apply a batch plan atomically
`compile.apply` SHALL apply all article and fact items from a confirmed, unexpired batch plan only when every recorded base version/hash remains current.

#### Scenario: Confirmed batch apply
- **WHEN** all batch inputs are current and confirmation is explicit
- **THEN** Moss updates managed files, SQLite state, citations, facts, and indexes as one recoverable operation

#### Scenario: One stale item
- **WHEN** any article file, article version, or fact version changed after preview
- **THEN** Moss rejects the entire batch with a stale error and applies no item
