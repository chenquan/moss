## ADDED Requirements

### Requirement: Protocol handshake
`system.handshake` SHALL report the CLI version, supported protocol range, Skill compatibility, and supported operation names without changing user data.

#### Scenario: Compatible Skill
- **WHEN** the actor identifies a compatible Cairn Skill version
- **THEN** the response reports `ok: true` and the negotiated protocol information

#### Scenario: Incompatible Skill
- **WHEN** the Skill version is outside the CLI compatibility range
- **THEN** the response reports a stable compatibility error and no mutation occurs

### Requirement: Health validation
`system.health` SHALL validate the data root, required directory permissions, SQLite availability and integrity, schema version, and unresolved staging markers.

#### Scenario: Healthy installation
- **WHEN** all required checks pass
- **THEN** the response reports component statuses and an overall healthy result

#### Scenario: Failed storage check
- **WHEN** any required directory, database, schema, or recovery check fails
- **THEN** the response reports `STORAGE_UNHEALTHY` with machine-readable component details and marks mutations unavailable

### Requirement: Capability discovery
`system.capabilities` SHALL return a deterministic list of operation names, protocol range, supported source types, and implementation version.

#### Scenario: Capability request
- **WHEN** a compatible caller requests capabilities
- **THEN** the response contains a stable, sorted capability list suitable for Skill routing

#### Scenario: Capability response is read-only
- **WHEN** `system.capabilities` executes
- **THEN** no database, Raw, Wiki, Job, or audit mutation is produced
