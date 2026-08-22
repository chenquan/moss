# source-sensitivity Specification

## Purpose
Keep source privacy changes consistent across SQLite control state, managed Wiki projections, and derived records.
## Requirements
### Requirement: Mark an active source sensitivity
Moss SHALL implement `source.mark_sensitive` as an idempotent machine mutation that accepts one active source ID and one supported sensitivity (`normal`, `sensitive`, or `restricted`), returns previous/new metadata and bounded propagation IDs, and never returns source content.

#### Scenario: Tighten source privacy
- **WHEN** the Skill marks an active normal source as sensitive or restricted
- **THEN** Moss updates the source atomically, propagates the stricter rank to linked active articles/actions that are less sensitive, and records an audit event

When an article is tightened, Moss SHALL create a new managed article projection version and update its Markdown hash, current metadata, citations, and search projection in the same logical mutation.

#### Scenario: Tighten linked article without Wiki drift
- **WHEN** a linked article is less sensitive than the newly requested source rank and its managed Markdown is current
- **THEN** Moss creates the next article version with the stricter frontmatter and keeps materialization, catalog, and search consistent

#### Scenario: Lower source privacy with confirmation
- **WHEN** the Skill requests a lower sensitivity with `confirmed: true`
- **THEN** Moss updates only the source while preserving stricter derived article/action classifications and returns the affected metadata

#### Scenario: Missing confirmation for lowering
- **WHEN** the requested rank is lower than the current rank and `confirmed` is false or absent
- **THEN** Moss returns `CONFIRMATION_REQUIRED` without changing the source or derived records

#### Scenario: Same value or forgotten source
- **WHEN** the requested value equals the current value, or the source is missing/forgotten
- **THEN** Moss returns an idempotent unchanged response or `SOURCE_NOT_FOUND` respectively, without exposing Raw content

#### Scenario: Invalid sensitivity
- **WHEN** the request contains an unsupported sensitivity value or empty source ID
- **THEN** Moss returns `SENSITIVITY_INVALID` or `REQUEST_INVALID` without opening a write transaction
