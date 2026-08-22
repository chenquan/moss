## MODIFIED Requirements

### Requirement: Select bounded ranked candidates locally
Moss SHALL implement `knowledge.candidates` as a bounded deterministic local search over the maintained FTS5 index, preserving article IDs, scores, summaries/snippets, versions, drift flags, and source references; it SHALL retain a safe substring fallback for unsupported queries.

#### Scenario: Query finds relevant articles
- **WHEN** Claude submits a non-empty query and result limit
- **THEN** Moss ranks permitted indexed articles deterministically and returns no more than the limit

#### Scenario: Index unavailable
- **WHEN** the FTS5 index is missing or unhealthy
- **THEN** Moss returns a stable maintenance error or uses the bounded safe fallback without exposing unverified content
