## MODIFIED Requirements

### Requirement: File-based call contract
The Moss CLI SHALL expose a machine-only `call` entrypoint that defaults to reading one complete request JSON document from stdin and writing one complete response JSON document to stdout, SHALL parse the complete request before mutation, and SHALL never enter an interactive mode. It MAY additionally accept the explicit `--request <file> --response <file>` compatibility transport, which SHALL preserve the file-based semantics.

#### Scenario: Valid request through stdio
- **WHEN** the caller invokes `moss call` with a supported request document on stdin
- **THEN** the CLI executes exactly the requested operation and writes one complete response document to stdout

#### Scenario: Valid request through compatibility files
- **WHEN** the caller invokes `moss call --request <file> --response <file>` with readable request and writable response paths containing a supported protocol request
- **THEN** the CLI executes exactly the requested operation and writes one complete response document atomically to the response path while leaving stdout empty

#### Scenario: Missing protocol fields are rejected
- **WHEN** a request omits `protocol_version`, `request_id`, `actor`, `operation`, or `arguments`
- **THEN** the CLI emits `ok: false` with error code `REQUEST_INVALID` on the selected response transport and performs no mutation

### Requirement: Atomic response and output discipline
The CLI SHALL emit exactly one bounded JSON response on stdout in stdio mode, SHALL leave stdout empty for valid file-mode calls, SHALL write only diagnostic information to stderr, and SHALL atomically replace compatibility response files.

#### Scenario: Stdio response
- **WHEN** a stdio operation completes successfully or with a business error
- **THEN** stdout contains one complete JSON response document, stderr contains no business data, and no protocol response file is created

#### Scenario: File response replacement
- **WHEN** a file-mode operation completes successfully or with a business error
- **THEN** the response path contains one complete JSON document with no partially written response observable, stdout is empty, and stderr contains no business data

#### Scenario: Business failure
- **WHEN** an operation cannot satisfy a valid request
- **THEN** the selected transport carries `ok: false` and a stable error object rather than an unstructured business message

#### Scenario: Unreadable or oversized stdio request
- **WHEN** stdin is empty, is larger than the request limit, contains invalid JSON, or contains more than one JSON document
- **THEN** the CLI emits a structured `REQUEST_INVALID` or `REQUEST_TOO_LARGE` response when stdout is writable, performs no operation mutation, and writes only diagnostics to stderr

### Requirement: Managed path safety
The Moss CLI SHALL validate every domain file path referenced by an operation, SHALL reject traversal and symlink escapes, and SHALL apply request/response path validation whenever the explicit compatibility file transport is selected. Stdio transport SHALL not require a request or response path.

#### Scenario: Traversal attempt in a domain argument
- **WHEN** a request references `../` to escape the managed source, job, article, backup, or staging directory
- **THEN** the CLI returns `PATH_INVALID` before reading or writing the target and performs no operation mutation

#### Scenario: Symlink escape in a domain argument
- **WHEN** a request path resolves through a symlink outside the permitted managed root
- **THEN** the CLI returns `PATH_INVALID` and performs no operation mutation

#### Scenario: Compatibility request path validation
- **WHEN** the file transport is selected with a missing, non-regular, symlinked, oversized, or otherwise invalid request/response path
- **THEN** the CLI returns `PATH_INVALID` or `REQUEST_TOO_LARGE` before dispatching the operation and leaves stdout empty

### Requirement: Advertise the complete compile operation set
The CLI capability response SHALL include `compile.start`, `compile.next`, `compile.submit`, `compile.status`, `compile.preview`, `compile.apply`, and `compile.abort`, and SHALL classify `compile.apply` as mutating.

#### Scenario: Skill discovers compile apply
- **WHEN** a compatible caller calls `system.capabilities` through stdin/stdout or the compatibility file transport
- **THEN** the response contains `compile.apply` with `mutating: true` and a stable description

#### Scenario: Compile apply requires idempotency
- **WHEN** a caller submits `compile.apply` without an idempotency key
- **THEN** the CLI returns `IDEMPOTENCY_REQUIRED` before dispatching the operation

### Requirement: Cobra-backed machine command boundary
The Moss executable SHALL expose a machine-only `call` entrypoint using stdin/stdout by default while allowing the explicit human-facing setup command `skill install`; all knowledge, source, action, plan, and system business operations SHALL remain behind `call`. The command SHALL reject positional arguments, SHALL reject mixing stdio and file transport flags, and SHALL preserve the compatibility file protocol when both file paths are supplied.

#### Scenario: Valid call through Cobra stdio mode
- **WHEN** the caller invokes `moss call` with a valid request on stdin
- **THEN** Cobra dispatches to the application executor, which writes one complete JSON response to stdout and leaves stderr diagnostic-only

#### Scenario: Valid call through Cobra file mode
- **WHEN** the caller invokes `moss call --request <file> --response <file>` with valid paths
- **THEN** Cobra dispatches to the application executor, which writes the complete JSON response atomically and leaves stdout empty

#### Scenario: Invalid transport flags or extra arguments
- **WHEN** the caller supplies only one file path, mixes file flags with stdio flags, supplies a positional argument, or uses an unknown command/flag for `call`
- **THEN** the process returns a usage failure with a diagnostic on stderr, does not dispatch a business operation, and does not emit business data on stdout

#### Scenario: Human-oriented surface remains limited
- **WHEN** the caller requests help, completion, suggestions, or a command other than the explicit Skill installer
- **THEN** the executable does not expose a human business workflow; only `skill install` may produce setup-oriented human output

## ADDED Requirements

### Requirement: Bounded stdio request and response
The stdio transport SHALL read one request within the protocol request limit, reject trailing documents, emit at most one response within the protocol response limit, and preserve request IDs and stable error codes across transport failures.

#### Scenario: Request and response stay within bounds
- **WHEN** a valid request and its response are within the configured protocol limits
- **THEN** Moss dispatches the operation and emits exactly one JSON response with the same request ID

#### Scenario: Response cannot be emitted
- **WHEN** Moss cannot write the response to stdout after request processing
- **THEN** the process returns a transport failure exit code and writes diagnostics only to stderr; it does not claim a user-visible success

### Requirement: Transport-neutral operation execution
The application SHALL use the same request validation, idempotency, operation dispatch, confirmation, audit, and storage transaction path for stdio and compatibility file transports.

#### Scenario: Transport parity
- **WHEN** the same request is executed once through stdin/stdout and once through validated request/response files
- **THEN** both responses have equivalent business data, warnings, next state, and stable error behavior, subject only to request IDs and timestamps
