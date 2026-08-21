# skill-installation Specification

## Purpose
TBD - created by archiving change cairn-skill-install-command. Update Purpose after archive.
## Requirements
### Requirement: Install bundled Skill resources
The project SHALL provide a `moss skill install` command that installs the bundled Moss Skill resources for Claude Code or Codex, with `--scope global|project` defaulting to `global`, a repeatable `--target claude|codex` flag defaulting to `claude`, and a human-readable result that reports each resolved destination.

#### Scenario: Default global Claude installation
- **WHEN** a user runs `moss skill install` with no target or scope flags
- **THEN** the command installs the bundled Skill under the user's global Claude skill directory and reports the destination

#### Scenario: Project Codex installation
- **WHEN** a user runs `moss skill install --target codex --scope project` from a project directory
- **THEN** the command installs the bundled Skill under `<project>/.codex/skills/moss`

#### Scenario: Multiple repeated targets
- **WHEN** a user runs `moss skill install --target codex --target claude`
- **THEN** the command installs equivalent bundled resources into both target directories and reports each result once in the requested order

#### Scenario: Duplicate target values
- **WHEN** a user supplies the same target more than once
- **THEN** the command installs that target only once and succeeds without duplicate result entries

#### Scenario: Removed combined target alias
- **WHEN** a user supplies `--target both`
- **THEN** the command rejects the value and instructs the user to repeat `--target` with `claude` and `codex`

### Requirement: Safe repeatable installation
The installer SHALL make identical existing files a no-op, SHALL refuse differing existing files by default, and SHALL overwrite only conflicting Skill files when the user explicitly supplies `--force`; it SHALL not delete unrelated files.

#### Scenario: Reinstall identical resources
- **WHEN** the destination already contains the same Skill file bytes
- **THEN** the command succeeds without changing the file and reports it as unchanged or skipped

#### Scenario: Conflict without force
- **WHEN** a destination Skill file differs from the bundled resource and `--force` is absent
- **THEN** the command fails before writing and reports the conflicting path

#### Scenario: Explicit overwrite
- **WHEN** a destination Skill file differs and the user supplies `--force`
- **THEN** the command atomically replaces that file while preserving unrelated destination files

### Requirement: Installed resources are self-contained
The installer SHALL use the versioned Skill resources bundled in the binary and SHALL not require a source checkout, network access, or execution of Markdown content.

#### Scenario: Installed binary outside checkout
- **WHEN** a user invokes the installer from a directory without the Moss source tree
- **THEN** the command still installs the complete Skill resource tree from the binary bundle
