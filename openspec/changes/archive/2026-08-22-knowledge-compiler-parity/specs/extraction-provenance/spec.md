## MODIFIED Requirements

### Requirement: Persist immutable extraction provenance
Moss SHALL persist every accepted extraction with its source hash, extraction hash, extractor version, prompt hash, schema version, strategy, managed path, and status.

#### Scenario: Accept extraction
- **WHEN** an extract stage passes schema, source, size, and sensitivity validation
- **THEN** Moss records an immutable extraction artifact and its provenance before advancing the job

#### Scenario: Reuse fresh extraction
- **WHEN** source hash and all extraction fingerprint inputs match an active extraction
- **THEN** Moss marks the extraction reusable without requiring a new model result

### Requirement: Mark stale extraction deterministically
Moss SHALL mark an extraction stale when any source or pipeline fingerprint input differs, without silently treating it as current.

#### Scenario: Prompt changes
- **WHEN** the prompt hash changes for an otherwise identical source
- **THEN** the previous extraction remains historical and a new compile is required
