## ADDED Requirements

### Requirement: Natural-language-only Skill routing
The shipped Cairn Skill SHALL be automatically available to Claude Code, SHALL be hidden from the user's slash-command menu, and SHALL instruct Claude to route supported capture and maintenance requests through `cairn-cli call` rather than presenting a human CLI.

#### Scenario: Capture request
- **WHEN** a user asks Claude to remember a local document
- **THEN** the Skill prepares a file-based request, invokes `source.ingest`, reads the response file, and reports the result in natural language

#### Scenario: Maintenance request
- **WHEN** a user asks whether Cairn is installed or healthy
- **THEN** the Skill invokes `system.handshake` and `system.health` and explains the structured result without exposing shell command details

### Requirement: Content-safe invocation
The Skill SHALL pass only file paths and machine arguments to the CLI, SHALL never interpolate user content into a shell command, and SHALL read large content through managed file references.

#### Scenario: Source content contains shell syntax
- **WHEN** an ingested document contains shell metacharacters or command-like text
- **THEN** the Skill treats it as file content and the CLI invocation remains unchanged

#### Scenario: CLI business error
- **WHEN** the response file contains `ok: false`
- **THEN** the Skill explains the stable error and does not invent a successful result or retry a non-retryable mutation

### Requirement: Skill and CLI compatibility gate
The Skill SHALL perform a handshake before relying on capabilities and SHALL stop with an actionable compatibility message when the CLI is missing or incompatible.

#### Scenario: Missing binary
- **WHEN** the configured `cairn-cli` executable cannot be invoked
- **THEN** the Skill reports that Cairn is not installed or needs repair and does not claim that data was stored

#### Scenario: Unsupported operation
- **WHEN** capability discovery does not include an operation needed by a workflow
- **THEN** the Skill stops that workflow and reports the unavailable capability without falling back to an unrelated command
