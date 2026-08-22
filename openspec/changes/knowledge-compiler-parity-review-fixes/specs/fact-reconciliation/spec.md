## MODIFIED Requirements

### Requirement: Version fact retractions
A forget or reconciliation retraction SHALL append a new `fact_versions` row with `retracted` status and update the current fact pointer, preserving all previous versions and their history.

#### Scenario: Forget a fact
- **WHEN** a confirmed forget plan retracts a current fact
- **THEN** a new retracted version is recorded and the prior version remains queryable

### Requirement: Expose fact freshness
Compile context SHALL expose each fact's freshness state, including `stale`, so downstream reconciliation cannot mistake stale facts for current facts.

#### Scenario: Compile with stale fact
- **WHEN** a source revision marks a dependent fact stale
- **THEN** compile context includes the fact with an explicit stale freshness value
