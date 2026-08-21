## MODIFIED Requirements

### Requirement: Cobra-backed machine command boundary
The Cairn executable SHALL expose a machine-only `call` entrypoint using the file-based protocol while allowing the explicit human-facing setup command `skill install`; all knowledge, source, action, plan, and system business operations SHALL remain behind `call`, and `call` SHALL require `--request` and `--response` file paths, reject positional arguments, and preserve the existing file-based protocol semantics.

#### Scenario: Valid call through Cobra
- **WHEN** the caller invokes `cairn-cli call --request <file> --response <file>` with valid paths
- **THEN** Cobra dispatches to the existing application executor, which writes the complete JSON response atomically and leaves stdout empty

#### Scenario: Missing or extra command arguments
- **WHEN** the caller omits either required path, supplies a positional argument, or uses an unknown command/flag for `call`
- **THEN** the process returns a usage failure with a diagnostic on stderr, does not dispatch a business operation, and does not emit business data on stdout

#### Scenario: Human-oriented surface remains limited
- **WHEN** the caller requests help, completion, suggestions, or a command other than the explicit Skill installer
- **THEN** the executable does not expose a human business workflow; only `skill install` may produce setup-oriented human output
