# Design

## Privacy

Historical article content is parsed per version. Without `allow_sensitive`, any requested sensitive historical version causes the whole history request to fail with `SENSITIVITY_DENIED`. Compile context uses the same explicit option and rejects unauthorized sensitive records instead of silently constructing a partial context.

## Mutation recovery

All operations that coordinate managed files and SQLite use a data-root mutation lock and a JSON recovery journal under `staging`. The journal records operation identity, before/after hashes and paths, and a phase (`prepared`, `renamed`, or `committed`). A final rename never clears its journal until the SQLite commit succeeds.

`system.recover` is explicit and idempotent. It resolves a journal by inspecting both SQLite and managed-file state: an unchanged database rolls back the renamed file, a committed database finalizes cleanup, and conflicting hashes return `RECOVERY_CONFLICT` without overwriting user data. `system.health` exposes unresolved journals and blocks mutations.

## Projection and provenance

Article undo paths parse the restored Markdown and rebuild all FTS fields and citations. Corrupt extraction replacements stale the old active row before inserting the replacement. Backup export/restore validates every managed source, article, extraction, and citation reference under the mutation lock.

## Compatibility

The protocol version remains `1.0`; `system.recover` is an additive capability. Existing plans and backups remain readable. No `.cairn` data is read, created, migrated, or deleted.

