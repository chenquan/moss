## Context

Cairn is a greenfield macOS personal knowledge assistant. Claude Code is the only user interface; the Go runtime is an opaque local execution component invoked through one file-based command. The workspace currently contains no application code or database, so this change establishes the first durable boundary without needing a data migration.

The runtime must work offline, run as a short-lived process, preserve user source files, and remain safe when multiple Claude sessions invoke it concurrently. Markdown Wiki content is managed by Cairn and is not an externally editable input in v1. Compilation, retrieval, actions, forgetting, backup, and upgrade workflows will build on these seams in later changes.

## Goals / Non-Goals

**Goals:**

- Establish a versioned JSON request/response protocol for `cairn-cli call`.
- Provide a user-scoped, testable SQLite and filesystem layout.
- Make source ingestion immutable, content-addressed, idempotent, and auditable.
- Provide deterministic handshake, health, and capability discovery.
- Ship a minimal Claude Code Skill that routes natural-language capture and maintenance requests to the machine protocol.
- Keep large content in managed files and return only references in JSON.

**Non-Goals:**

- No human-facing subcommands, interactive prompts, terminal formatting, Web UI, MCP server, daemon, or second model.
- No compilation stages, Markdown article generation, full-text knowledge retrieval, action ledger, forget plans, backup export, upgrade installer, or external notifications.
- No application-layer encryption; rely on macOS user permissions for this change.
- No supported workflow for importing manual edits to Markdown Wiki files.

## Decisions

### One machine entrypoint

The binary exposes only `call` with required `--request` and `--response` paths. The request is parsed from a regular file, validated before any mutation, and the response is written to a temporary sibling and atomically renamed. Business success and business errors are represented in the response JSON; stdout remains empty and stderr is diagnostic-only.

Alternatives considered: a human CLI with many subcommands would duplicate the Skill contract; a long-running local server would add lifecycle, port, and authentication concerns without a product need.

### Request identity and retries

`request_id` identifies one transport attempt. Mutating requests also carry `idempotency_key`, which identifies the logical operation. A SQLite idempotency table stores the request fingerprint and serialized response. Reusing a key with a different fingerprint returns `IDEMPOTENCY_CONFLICT`; retrying the same key returns the original result.

### Data root and layout

The default data root is `~/Library/Application Support/Cairn`. Tests use the internal `CAIRN_DATA_DIR` environment override; no user-facing flag is added. The runtime creates only user-owned directories with restrictive permissions:

```text
<data-root>/
├── cairn.db
├── raw/sha256/<prefix>/<digest>
├── wiki/
├── jobs/
├── responses/
├── backups/
├── trash/
└── locks/
```

SQLite is the source of truth for metadata, idempotency, and audit records. Raw bytes are the source of truth for ingested content. Future Markdown article files will be managed output and must carry hashes in SQLite; this change only creates the boundary and does not write articles.

### SQLite implementation and concurrency

Use the pure-Go `modernc.org/sqlite` driver to avoid requiring CGO in the macOS release path. Open the database in WAL mode with a bounded busy timeout, foreign keys enabled, and an application schema version table. Short-lived processes may overlap; SQLite serializes mutations, while the source operation uses a staging file and a database transaction.

### Source and Blob model

`blobs` are deduplicated by SHA-256 and point to immutable Raw files. `sources` are ingestion records and may reference the same Blob more than once, preserving source context without duplicating bytes. A source record stores type, sensitivity, byte size, hash, creation time, and optional redacted origin metadata; it never grants Cairn permission to modify the original path.

### File boundary and large content

All response content files are created under a request-scoped directory in the data root. Paths in JSON are relative managed references, never arbitrary caller-selected destinations. The runtime rejects traversal, symlink escapes, non-regular files, and response paths outside the managed root. `source.get` returns metadata by default and materializes content only when explicitly requested by a later operation.

### Skill boundary

The repository ships `.claude/skills/cairn/SKILL.md` as the source package. It is automatically invocable by Claude but hidden from the user's slash-command menu with `user-invocable: false`. It instructs Claude to create request/response files, invoke only `cairn-cli call`, read response references, and never put user content into shell arguments. It does not contain business answers or a second model.

### Stable errors and health

Errors use stable machine codes such as `PROTOCOL_VERSION_UNSUPPORTED`, `REQUEST_INVALID`, `IDEMPOTENCY_CONFLICT`, `PATH_INVALID`, `SOURCE_NOT_FOUND`, `STORAGE_UNHEALTHY`, and `INTERNAL_ERROR`. `system.handshake` validates protocol ranges and actor compatibility. `system.health` checks schema version, permissions, required directories, SQLite integrity, and staged-file recovery markers. `system.capabilities` returns the supported operation names and protocol range.

## Risks / Trade-offs

- **[Risk]** A process can commit SQLite state and crash before writing its response. → Persist the idempotency result in the same transaction; a retry returns the committed response.
- **[Risk]** A staged Raw file can remain after a crash. → Use deterministic staging names and have `system.health` reconcile or quarantine incomplete stages.
- **[Risk]** File and database commits cannot form a true cross-system transaction. → Source ingestion commits only after the staged file is durable and records recovery state for interrupted finalization.
- **[Risk]** A local path can reveal private project names. → Keep origin metadata optional and redacted; never include full paths in Markdown or normal responses.
- **[Risk]** SQLite write contention across Claude sessions can cause transient failures. → Use WAL, a bounded busy timeout, retry only safe transactional steps, and return a stable retryable error when the bound is exceeded.
- **[Risk]** A Skill may be installed before the binary is on PATH. → Keep the Skill command configurable by the installer contract; development tests may set `CAIRN_CLI` while the eventual installer provides the stable `cairn-cli` command.

## Migration Plan

This is an initial greenfield schema. First run creates the data root, database, schema version, and required directories atomically enough for health checks to recover. There is no existing data migration. If initialization fails, the runtime leaves a diagnostic staging marker and does not report healthy. Later schema changes must add explicit migrations and preserve the protocol envelope.

Rollback for this change is uninstalling the binary and Skill; the data root is preserved unless a future, explicitly confirmed purge operation removes it.

## Open Questions

No product decisions remain for this foundation change. Manual Wiki import, compiler adapters, backups, forget semantics, and installer packaging are deliberately deferred to later OpenSpec changes.
