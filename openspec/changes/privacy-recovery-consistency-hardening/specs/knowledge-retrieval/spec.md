# Knowledge Retrieval Hardening

## MODIFIED Requirements

### Requirement: History must enforce per-version sensitivity

`knowledge.history` SHALL validate the sensitivity declared by every requested article version. Without explicit sensitive-content permission, it SHALL reject a result containing any non-normal version.

#### Scenario: Restricted historical version is not exposed

- **WHEN** the current article is normal but a requested historical version is restricted and `allow_sensitive` is absent
- **THEN** the operation returns `SENSITIVITY_DENIED` without returning that version's content or metadata

