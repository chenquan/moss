# cli-protocol Specification

## Purpose
TBD - created by archiving change cairn-spec-baseline. Update Purpose after archive.
## Requirements
### Requirement: File-based call contract
The Cairn CLI SHALL expose a machine-only `call` entrypoint that requires a request file and a response file, SHALL parse the complete request before mutation, and SHALL never enter an interactive mode.

#### Scenario: Valid request is executed
- **WHEN** the caller invokes `cairn call` with readable request and writable response paths containing a supported protocol request
- **THEN** the CLI executes exactly the requested operation and writes one complete response document

#### Scenario: Missing protocol fields are rejected
- **WHEN** a request omits `protocol_version`, `request_id`, `actor`, `operation`, or `arguments`
- **THEN** the CLI writes `ok: false` with error code `REQUEST_INVALID` and performs no mutation

### Requirement: Version negotiation
The CLI SHALL advertise a supported protocol range, SHALL accept only compatible protocol versions, and SHALL return `PROTOCOL_VERSION_UNSUPPORTED` without invoking an operation when the request is incompatible.

#### Scenario: Compatible request
- **WHEN** `protocol_version` is within the CLI's supported major/minor range
- **THEN** the requested operation is eligible for execution

#### Scenario: Incompatible request
- **WHEN** the request has an unsupported major version
- **THEN** the response identifies the supported range and no operation state changes

### Requirement: Idempotent mutations
Every mutating operation SHALL require an `idempotency_key`; the CLI SHALL persist the request fingerprint and response, SHALL return the original response for an identical retry, and SHALL reject reuse with a different fingerprint.

#### Scenario: Safe retry after missing response
- **WHEN** a caller retries a mutation with the same idempotency key and identical request after a transport failure
- **THEN** the CLI returns the previously committed response without applying the mutation again

#### Scenario: Conflicting reuse
- **WHEN** a caller reuses an idempotency key with different operation or arguments
- **THEN** the CLI returns `IDEMPOTENCY_CONFLICT` and does not execute the second request

### Requirement: Atomic response and output discipline
The CLI SHALL write responses through an atomic replacement, SHALL leave stdout empty for business results, and SHALL write only diagnostic information to stderr.

#### Scenario: Response replacement
- **WHEN** the operation completes successfully or with a business error
- **THEN** the response path contains one complete JSON document and no partially written response is observable

#### Scenario: Business failure
- **WHEN** an operation cannot satisfy a valid request
- **THEN** the CLI writes `ok: false` and a stable error object to the response file rather than printing business data to stdout

### Requirement: Managed path safety
The CLI SHALL reject traversal, symlink escapes, non-regular request files, and response destinations outside the caller-approved managed request directory or Cairn data root.

#### Scenario: Traversal attempt
- **WHEN** a request references `../` to escape its managed directory
- **THEN** the CLI returns `PATH_INVALID` before reading or writing the target

#### Scenario: Symlink escape
- **WHEN** a request or response path resolves through a symlink outside the managed root
- **THEN** the CLI returns `PATH_INVALID` and performs no operation mutation

### Requirement: Advertise the complete compile operation set
The CLI capability response SHALL include `compile.start`, `compile.next`, `compile.submit`, `compile.status`, `compile.preview`, `compile.apply`, and `compile.abort`, and SHALL classify `compile.apply` as mutating.

#### Scenario: Skill discovers compile apply
- **WHEN** a compatible Skill calls `system.capabilities`
- **THEN** the response contains `compile.apply` with `mutating: true` and a stable description

#### Scenario: Compile apply requires idempotency
- **WHEN** a caller submits `compile.apply` without an idempotency key
- **THEN** the CLI returns `IDEMPOTENCY_REQUIRED` before dispatching the operation

### Requirement: Cobra-backed machine command boundary
The Cairn executable SHALL expose a machine-only `call` entrypoint using the file-based protocol while allowing the explicit human-facing setup command `skill install`; all knowledge, source, action, plan, and system business operations SHALL remain behind `call`, and `call` SHALL require `--request` and `--response` file paths, reject positional arguments, and preserve the existing file-based protocol semantics.

#### Scenario: Valid call through Cobra
- **WHEN** the caller invokes `cairn call --request <file> --response <file>` with valid paths
- **THEN** Cobra dispatches to the existing application executor, which writes the complete JSON response atomically and leaves stdout empty

#### Scenario: Missing or extra command arguments
- **WHEN** the caller omits either required path, supplies a positional argument, or uses an unknown command/flag for `call`
- **THEN** the process returns a usage failure with a diagnostic on stderr, does not dispatch a business operation, and does not emit business data on stdout

#### Scenario: Human-oriented surface remains limited
- **WHEN** the caller requests help, completion, suggestions, or a command other than the explicit Skill installer
- **THEN** the executable does not expose a human business workflow; only `skill install` may produce setup-oriented human output
