## ADDED Requirements

### Requirement: Persist explicit typed relationships

Moss SHALL persist only explicit relationships whose type is one of `supports`, `contradicts`, `supersedes`, `depends_on`, `produces`, or `resulted_in`, with typed endpoints and optional endpoint versions.

#### Scenario: Store a relation in a compile batch

- **WHEN** a valid write result contains a relation between live entities
- **THEN** the relation is included in the pending compile plan and is persisted only when the confirmed compile plan is applied

#### Scenario: Reject inferred conflict

- **WHEN** two decision facts overlap in text or sources without an explicit relation entry
- **THEN** Moss stores no `contradicts` relation and returns no inferred conflict

### Requirement: Validate relationship endpoints and provenance

Moss SHALL reject unsupported endpoint types, unknown or forgotten entities, self-relations, duplicate tuples, stale versions, relations outside the compile job source set, and relations whose sensitivity exceeds the allowed request or target plan.

#### Scenario: Stale endpoint

- **WHEN** a relation references an article version, fact version, action revision, or action-result version that changed after preview
- **THEN** apply returns a stable stale-plan error and writes no relation

#### Scenario: Sensitive endpoint

- **WHEN** a relation references a sensitive or restricted source without explicit permission
- **THEN** preview/apply rejects the relation without returning unauthorized endpoint metadata

### Requirement: Apply and undo compile relationships atomically

Moss SHALL apply compile-created relationships in the same transaction as their associated article and fact changes, and SHALL remove only those relationships during an unchanged confirmed undo.

#### Scenario: Atomic compile apply

- **WHEN** a confirmed compile batch contains valid articles, facts, and relations
- **THEN** all records commit together or none of them become visible

#### Scenario: Relation apply failure

- **WHEN** any relation conflicts or fails validation during apply
- **THEN** the article, fact, and relation portions of the batch remain unchanged

#### Scenario: Undo an unchanged batch

- **WHEN** an applied compile batch is undone before any affected entity or relation changes
- **THEN** Moss removes the batch-created relations and restores the prior article/fact state atomically

### Requirement: Preserve relationship visibility and privacy

Moss SHALL expose relationships only through bounded review/context responses, filter forgotten or unauthorized endpoints, and preserve relation provenance for audit and later drill-down.

#### Scenario: Forgotten endpoint

- **WHEN** a source, action, article, or fact referenced by a relation is forgotten
- **THEN** normal relation results omit the relation and forget impact reporting includes it

#### Scenario: Explicit relationship query

- **WHEN** a permitted relation is included in a context bundle or review scan
- **THEN** the response includes its type, endpoint IDs/versions, and permitted provenance without inferring causality beyond the stored predicate
