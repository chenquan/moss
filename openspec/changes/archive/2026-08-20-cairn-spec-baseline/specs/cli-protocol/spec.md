## ADDED Requirements

### Requirement: File-based call contract
The Cairn CLI SHALL expose a machine-only `call` entrypoint that requires a request file and a response file, SHALL parse the complete request before mutation, and SHALL never enter an interactive mode.

#### Scenario: Valid request is executed
- **WHEN** the caller invokes `cairn-cli call` with readable request and writable response paths containing a supported protocol request
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
