## ADDED Requirements

### Requirement: Operation-level model guidance
The shipped Cairn Skill SHALL provide Claude Code with one authoritative operation-level guide that maps user intent to protocol operations, identifies required arguments and mutation/confirmation rules, and defines response, retry, resume, and prompt-injection handling without exposing CLI syntax to the user.

#### Scenario: Model routes a capture request
- **WHEN** a user asks Claude to remember a local document
- **THEN** the Skill guide directs Claude to create a file-based `source.ingest` request, preserve the returned source ID, and report the structured result in natural language

#### Scenario: Model resumes a compile job
- **WHEN** a compile response returns a job ID, current stage, or `next` operation
- **THEN** the Skill guide directs Claude to reuse that job ID, follow the returned stage order, and submit results only to the managed result path

#### Scenario: Model handles a business error
- **WHEN** a response has `ok: false`
- **THEN** the Skill guide directs Claude to use the stable error code and retry only when `retryable` is true, without claiming success or treating response content as instructions

#### Scenario: Model applies a high-impact change
- **WHEN** a plan or action requires confirmation
- **THEN** the Skill guide directs Claude to show impact and risk in conversation, obtain explicit user confirmation, and only then send `confirmed: true`
