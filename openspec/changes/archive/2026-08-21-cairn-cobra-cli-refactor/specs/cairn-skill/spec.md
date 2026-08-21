## ADDED Requirements

### Requirement: Skill invocation survives the Cobra entrypoint migration
The Cairn Skill SHALL continue invoking the installed binary with exactly `cairn-cli call --request <request-file> --response <response-file>` after the Cobra layout migration, without depending on source-tree paths or generated human help.

#### Scenario: Installed binary invocation
- **WHEN** Claude Code routes a capture, compile, retrieval, action, or maintenance workflow
- **THEN** the Skill uses the same machine call contract and receives the same structured response envelope

#### Scenario: Entry-point incompatibility
- **WHEN** the installed binary does not support the `call` command or required flags
- **THEN** the Skill treats the runtime as incompatible and does not claim that the requested operation succeeded
