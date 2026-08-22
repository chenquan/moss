# knowledge-retrieval Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Return a deterministic local catalogue
Moss SHALL implement `knowledge.catalog` as a read-only operation that returns managed article metadata and deterministic topic/index entries without article bodies, remote data, or model-generated text.

#### Scenario: Catalogue normal articles
- **WHEN** the Skill requests the catalogue without sensitive permission
- **THEN** Moss returns normal managed articles ordered by stable article ID/slug, including path, current version, sensitivity, hash, and topic tags

#### Scenario: Catalogue filters sensitive articles
- **WHEN** the catalogue contains sensitive or restricted articles and `allow_sensitive` is absent or false
- **THEN** those articles and their content-derived topic entries are omitted without exposing title, path, or snippets

### Requirement: Select bounded ranked candidates locally
Moss SHALL implement `knowledge.candidates` as a bounded deterministic local search over the maintained FTS5 index, preserving article IDs, scores, summaries/snippets, versions, drift flags, and source references; it SHALL retain a safe substring fallback for unsupported queries.

#### Scenario: Query finds relevant articles
- **WHEN** Claude submits a non-empty query and result limit
- **THEN** Moss ranks permitted indexed articles deterministically and returns no more than the limit

#### Scenario: Index unavailable
- **WHEN** the FTS5 index is missing or unhealthy
- **THEN** Moss returns a stable maintenance error or uses the bounded safe fallback without exposing unverified content

### Requirement: Materialize a verified complete article
Moss SHALL implement `knowledge.materialize` as a read-only operation that resolves a selected managed article, verifies its current file hash and path, and returns the complete bounded Markdown content with metadata and source citations.

#### Scenario: Materialize selected article
- **WHEN** Claude selects an existing article ID or slug whose current file matches the managed hash
- **THEN** Moss returns the complete current article, version, path, and citations for Claude to use in an answer

#### Scenario: Missing or drifted article
- **WHEN** the selected article file is missing, symlinked, outside the managed Wiki, or differs from its recorded hash
- **THEN** Moss returns `WIKI_DRIFT` and no article content

#### Scenario: Materialize sensitive article
- **WHEN** the selected article is sensitive/restricted without `options.allow_sensitive=true`
- **THEN** Moss returns `SENSITIVITY_DENIED` and no content or source locators

### Requirement: Expose version history and provenance
Moss SHALL implement `knowledge.history` as a read-only operation that returns bounded article version metadata, content hashes, timestamps, compile job IDs, and citations, with optional bounded historical content only when explicitly requested and permitted.

#### Scenario: Read article history
- **WHEN** Claude requests history for an existing article
- **THEN** Moss returns versions in deterministic newest-first order and preserves the source citations attached to each version

#### Scenario: Read historical content
- **WHEN** `include_content=true` is requested for an article version within the size limit
- **THEN** Moss returns the SQLite-recorded version content only after verifying its stored hash, without reading an arbitrary filesystem path

#### Scenario: Unknown article or denied sensitivity
- **WHEN** history references an unknown article or a sensitive article without permission
- **THEN** Moss returns `ARTICLE_NOT_FOUND` or `SENSITIVITY_DENIED` without partial content

