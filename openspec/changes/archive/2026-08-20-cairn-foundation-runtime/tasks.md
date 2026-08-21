## 1. Go module and protocol foundation

- [x] 1.1 Create the Go module, command layout, version constants, and a pure-Go SQLite dependency suitable for the macOS release path.
- [x] 1.2 Define request, actor, options, response, warning, error, capability, and protocol-version types with strict JSON decoding.
- [x] 1.3 Implement the `call --request --response` argument parser, protocol validation, stable exit semantics, empty stdout behavior, and atomic response writer.
- [x] 1.4 Implement request-scoped managed-path validation, traversal/symlink checks, permission checks, and bounded request/response file handling.
- [x] 1.5 Implement the idempotency store and fingerprint comparison so identical retries return the original response and conflicting keys fail deterministically.

## 2. Local storage and recovery

- [x] 2.1 Implement the default Cairn data-root resolver and `CAIRN_DATA_DIR` test override without introducing user-facing CLI flags.
- [x] 2.2 Implement directory creation and restrictive permissions for database, Raw, Wiki, Jobs, responses, backups, trash, locks, and staging areas.
- [x] 2.3 Implement SQLite opening, WAL/foreign-key/busy-timeout configuration, schema versioning, migrations, and integrity checks.
- [x] 2.4 Add metadata, blobs, sources, idempotency, and audit tables with indexes and foreign-key constraints.
- [x] 2.5 Implement durable temporary-file writes, content-addressed Raw paths, staging markers, and health-time recovery/quarantine handling.

## 3. System operations

- [x] 3.1 Implement `system.handshake` with protocol range, Skill compatibility, CLI version, and actor validation.
- [x] 3.2 Implement `system.capabilities` with deterministic sorted operation names, supported source types, and protocol information.
- [x] 3.3 Implement `system.health` with component checks for directories, permissions, SQLite integrity, schema version, and unresolved staging markers.
- [x] 3.4 Add unit and integration tests for healthy, missing, incompatible, corrupt, and recovery-required system states.

## 4. Source capture operations

- [x] 4.1 Implement regular-file validation, rejected filesystem object handling, SHA-256 hashing, and immutable Raw Blob ingestion.
- [x] 4.2 Implement Source records, Blob deduplication, redacted origin metadata, sensitivity/source-type validation, and audit events.
- [x] 4.3 Implement `source.get` metadata retrieval, managed Raw references/hashes, unknown-source errors, and sensitivity-aware response shaping.
- [x] 4.4 Implement bounded deterministic `source.list` filtering and stable ordering.
- [x] 4.5 Add tests for new ingestion, duplicate content, idempotent retry, conflicting idempotency, invalid paths, unknown IDs, and list filters.

## 5. Claude Code Skill

- [x] 5.1 Add the `.claude/skills/cairn/SKILL.md` source package with automatic invocation, `user-invocable: false`, and a concise natural-language routing contract.
- [x] 5.2 Add Skill instructions for handshake/health and source capture using request/response files, managed content references, and no user-content shell interpolation.
- [x] 5.3 Add Skill fixture checks that verify required operation names, CLI-only invocation, compatibility failure handling, and absence of human CLI promises.

## 6. End-to-end validation and documentation

- [x] 6.1 Add protocol fixture tests covering success, business errors, transport failures, atomic responses, stable error codes, and stdout/stderr discipline.
- [x] 6.2 Add an end-to-end test that initializes an isolated data root, ingests a source, retrieves metadata, lists it, and verifies Raw bytes and SQLite state.
- [x] 6.3 Run `go test ./...`, `go vet ./...`, `openspec status --change "cairn-foundation-runtime"`, and `git diff --check` (or the repository-equivalent checks when Git metadata is available).
- [x] 6.4 Review the implementation against every scenario in the five delta specs and record any design adjustment before verification.
