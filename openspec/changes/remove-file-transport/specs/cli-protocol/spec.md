## MODIFIED Requirements

### Requirement: File-based call contract
The Moss CLI SHALL expose a machine-only `call` entrypoint that reads one complete request JSON document from stdin and writes one complete response JSON document to stdout, SHALL parse the complete request before mutation, and SHALL never enter an interactive mode. The entrypoint SHALL NOT accept request or response file transport flags.

#### Scenario: Valid request is executed
- **WHEN** the caller invokes `moss call` with a supported request document on stdin
- **THEN** the CLI executes exactly the requested operation and writes one complete response document to stdout

#### Scenario: Missing protocol fields are rejected
- **WHEN** a request omits `protocol_version`, `request_id`, `actor`, `operation`, or `arguments`
- **THEN** the CLI writes `ok: false` with error code `REQUEST_INVALID` to stdout and performs no mutation

#### Scenario: File transport flags are removed
- **WHEN** the caller supplies `--request`, `--response`, or another file transport flag
- **THEN** the process returns a usage failure on stderr, does not dispatch a business operation, and does not emit a business response

### Requirement: Atomic response and output discipline
The CLI SHALL emit exactly one bounded JSON response on stdout, SHALL write only diagnostic information to stderr, and SHALL not create protocol request or response envelope files.

#### Scenario: Stdio response
- **WHEN** a call completes successfully or with a business error
- **THEN** stdout contains one complete JSON response document, stderr contains no business data, and no protocol response file is created

#### Scenario: Business failure
- **WHEN** an operation cannot satisfy a valid request
- **THEN** stdout carries `ok: false` and a stable error object rather than an unstructured business message

#### Scenario: Unreadable or oversized stdin request
- **WHEN** stdin is empty, is larger than the request limit, contains invalid JSON, or contains more than one JSON document
- **THEN** the CLI emits a structured `REQUEST_INVALID` or `REQUEST_TOO_LARGE` response when stdout is writable, performs no operation mutation, and writes only diagnostics to stderr

### Requirement: Managed path safety
The Moss CLI SHALL validate every domain file path referenced by an operation, SHALL reject traversal and symlink escapes, and SHALL not interpret any request or response envelope path because protocol envelopes are transported through stdin/stdout.

#### Scenario: Traversal attempt in a domain argument
- **WHEN** a request references `../` to escape the managed source, job, article, backup, or staging directory
- **THEN** the CLI returns `PATH_INVALID` before reading or writing the target and performs no operation mutation

#### Scenario: Symlink escape in a domain argument
- **WHEN** a request path resolves through a symlink outside the permitted managed root
- **THEN** the CLI returns `PATH_INVALID` and performs no operation mutation

#### Scenario: Protocol envelope paths are absent
- **WHEN** a valid call is received through stdin/stdout
- **THEN** the CLI performs no request/response path validation and applies safety checks only to operation-issued domain paths

### Requirement: Cobra-backed machine command boundary
The Moss executable SHALL expose a machine-only `call` entrypoint using stdin/stdout while allowing the explicit human-facing setup command `skill install`; all knowledge, source, action, plan, and system business operations SHALL remain behind `call`. The command SHALL reject positional arguments and all transport flags.

#### Scenario: Valid call through Cobra stdio mode
- **WHEN** the caller invokes `moss call` with a valid request on stdin
- **THEN** Cobra dispatches to the application executor, which writes one complete JSON response to stdout and leaves stderr diagnostic-only

#### Scenario: Invalid transport flags or extra arguments
- **WHEN** the caller supplies a transport flag, a positional argument, or an unknown command/flag for `call`
- **THEN** the process returns a usage failure with a diagnostic on stderr, does not dispatch a business operation, and does not emit business data on stdout

#### Scenario: Human-oriented surface remains limited
- **WHEN** the caller requests help, completion, suggestions, or a command other than the explicit Skill installer
- **THEN** the executable does not expose a human business workflow; only `skill install` may produce setup-oriented human output
