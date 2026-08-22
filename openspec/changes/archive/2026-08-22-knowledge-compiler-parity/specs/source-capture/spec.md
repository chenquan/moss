## MODIFIED Requirements

### Requirement: Immutable source ingestion
`source.ingest` SHALL accept a regular local file and an optional explicit `origin_key`, preserve immutable content-addressed storage, and never infer lineage from filesystem paths.

#### Scenario: Source with lineage
- **WHEN** a valid file is ingested with an origin key
- **THEN** Moss stores the source, key, content hash, and deterministic lineage revision

#### Scenario: Source without lineage
- **WHEN** a valid file is ingested without an origin key
- **THEN** Moss stores an independent source with no propagation relationship
