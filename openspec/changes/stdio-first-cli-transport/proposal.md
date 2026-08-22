## Why

Moss is used only as the local execution driver of a Claude Code Skill, but the current request/response-file contract makes every operation require a temporary request file, a CLI invocation, and a response-file read. This multiplies tool calls and host authorization prompts without adding value for the normal local Skill workflow.

The transport must match Moss's product boundary: Claude remains the user interface, Claude may write model-generated staging artifacts locally, and Moss remains the authority that validates and applies data to SQLite and the managed Wiki.

## What Changes

- **BREAKING**: Make `moss call` use stdin/stdout as its primary machine transport: read one JSON request from stdin and emit one JSON response on stdout.
- **BREAKING**: Stop requiring the Skill to create request and response JSON files for ordinary operations.
- Keep the existing request and response envelopes, operation names, idempotency rules, stable errors, and exit-code semantics.
- Keep `input_file`, `result_file`, managed article paths, and backup paths as domain artifacts; these are not protocol transport files.
- Allow Claude to write compile staging results to Moss-managed `result_file` paths, while Moss continues to validate and apply authoritative SQLite/Wiki changes.
- Make handshake, health, and capability discovery maintenance-time operations instead of mandatory per-turn preambles.
- Retain the file transport only as an explicit compatibility/automation path during migration, without documenting it as the normal Skill workflow.
- Update the shipped Skill and protocol guidance to use the single `moss call` stdin/stdout invocation and to preserve file paths only where a workflow explicitly requires a durable artifact.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cli-protocol`: Change the machine call transport from required request/response files to stdin/stdout-first, define structured stdout responses and transport errors, and retain file transport only as an explicit compatibility mode.
- `cairn-skill`: Change Skill routing and operation guidance so ordinary calls do not create temporary protocol files, while compile staging and large managed artifacts remain file-backed.

## Impact

- `cmd/call.go` and `internal/app`: add the stdin/stdout process boundary and preserve shared request validation and dispatch semantics.
- `internal/protocol`: add bounded request reading and response encoding for streams; keep atomic managed artifact writes separate from protocol transport.
- `internal/skill/assets`: revise `SKILL.md`, the CLI protocol reference, operation guide, and workflow instructions.
- Tests and real-binary simulations must verify one-call operation, empty diagnostic stderr, structured business errors, bounded streams, compile staging handoff, and compatibility behavior.
- The installed Moss Skill and binary must be upgraded together so an old file-only runtime is not mistaken for a compatible stdio runtime.
