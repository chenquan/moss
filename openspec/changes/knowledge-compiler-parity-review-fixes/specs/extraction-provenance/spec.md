## MODIFIED Requirements

### Requirement: Map extraction artifacts one-to-one to source items
For a multi-source extraction, Moss SHALL require exactly one item for each Job source and persist each source item's own content, hash, and provenance. An aggregate payload SHALL NOT be stored as every source's artifact.

#### Scenario: Missing source item
- **WHEN** a multi-source extraction omits one Job source
- **THEN** submission fails and no incomplete extraction set is registered

### Requirement: Verify reusable extraction artifacts
An active extraction SHALL be reusable only when its managed path is safe, its artifact exists as a regular non-symlink file, and its content hash matches the recorded hash. Invalid artifacts SHALL be treated as unavailable/stale.

#### Scenario: Corrupted artifact
- **WHEN** an active extraction file is missing or its hash differs
- **THEN** Moss does not reuse it
