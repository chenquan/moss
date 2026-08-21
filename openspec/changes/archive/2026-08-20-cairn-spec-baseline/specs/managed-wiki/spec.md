## ADDED Requirements

### Requirement: Generate deterministic managed Markdown
Cairn SHALL render a write-stage article candidate into deterministic Markdown with stable frontmatter ordering, escaped metadata, sorted tags and source references, and a final newline.

#### Scenario: New article rendering
- **WHEN** a valid write result has no article ID
- **THEN** preview contains a deterministic new article path and content with Cairn metadata and source citations

#### Scenario: Existing article rendering
- **WHEN** a valid write result targets an existing article ID
- **THEN** preview includes the current version and proposed version without mutating the existing file

### Requirement: Track article versions and citations
Cairn SHALL store article metadata, every managed content version, content hashes, source citations, sensitivity, and the relationship between the article version and its compile job.

#### Scenario: Version recorded after apply
- **WHEN** a plan applies a new or updated article
- **THEN** SQLite contains the resulting version and citations, and the Markdown hash matches the recorded version hash

#### Scenario: Citation points outside the job source
- **WHEN** a write result cites a source that is not part of the compile job
- **THEN** submission is rejected before any article or plan mutation

### Requirement: Detect managed Wiki drift
Cairn SHALL compare the current article file hash with the last managed version before preview, apply, status, or undo operations that read the article.

#### Scenario: External edit detected
- **WHEN** an article file differs from its recorded managed hash
- **THEN** the operation returns `WIKI_DRIFT`, does not overwrite the file, and marks mutation unavailable

#### Scenario: Article file missing
- **WHEN** a recorded article file is missing before an update or undo
- **THEN** the operation returns `WIKI_DRIFT` with a missing-file detail
