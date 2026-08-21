## Context

Cairn is a short-lived Go process invoked by the Claude Code Skill through request/response files. Its durable state spans SQLite plus managed Raw, Wiki, Jobs, and trash files. The current runtime already validates schema and health, but it has no stable archive format or restore transaction boundary. Installation and upgrades are orchestrated by Claude, so the CLI must expose only machine operations while the Skill owns user-facing confirmation and version routing.

## Goals / Non-Goals

**Goals:**

- Produce a private, self-describing backup archive with a SQLite-consistent snapshot and hashes for every managed file.
- Restore only a validated archive, require explicit Skill confirmation, create an automatic pre-restore backup, and leave a recovery marker across filesystem/SQLite failures.
- Preserve request idempotency and audit history for export and restore.
- Document Claude-driven bootstrap and upgrade sequencing, including handshake compatibility and migration preflight.

**Non-Goals:**

- Remote backup storage, encryption-key management, cloud sync, or secure hardware erasure.
- A human installer UI, a human-facing CLI, Web UI, MCP server, or a second model.
- Downloading arbitrary binaries from the network inside the Go runtime. The Skill may use a trusted release or local build source supplied by the installation environment.

## Decisions

### Versioned ZIP archive

`system.export` creates `<backup-id>.cairn-backup.zip` below the private `backups` directory. The archive contains `manifest.json`, a SQLite snapshot at `database/cairn.db`, and the managed `raw/`, `wiki/`, `jobs/`, and `trash/` trees. Manifest entries record relative path, byte size, and SHA-256. ZIP is chosen over a custom directory because it is portable, atomic to publish, and available in the Go standard library; a plain directory would expose partially written backups.

### SQLite snapshot and restore staging

Export uses SQLite `VACUUM INTO` while the source database is healthy, then hashes the snapshot and managed files before publishing the archive via a temporary file and rename. Restore extracts into a private staging directory, rejects absolute/traversal paths and symlinks, verifies the manifest and `PRAGMA integrity_check`, and accepts schema versions from 1 through the current version so the normal migration path can upgrade an older snapshot.

Before replacing active state, restore creates an automatic export of the current state. It writes a `restore-<id>.json` marker, closes the active database, swaps only the database and managed state trees, reopens the restored store, runs health checks, and records audit/idempotency there. Any failure leaves the marker and old state in a recoverable staging location; `system.health` blocks further mutation until the operator/Skill resolves it.

### Path and confirmation boundary

Backup paths are either generated or simple paths inside `Paths.Backups`; restore never reads outside that directory. Export is safe to run automatically. Restore requires `confirmed: true` in the request; Claude is responsible for showing the archive, compatibility, and recovery implications before sending it.

### Bootstrap and upgrade are Skill workflows

The Skill installation flow obtains a trusted matching binary and embedded/versioned Skill resources, installs them into private application locations, and immediately calls `system.handshake` and `system.health`. Upgrade first calls `system.export`, checks that the new binary advertises a compatible protocol and can open the data root in a migration preflight, then replaces binary/Skill resources. If health fails, the Skill calls `system.restore` on the automatic pre-upgrade archive after user confirmation. No new human command surface is introduced.

## Risks / Trade-offs

- **[Risk]** Files may change while they are copied after the SQLite snapshot. → Export records hashes and sizes in the manifest; restore refuses a tampered archive, and mutating operations remain health-gated.
- **[Risk]** A crash during directory/database replacement can leave mixed state. → Keep a private staging marker and old state until the restored store passes health; block mutation while unresolved.
- **[Risk]** ZIP archives can contain malicious paths or symlinks. → Reject traversal, absolute paths, duplicate entries, symlinks, and files outside the allowlisted roots before extraction.
- **[Risk]** Older snapshots may require migrations unavailable to a future binary. → Require schema version <= supported, report the version in the response, and let the Skill choose a compatible binary before restore.
- **[Risk]** A local archive contains all personal data. → Keep it under 0700/0600 private paths, never expose file contents in responses, and let the Skill explain that the archive is sensitive.

## Migration Plan

1. Ship the new CLI and matching Skill with the additive operations and archive verifier.
2. On upgrade, the Skill exports a backup and records its path before replacing resources.
3. Run handshake and health/migration preflight with the new binary. If successful, continue; if not, restore the recorded archive and report the result.
4. Existing data roots are unchanged until an explicit restore. Existing schema migrations continue to run through `storage.Open`.

## Open Questions

- A future release may add optional authenticated encryption for archives; v1 deliberately relies on local file permissions and the user's filesystem security.
