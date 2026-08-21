# local-storage Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: User-home data layout
Moss SHALL initialize its default data root at `~/.cairn` for upgrade compatibility and SHALL create separate database, Raw, Wiki, Job, response, backup, trash, and lock areas with permissions restricted to the current user. An explicit `MOSS_DATA_DIR` environment override MAY select another data root for controlled runtime use; `CAIRN_DATA_DIR` remains a legacy alias.

#### Scenario: First initialization
- **WHEN** a supported operation runs against a missing data root
- **THEN** Moss creates the required layout, initializes SQLite, and records the schema version before reporting healthy

#### Scenario: Test data root
- **WHEN** the internal `MOSS_DATA_DIR` override points to an empty writable temporary directory
- **THEN** all database and filesystem writes remain inside that directory

### Requirement: SQLite integrity foundation
Moss SHALL use SQLite with foreign keys enabled, WAL mode, an application schema version, and an integrity check exposed to health validation.

#### Scenario: Healthy database
- **WHEN** the database opens, migrations are current, and the integrity check succeeds
- **THEN** `system.health` reports the storage component healthy

#### Scenario: Corrupt or incompatible database
- **WHEN** SQLite integrity fails or the schema version is unsupported
- **THEN** `system.health` reports `STORAGE_UNHEALTHY` and mutating operations are refused

### Requirement: Durable managed artifacts
Managed files SHALL be written through temporary files and atomic renames, SHALL use restrictive permissions, and SHALL leave deterministic staging markers for recovery after an interrupted process.

#### Scenario: Durable artifact write
- **WHEN** Moss stores a Raw file or response content file
- **THEN** it writes a durable temporary file inside the managed root and atomically moves it into its final location

#### Scenario: Interrupted staging
- **WHEN** a previous process left a staging marker or temporary managed file
- **THEN** `system.health` detects it and reports recovery state without silently deleting user content
