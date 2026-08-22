# fact-reconciliation Specification

## Purpose
Define source-backed fact versioning and safe retraction.
## Requirements
### Requirement: Track versioned source-backed facts
Moss SHALL store fact versions with active, superseded, or retracted status and source citations, extraction identity, and optional supersession links.

#### Scenario: Supersede a fact
- **WHEN** a reviewed write result replaces an existing fact
- **THEN** Moss preserves the old version and records the new version as superseding it

#### Scenario: Retract a fact
- **WHEN** a reviewed source revision or forget plan retracts a fact
- **THEN** Moss records a retracted version and does not delete the historical evidence

### Requirement: Use explicit source lineage
`source.ingest` SHALL accept an optional `origin_key`; only sources with the same explicit key participate in source-version propagation.

#### Scenario: Same origin key
- **WHEN** a new source is ingested with an existing origin key
- **THEN** Moss assigns the next lineage revision and marks dependent extraction/facts stale

#### Scenario: No origin key
- **WHEN** a source is ingested without an origin key
- **THEN** Moss treats it as an independent source and performs no inferred propagation

