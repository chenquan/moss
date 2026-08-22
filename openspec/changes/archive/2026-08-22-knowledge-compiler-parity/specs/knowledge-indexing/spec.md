## MODIFIED Requirements

### Requirement: Maintain a deterministic FTS5 article index
Moss SHALL maintain a local SQLite FTS5 index for current permitted managed articles and update it with article application or explicit reindex operations.

#### Scenario: Search indexed articles
- **WHEN** `knowledge.candidates` receives a non-empty query
- **THEN** it returns bounded candidates ranked by FTS relevance with stable tie-breaking and existing citation metadata

#### Scenario: Reindex after drift repair
- **WHEN** `knowledge.reindex` is requested
- **THEN** Moss rebuilds the derived index idempotently without changing Raw, Wiki, facts, or article versions

### Requirement: Protect index responses
The index SHALL not expose sensitive content without permission and SHALL not return unverified content from drifted articles.

#### Scenario: Drifted indexed article
- **WHEN** an indexed article no longer matches its managed hash
- **THEN** candidate output marks it drifted or omits its snippet and never returns unverified body content
