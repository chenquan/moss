# local-storage Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: User-home data layout
Moss SHALL initialize its default data root at `~/.moss` and SHALL create separate database, Raw, Wiki, Job, response, backup, trash, and lock areas with permissions restricted to the current user. An explicit `MOSS_DATA_DIR` environment override MAY select another data root for controlled runtime use. Existing `~/.cairn` data is not automatically migrated or read.

#### Scenario: First initialization
- **WHEN** a supported operation runs against a missing data root
- **THEN** Moss creates the required layout, initializes SQLite, and records the schema version before reporting healthy

#### Scenario: Test data root
- **WHEN** the internal `MOSS_DATA_DIR` override points to an empty writable temporary directory
- **THEN** all database and filesystem writes remain inside that directory

### Requirement: SQLite integrity foundation
Moss SHALL use additive schema migration to add extraction, fact, source-lineage, batch-plan, and FTS5 index state while preserving v4 Raw, Source, Article, and Plan data.

#### Scenario: Upgrade existing v4 data
- **WHEN** a v4 database is opened by the new runtime
- **THEN** Moss migrates it to the new schema without deleting Raw, Wiki, source, or article records

#### Scenario: Incomplete migration
- **WHEN** a required migration or index structure cannot be validated
- **THEN** health reports an unhealthy/recovery state and mutating operations are refused

### Requirement: Durable managed artifacts
Managed files SHALL be written through temporary files and atomic renames, SHALL use restrictive permissions, and SHALL leave deterministic staging markers for recovery after an interrupted process.

#### Scenario: Durable artifact write
- **WHEN** Moss stores a Raw file or response content file
- **THEN** it writes a durable temporary file inside the managed root and atomically moves it into its final location

#### Scenario: Interrupted staging
- **WHEN** a previous process left a staging marker or temporary managed file
- **THEN** `system.health` detects it and reports recovery state without silently deleting user content
