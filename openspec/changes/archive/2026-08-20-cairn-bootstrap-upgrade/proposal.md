## Why

Cairn now has durable sources, Wiki, action state, and reversible safety plans, but a fresh Claude Code environment still lacks a defined bootstrap path and an upgrade safety boundary. Without an in-protocol backup and restore contract, an upgrade cannot satisfy the requirement to back up first and recover if migration or health checks fail.

## What Changes

- Add machine-only `system.export` to create a verified local Cairn backup archive under the private backup directory.
- Add confirmed `system.restore` with path validation, archive integrity checks, pre-restore backup, migration compatibility checks, recovery markers, and audit/idempotency records.
- Extend capabilities, protocol mutating classification, and app dispatch for backup/restore.
- Define Claude Code Skill workflows for first-time bootstrap and upgrade: install matching binary/Skill resources, handshake and health-check, export before replacement, run migration preflight, and restore the pre-upgrade backup on failure.
- Keep all install, upgrade, and recovery prompts inside Claude; do not add human CLI subcommands, Web UI, MCP, or a second model.

## Capabilities

### New Capabilities

- `backup-restore`: File-protocol backup export and confirmed restore of Cairn's local state.
- `bootstrap-upgrade`: Claude-driven installation, version handshake, upgrade preflight, automatic backup, and failure recovery workflow.

### Modified Capabilities

- None.

## Impact

- `internal/protocol`, `internal/system`, `internal/app`, and `internal/storage` gain backup/restore contracts and archive helpers.
- `.claude/skills/cairn/SKILL.md` gains bootstrap and upgrade routing rules.
- SQLite and managed Raw/Wiki/Jobs/trash state are copied into private backup archives; no external service or new runtime dependency is required.
