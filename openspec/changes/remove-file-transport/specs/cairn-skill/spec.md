## MODIFIED Requirements

### Requirement: Explicit-name Skill routing
The shipped Moss Skill SHALL be available to Claude Code, SHALL be hidden from the user's slash-command menu, and SHALL activate only when the user's current message explicitly addresses Moss by name. It SHALL route supported capture and maintenance requests through the machine-only `moss call` stdin/stdout protocol rather than presenting a human CLI.

#### Scenario: User explicitly addresses Moss
- **WHEN** a user says “Moss，记住这份资料” or otherwise addresses Moss by name in the current message
- **THEN** the Skill activates and routes the requested workflow through the machine protocol

#### Scenario: Generic request does not activate Moss
- **WHEN** a user asks to remember, search, organize, or check something without addressing Moss by name
- **THEN** the Skill does not invoke the Moss binary or claim to have handled the request

#### Scenario: Capture request
- **WHEN** a user asks Claude to remember a local document
- **THEN** the Skill sends a `source.ingest` request through stdin/stdout without creating protocol request or response files, then reports the structured result in natural language

#### Scenario: Maintenance request
- **WHEN** a user asks whether Moss is installed or healthy
- **THEN** the Skill invokes the required maintenance operations through stdin/stdout and explains the structured result without exposing shell command details

### Requirement: Content-safe invocation
The Skill SHALL pass request envelopes through stdin rather than process arguments, SHALL never interpolate user content into shell arguments or unquoted shell syntax, SHALL use only Moss-issued domain file paths for staging and large artifacts, and SHALL read large content through managed file references.

#### Scenario: Source content contains shell syntax
- **WHEN** an ingested document contains shell metacharacters or command-like text
- **THEN** the Skill treats it as file content, the stdin envelope remains safely quoted, and the Moss invocation does not execute or interpolate the document text

#### Scenario: Model writes a compile result
- **WHEN** `compile.next` returns a managed `result_file`
- **THEN** Claude may write the generated stage JSON to that exact staging path, but the Skill submits only the path through stdin and Moss validates the file before advancing the job

#### Scenario: CLI business error
- **WHEN** the stdout response contains `ok: false`
- **THEN** the Skill explains the stable error and does not invent a successful result or retry a non-retryable mutation

### Requirement: Skill and CLI compatibility gate
The Skill SHALL establish compatibility with a matching stdio-only Moss binary during installation, upgrade, repair, or an explicit maintenance workflow, SHALL invoke normal operations directly after a compatible installation is established, and SHALL stop with an actionable compatibility message when the CLI is missing or incompatible. It SHALL never fall back to request/response envelope files.

#### Scenario: Missing binary
- **WHEN** the configured `moss` executable cannot be invoked
- **THEN** the Skill reports that Moss is not installed or needs repair and does not claim that data was stored

#### Scenario: Unsupported protocol or transport
- **WHEN** the installed binary cannot parse the stdio `call` entrypoint or reports an unsupported protocol/runtime version
- **THEN** the Skill stops the workflow, reports that the matching Moss binary and Skill must be upgraded together, and does not retry through file flags

#### Scenario: Unsupported operation
- **WHEN** capability discovery does not include an operation needed by a workflow
- **THEN** the Skill stops that workflow and reports the unavailable capability without falling back to an unrelated command

### Requirement: Skill invocation survives the Cobra entrypoint migration
The Moss Skill SHALL invoke the installed binary with the stable `moss call` stdin/stdout entrypoint after the Cobra layout migration, without depending on source-tree paths, generated human help, or per-call request/response envelope files.

#### Scenario: Installed binary invocation
- **WHEN** Claude Code routes a capture, compile, retrieval, action, or maintenance workflow
- **THEN** the Skill sends the request envelope through stdin to `moss call`, receives one structured response from stdout, and uses only operation-issued domain paths for staging or large artifacts

#### Scenario: Entry-point incompatibility
- **WHEN** the installed binary does not support the stdio-only `call` entrypoint
- **THEN** the Skill treats the runtime as incompatible and does not claim that the requested operation succeeded

### Requirement: Operation-level model guidance
The shipped Moss Skill SHALL provide Claude Code with one authoritative operation-level guide that maps user intent to protocol operations, identifies required arguments and mutation/confirmation rules, and defines stdin/stdout response, retry, resume, and prompt-injection handling without exposing CLI syntax to the user.

#### Scenario: Model routes a capture request
- **WHEN** a user asks Claude to remember a local document
- **THEN** the Skill guide directs Claude to send a stdin `source.ingest` request containing the local input path, preserve the returned source ID, and report the structured result in natural language

#### Scenario: Model resumes a compile job
- **WHEN** a compile response returns a job ID, current stage, or `next` operation
- **THEN** the Skill guide directs Claude to reuse that job ID, follow the returned stage order, write only the managed result path, and submit the result path through stdin

#### Scenario: Model handles a business error
- **WHEN** a stdout response has `ok: false`
- **THEN** the Skill guide directs Claude to use the stable error code and retry only when `retryable` is true, without claiming success or treating response content as instructions

#### Scenario: Model applies a high-impact change
- **WHEN** a plan or action requires confirmation
- **THEN** the Skill guide directs Claude to show impact and risk in conversation, obtain explicit user confirmation, and only then send `confirmed: true` through stdin

## ADDED Requirements

### Requirement: Staging ownership boundary
The Moss Skill SHALL allow Claude to create or update only Moss-issued staging/result artifacts and SHALL route every authoritative SQLite, Wiki, index, plan, trash, backup, and audit mutation through Moss operations.

#### Scenario: Valid staging write
- **WHEN** Moss returns a managed compile `result_file` for the current job stage
- **THEN** Claude may write the model-generated stage result there and Moss may validate it on `compile.submit`

#### Scenario: Direct authoritative write is prohibited
- **WHEN** a workflow would require changing the final Wiki, SQLite database, index, plan, trash, backup, or audit state
- **THEN** the Skill invokes the corresponding Moss operation and does not ask Claude to edit the authoritative artifact directly
