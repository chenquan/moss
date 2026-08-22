## ADDED Requirements

### Requirement: Back up extraction artifacts
Backup export and restore SHALL include the managed extraction artifact directory and preserve the SQLite references that point to those artifacts.

#### Scenario: Restore provenance
- **WHEN** a backup containing extraction artifacts and SQLite references is restored
- **THEN** the artifact files and referenced extraction rows are both available
