## ADDED Requirements

### Requirement: Bootstrap through Claude Code only
The Cairn Skill SHALL define first-time bootstrap as a Claude Code workflow that installs a trusted matching Go binary and versioned Skill resources into private application locations, initializes the data root, and verifies `system.handshake` plus `system.health` before claiming readiness.

#### Scenario: Fresh installation
- **WHEN** the user asks Claude to install and initialize Cairn
- **THEN** Claude performs the installation and checks without asking the user to learn or run Cairn CLI commands, and reports structured readiness or the repair state

#### Scenario: Incompatible runtime
- **WHEN** handshake reports an unsupported protocol or Skill version
- **THEN** Claude stops the workflow, explains the compatibility mismatch, and does not claim that data was stored

### Requirement: Upgrade with backup and migration preflight
The Cairn Skill SHALL export a backup before replacing the binary or Skill, verify the new runtime handshake and health/migration preflight, and use the recorded backup for confirmed recovery if the upgrade cannot open healthy storage.

#### Scenario: Successful upgrade
- **WHEN** the backup succeeds and the new runtime passes handshake and health checks
- **THEN** Claude reports the upgraded version and backup location without exposing shell output or requiring a human CLI invocation

#### Scenario: Failed upgrade recovery
- **WHEN** the new runtime fails compatibility, migration, or health checks after the backup
- **THEN** Claude stops normal writes, explains the affected backup, and asks for confirmation before calling `system.restore`

### Requirement: Keep operational UI inside the Skill
The bootstrap and upgrade workflow SHALL not add human-facing subcommands, Web UI, MCP access, interactive CLI prompts, or a second model invocation.

#### Scenario: User asks for installation status
- **WHEN** the user asks whether Cairn is installed or healthy
- **THEN** the Skill calls handshake/health and explains the structured result in conversation without exposing CLI syntax
