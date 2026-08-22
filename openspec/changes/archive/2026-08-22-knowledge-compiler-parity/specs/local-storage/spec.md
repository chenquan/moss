## MODIFIED Requirements

### Requirement: SQLite integrity foundation
Moss SHALL use additive schema migration to add extraction, fact, source-lineage, batch-plan, and FTS5 index state while preserving v4 Raw, Source, Article, and Plan data.

#### Scenario: Upgrade existing v4 data
- **WHEN** a v4 database is opened by the new runtime
- **THEN** Moss migrates it to the new schema without deleting Raw, Wiki, source, or article records

#### Scenario: Incomplete migration
- **WHEN** a required migration or index structure cannot be validated
- **THEN** health reports an unhealthy/recovery state and mutating operations are refused
