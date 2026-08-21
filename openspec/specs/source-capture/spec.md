# source-capture Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Immutable source ingestion
`source.ingest` SHALL accept a regular local file, copy its bytes into content-addressed Raw storage, calculate a SHA-256 content hash, and never modify or delete the original file.

#### Scenario: New source
- **WHEN** a readable regular file is ingested with a supported source type and sensitivity
- **THEN** Moss stores an immutable Raw Blob, creates a Source record, and returns `source_id`, `content_hash`, and `duplicate: false`

#### Scenario: Unsupported filesystem object
- **WHEN** the input path is a directory, device, socket, or rejected symlink
- **THEN** Moss returns `SOURCE_INPUT_INVALID` and leaves Raw storage and SQLite unchanged

### Requirement: Blob deduplication and source context
Moss SHALL deduplicate identical bytes by SHA-256 while preserving separate Source records when distinct ingestion requests carry different origin context.

#### Scenario: Same content ingested twice
- **WHEN** two valid source records contain identical bytes
- **THEN** Moss stores one Blob, returns `duplicate: true` for the later ingestion, and preserves both valid Source references when their request identities differ

#### Scenario: Same idempotency key retried
- **WHEN** the same ingestion request is retried with its original idempotency key
- **THEN** Moss returns the original Source result and does not create a second Source record

### Requirement: Source metadata retrieval
`source.get` SHALL return source metadata, hash, size, source type, sensitivity, and creation information without exposing the complete content unless an explicit managed content materialization option is requested.

#### Scenario: Existing source metadata
- **WHEN** `source.get` receives an existing `source_id`
- **THEN** it returns the source metadata and a verified content reference or hash

#### Scenario: Unknown source
- **WHEN** `source.get` receives an unknown `source_id`
- **THEN** it returns `SOURCE_NOT_FOUND` without creating a response content file

### Requirement: Source listing
`source.list` SHALL provide deterministic, bounded metadata listings with stable ordering and sensitivity-aware filtering.

#### Scenario: List normal sources
- **WHEN** a caller requests a source list without a sensitive-content permission in the request policy
- **THEN** Moss returns only permitted metadata in stable creation-time/ID order

#### Scenario: Empty result
- **WHEN** no source matches the requested filter
- **THEN** Moss returns an empty list with `ok: true`

