## MODIFIED Requirements

### Requirement: Track article versions and citations
Moss SHALL store every article projection version, source citations, affected fact versions, content hashes, and the relationship between the projection and its compile plan.

#### Scenario: Version recorded after batch apply
- **WHEN** a confirmed batch plan applies multiple article projections
- **THEN** every resulting article version and citation is persisted consistently with the batch state

#### Scenario: Batch citation outside sources
- **WHEN** any article operation cites a source outside the compile source set
- **THEN** the batch is rejected before any article mutation
