## MODIFIED Requirements

### Requirement: Install bundled Skill resources
The project SHALL provide a `cairn-cli skill install` command that installs the bundled Cairn Skill resources for Claude Code or Codex, with `--scope global|project` defaulting to `global`, a repeatable `--target claude|codex` flag defaulting to `claude`, and a human-readable result that reports each resolved destination.

#### Scenario: Default global Claude installation
- **WHEN** a user runs `cairn-cli skill install` with no target or scope flags
- **THEN** the command installs the bundled Skill under the user's global Claude skill directory and reports the destination

#### Scenario: Project Codex installation
- **WHEN** a user runs `cairn-cli skill install --target codex --scope project` from a project directory
- **THEN** the command installs the bundled Skill under `<project>/.codex/skills/cairn`

#### Scenario: Multiple repeated targets
- **WHEN** a user runs `cairn-cli skill install --target codex --target claude`
- **THEN** the command installs equivalent bundled resources into both target directories and reports each result once in the requested order

#### Scenario: Duplicate target values
- **WHEN** a user supplies the same target more than once
- **THEN** the command installs that target only once and succeeds without duplicate result entries

#### Scenario: Removed combined target alias
- **WHEN** a user supplies `--target both`
- **THEN** the command rejects the value and instructs the user to repeat `--target` with `claude` and `codex`
