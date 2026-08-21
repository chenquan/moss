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
Moss SHALL implement `knowledge.candidates` as a bounded deterministic local filter over managed current articles, returning article IDs, scores, summaries/snippets, versions, drift flags, and source references but not complete article bodies.

#### Scenario: Query finds relevant articles
- **WHEN** Claude submits a non-empty query and a result limit
- **THEN** Moss ranks title/slug/tag/summary/body matches with fixed scoring and resolves ties deterministically, returning no more than the requested limit

#### Scenario: Empty or topic query
- **WHEN** Claude submits an empty query or a topic filter
- **THEN** Moss returns catalogue-order candidates matching the topic without inventing an answer

#### Scenario: Candidate file drift
- **WHEN** an article's current file is missing, symlinked, or hash-mismatched
- **THEN** Moss marks that candidate as drifted and does not return unverified body text as a snippet

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

