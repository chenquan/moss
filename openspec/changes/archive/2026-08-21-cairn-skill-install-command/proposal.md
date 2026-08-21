## Why

Cairn currently ships Skill resources in the repository, but users have no supported command to install those resources into Claude Code or Codex. Manually copying the directory is error-prone and makes global versus project-local installation ambiguous.

## What Changes

- Add an explicit `cairn-cli skill install` setup command alongside the machine-only `call` entrypoint.
- Support `--target claude`, `--target codex`, and `--target both`; default to Claude.
- Support `--scope global` and `--scope project`; default to global.
- Install the bundled Cairn Skill into the appropriate Claude/Codex skill directory, including `CODEX_HOME` resolution for global Codex installs.
- Make installation idempotent, refuse conflicting existing files by default, and require `--force` to overwrite differences.
- Embed the versioned Skill resources in the Go binary so installation does not depend on a source checkout.
- Keep `call` as the only machine protocol operation; the installer is the sole intentional human-facing setup command.

## Capabilities

### New Capabilities

- `skill-installation`: Install bundled Cairn Skill resources for Claude Code or Codex at global or project scope.

### Modified Capabilities

- `cli-protocol`: distinguish the machine-only `call` boundary from the explicit human-facing Skill installer.

## Impact

- Adds a Cobra `skill install` command and a bundled-resource installer package.
- Adds embedded Skill resource files under `internal/skill/assets` and keeps the checked-in Claude Skill as the source-visible copy.
- Updates command and Skill installation tests, documentation/specs, and Git ignore behavior.
- No JSON request protocol, SQLite schema, or existing runtime operation changes.
