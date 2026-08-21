## 1. Protocol and backup model

- [x] 1.1 Add export/restore request models, mutating classification, capabilities, and app dispatch.
- [x] 1.2 Define versioned backup manifest, private archive path validation, and deterministic ZIP entry rules.
- [x] 1.3 Add backup/restore audit and idempotency response handling without exposing personal content.

## 2. Backup implementation

- [x] 2.1 Implement SQLite-consistent `system.export` snapshots with managed Raw/Wiki/Jobs/trash files and SHA-256 manifest entries.
- [x] 2.2 Verify archive integrity, traversal/symlink/duplicate protections, schema compatibility, and bounded resource limits.
- [x] 2.3 Add restore staging, automatic pre-restore backup, swap/reopen health checks, and recovery markers.

## 3. Skill bootstrap and upgrade

- [x] 3.1 Extend the Cairn Skill with Claude-only bootstrap, trusted binary/Skill installation, handshake, and health routing.
- [x] 3.2 Extend the Skill with export-before-upgrade, migration preflight, failure confirmation, and restore recovery workflow.

## 4. Tests and validation

- [x] 4.1 Add unit tests for archive paths, manifests, traversal rejection, hash checks, and schema compatibility.
- [x] 4.2 Add integration tests for export, idempotent retry, confirmed restore, tampered archive, recovery marker, and Skill wording.
- [x] 4.3 Run tests, race tests, vet, build, strict OpenSpec validation, whitespace checks, verify scenarios, and archive the change.
