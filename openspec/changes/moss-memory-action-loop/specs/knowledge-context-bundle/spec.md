## ADDED Requirements

### Requirement: Assemble an evidence-oriented context bundle

Moss SHALL implement read-only `knowledge.context.bundle` that deterministically combines bounded permitted articles, facts/decisions, relations, source citations, review signals, actions, and action results for a query or topic.

#### Scenario: Build a context bundle

- **WHEN** the Skill requests a permitted topic with a bounded limit
- **THEN** Moss returns deduplicated, stable-sorted metadata and provenance across the selected knowledge entities without generating an answer

#### Scenario: No matching knowledge

- **WHEN** no permitted article, fact, relation, action, or result matches
- **THEN** Moss returns an empty bounded bundle with count metadata and no unrelated records

### Requirement: Return managed references instead of unverified bodies

The bundle SHALL return article path, version, hash, sensitivity, and citations, but SHALL NOT inline article body content by default; the Skill SHALL use `knowledge.materialize` for verified body retrieval.

#### Scenario: Article drift

- **WHEN** an associated managed article fails path or hash verification
- **THEN** the bundle returns metadata with a drift/evidence warning and no article body

#### Scenario: Verified drill-down

- **WHEN** the Skill selects an article reference from the bundle
- **THEN** it can call the existing materialize operation to obtain the complete verified article or a stable error

### Requirement: Preserve privacy and boundedness

The bundle SHALL apply sensitivity filtering independently to every entity family, cap fan-out and total response size, preserve explicit relation predicates, and omit forgotten or unauthorized records.

#### Scenario: Sensitive association

- **WHEN** a permitted fact is associated with a sensitive article or action
- **THEN** the unauthorized association is omitted without leaking its title, path, details, or source locator

#### Scenario: Stable limits

- **WHEN** the query would produce more records than the configured caps
- **THEN** Moss returns deterministic truncation metadata and stable ordering rather than an unbounded response
