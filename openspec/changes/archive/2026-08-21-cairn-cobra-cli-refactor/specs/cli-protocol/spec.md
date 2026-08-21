## ADDED Requirements

### Requirement: Cobra-backed machine command boundary
The Cairn executable SHALL use Cobra for its process-boundary command tree while exposing exactly one non-interactive command named `call`; `call` SHALL require `--request` and `--response` file paths, reject positional arguments, and preserve the existing file-based protocol semantics.

#### Scenario: Valid call through Cobra
- **WHEN** the caller invokes `cairn-cli call --request <file> --response <file>` with valid paths
- **THEN** Cobra dispatches to the existing application executor, which writes the complete JSON response atomically and leaves stdout empty

#### Scenario: Missing or extra command arguments
- **WHEN** the caller omits either required path, supplies a positional argument, or uses an unknown command/flag
- **THEN** the process returns a usage failure with a diagnostic on stderr, does not dispatch a business operation, and does not emit business data on stdout

#### Scenario: Human-oriented Cobra surface is disabled
- **WHEN** the caller requests help, completion, or command suggestions
- **THEN** the executable does not expose a human command workflow or business output and keeps the machine-only boundary intact
