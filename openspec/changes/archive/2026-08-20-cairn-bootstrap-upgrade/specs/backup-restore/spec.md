## ADDED Requirements

### Requirement: Export a verified local backup
Cairn SHALL implement `system.export` as an idempotent machine operation that creates a private ZIP backup containing a SQLite-consistent snapshot, managed Raw/Wiki/Jobs/trash files, a schema version, and per-file SHA-256 manifest entries without returning personal content.

#### Scenario: Export healthy state
- **WHEN** the Skill calls `system.export` against healthy storage
- **THEN** Cairn publishes one archive below the private backup directory and returns its backup ID, path, schema version, file count, and byte count

#### Scenario: Export retry
- **WHEN** the Skill retries an identical export request with the same idempotency key
- **THEN** Cairn returns the original archive response without creating a second archive

#### Scenario: Unhealthy export
- **WHEN** storage health reports unresolved recovery or integrity failure
- **THEN** Cairn refuses `system.export` with `STORAGE_UNHEALTHY` and does not publish an archive

### Requirement: Restore only a validated, confirmed archive
Cairn SHALL implement `system.restore` with an archive path confined to the private backup directory, explicit `confirmed: true`, traversal/symlink/hash/integrity validation, schema compatibility checks, and an automatic pre-restore backup before replacing managed state.

#### Scenario: Confirmed restore
- **WHEN** the Skill confirms a valid compatible archive
- **THEN** Cairn creates a pre-restore backup, stages and verifies the archive, swaps the database and managed trees, runs health checks, and records an auditable idempotent response

#### Scenario: Missing confirmation
- **WHEN** `system.restore` omits or sets `confirmed` to false
- **THEN** Cairn returns `CONFIRMATION_REQUIRED` without changing the active database or files

#### Scenario: Tampered or unsafe archive
- **WHEN** the archive has a hash mismatch, unsupported schema, traversal path, symlink, duplicate entry, or failed SQLite integrity check
- **THEN** Cairn returns a stable validation error and leaves active state unchanged

#### Scenario: Restore crash boundary
- **WHEN** a filesystem or database failure occurs after restore staging begins
- **THEN** Cairn leaves a private recovery marker and old state sufficient for recovery, and `system.health` blocks future mutations
