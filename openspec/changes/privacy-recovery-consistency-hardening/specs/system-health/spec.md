# Recovery and Mutation Safety

## ADDED Requirements

### Requirement: Interrupted managed mutations are recoverable

Moss SHALL serialize managed-file mutations per data root, retain a recovery journal after finalization begins, block further mutations while unresolved, and expose a controlled `system.recover` operation.

#### Scenario: Database failure after file replacement

- **WHEN** a managed article rename succeeds but the SQLite transaction does not commit
- **THEN** the recovery marker remains, `system.health` reports recovery-required, and `system.recover` can restore the journaled before state without overwriting an unrelated file

### Requirement: Restored provenance must be validated

`system.restore` SHALL verify that restored source blobs, current articles, and extraction artifacts exist within their managed roots and match their SQLite content hashes before reporting success.

#### Scenario: Restored article hash is invalid

- **WHEN** a restored article file does not match its recorded current hash
- **THEN** restore fails with `BACKUP_INVALID` and does not report healthy restored storage

