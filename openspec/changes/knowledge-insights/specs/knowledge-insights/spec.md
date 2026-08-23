## ADDED Requirements

### Requirement: Query bounded decision insights
Moss SHALL implement `knowledge.insights` as a read-only, deterministic operation that returns bounded current facts whose `kind` is `decision`, together with their version, status, freshness, and source citations.

#### Scenario: Return current decisions
- **WHEN** the Skill requests insights with no topic filter
- **THEN** Moss returns permitted current decision facts ordered by review priority and stable fact identifiers

#### Scenario: Filter decisions by topic
- **WHEN** the Skill supplies a non-empty topic
- **THEN** Moss returns only permitted decisions whose fact key or text matches the topic using the runtime's deterministic case-insensitive matching rules

#### Scenario: Apply a bounded limit
- **WHEN** the Skill supplies no limit or a limit within the supported range
- **THEN** Moss applies the default or requested limit and never returns more decision records than the maximum

### Requirement: Report review signals from existing lifecycle state
The operation SHALL derive review items only from authoritative fact, fact-version, extraction, and source state, including `retracted`, `superseded`, `stale`, and unavailable cited evidence; it SHALL not infer a contradiction relationship.

#### Scenario: Stale decision
- **WHEN** a current decision fact has `freshness = stale`
- **THEN** the response includes a `stale` review item with the decision ID and evidence of the stale state

#### Scenario: Superseded or retracted decision
- **WHEN** a decision's current or historical version is superseded or retracted
- **THEN** the response includes the corresponding lifecycle review signal and preserves the version identifiers needed for history lookup

#### Scenario: Explicit cross-fact supersession
- **WHEN** a decision version explicitly records `supersedes_fact_id` for another decision fact
- **THEN** the superseded decision includes a `superseded` review item identifying the successor fact and version

#### Scenario: No contradiction inference
- **WHEN** two decisions mention overlapping text or sources without an explicit persisted relationship
- **THEN** Moss does not return a `contradicts` relationship or claim that the decisions conflict

### Requirement: Expose explainable article and action associations
The operation SHALL associate a decision with current cited articles and actions only through explicit source IDs and SHALL label action associations as `shared_source` rather than causal relationships.

#### Scenario: Cited article association
- **WHEN** a current article version cites a source cited by a returned decision
- **THEN** the decision includes bounded article metadata and the shared source IDs without inlining article content

#### Scenario: Shared-source action association
- **WHEN** an action references a source cited by a returned decision
- **THEN** the decision includes bounded action metadata with `association = shared_source`

#### Scenario: No shared evidence
- **WHEN** an article or action has no source ID shared with the decision
- **THEN** Moss omits that article or action association

### Requirement: Enforce sensitivity and managed-content boundaries
The operation SHALL apply existing sensitivity authorization and forgotten-source filtering to every returned fact, citation, article association, and action association, and SHALL never return unverified article body content.

#### Scenario: Unauthorized sensitive decision
- **WHEN** a decision or its cited source is sensitive/restricted and `allow_sensitive` is false or absent
- **THEN** Moss omits the decision and all related citations, articles, and actions

#### Scenario: Authorized sensitive decision
- **WHEN** `allow_sensitive = true` is explicitly supplied
- **THEN** Moss may return the decision and permitted bounded metadata while preserving the sensitivity value

#### Scenario: Forgotten or drifted evidence
- **WHEN** a cited source is forgotten or an associated article no longer matches its managed hash
- **THEN** Moss excludes forgotten evidence and marks or omits drifted article metadata without returning article content

#### Scenario: Unavailable active extraction
- **WHEN** a cited extraction is marked `active` but its managed file is missing, unsafe, or has a mismatched content hash
- **THEN** Moss includes an `evidence_unavailable` review item and does not treat the extraction as verified evidence

### Requirement: Preserve protocol and capability behavior
The operation SHALL be advertised by `system.capabilities`, routed through the existing stdio dispatcher, and remain read-only and free of idempotency requirements.

#### Scenario: Capability discovery
- **WHEN** a compatible Skill requests system capabilities
- **THEN** the response lists `knowledge.insights` with `mutating = false`

#### Scenario: Valid insight request
- **WHEN** a compatible Skill submits a valid `knowledge.insights` request
- **THEN** Moss returns exactly one structured response without changing SQLite, managed files, indexes, plans, or audit records

#### Scenario: Invalid insight arguments
- **WHEN** the request contains an unknown argument, invalid limit, or malformed topic
- **THEN** Moss returns a stable request validation error and performs no state change
