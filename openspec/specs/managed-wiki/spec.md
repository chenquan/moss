# managed-wiki Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: Generate deterministic managed Markdown
Moss SHALL render a write-stage article candidate into deterministic Markdown with stable frontmatter ordering, escaped metadata, sorted tags and source references, and a final newline.

#### Scenario: New article rendering
- **WHEN** a valid write result has no article ID
- **THEN** preview contains a deterministic new article path and content with Moss metadata and source citations

#### Scenario: Existing article rendering
- **WHEN** a valid write result targets an existing article ID
- **THEN** preview includes the current version and proposed version without mutating the existing file

### Requirement: Track article versions and citations
Moss SHALL store every article projection version, source citations, affected fact versions, content hashes, and the relationship between the projection and its compile plan.

#### Scenario: Version recorded after batch apply
- **WHEN** a confirmed batch plan applies multiple article projections
- **THEN** every resulting article version and citation is persisted consistently with the batch state

#### Scenario: Batch citation outside sources
- **WHEN** any article operation cites a source outside the compile source set
- **THEN** the batch is rejected before any article mutation

### Requirement: Detect managed Wiki drift
Moss SHALL compare the current article file hash with the last managed version before preview, apply, status, or undo operations that read the article.

#### Scenario: External edit detected
- **WHEN** an article file differs from its recorded managed hash
- **THEN** the operation returns `WIKI_DRIFT`, does not overwrite the file, and marks mutation unavailable

#### Scenario: Article file missing
- **WHEN** a recorded article file is missing before an update or undo
- **THEN** the operation returns `WIKI_DRIFT` with a missing-file detail

