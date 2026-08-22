## MODIFIED Requirements

### Requirement: Bootstrap through Claude Code only
The Moss Skill SHALL define first-time bootstrap as a Claude Code workflow that installs a trusted matching Go binary and versioned Skill resources into private application locations, initializes the data root, and verifies stdio `system.handshake` plus `system.health` before claiming readiness.

#### Scenario: Fresh installation
- **WHEN** the user asks Claude to install and initialize Moss
- **THEN** Claude installs the matching runtime and Skill pair, performs the stdio checks without asking the user to learn or run Moss CLI commands, and reports structured readiness or the repair state

#### Scenario: Incompatible runtime
- **WHEN** handshake reports an unsupported protocol, transport, or Skill version
- **THEN** Claude stops the workflow, explains the compatibility mismatch, and does not claim that data was stored

### Requirement: Upgrade with backup and migration preflight
The Moss Skill SHALL export a backup before replacing the binary or Skill, verify the new matching runtime/Skill pair through stdio handshake and health/migration preflight, and use the recorded backup for confirmed recovery if the upgrade cannot open healthy storage.

#### Scenario: Successful upgrade
- **WHEN** the backup succeeds and the new matching runtime/Skill pair passes stdio handshake and health checks
- **THEN** Claude reports the upgraded version and backup location without exposing shell output or requiring a human CLI invocation

#### Scenario: Failed upgrade recovery
- **WHEN** the new runtime or Skill fails compatibility, migration, or health checks after the backup
- **THEN** Claude restores the previous binary and Skill pair or stops with the recorded recovery state, explains the affected backup, and asks for confirmation before calling `system.restore`

### Requirement: Keep operational UI inside the Skill
The bootstrap and upgrade workflow SHALL not add human-facing subcommands, Web UI, MCP access, interactive CLI prompts, a second model invocation, or a request/response-file fallback.

#### Scenario: User asks for installation status
- **WHEN** the user asks whether Moss is installed or healthy
- **THEN** the Skill calls stdio handshake/health and explains the structured result in conversation without exposing CLI syntax
