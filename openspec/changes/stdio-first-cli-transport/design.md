## Context

Moss is a local Go runtime invoked by a Claude Code Skill. The current Cobra `call` command requires `--request` and `--response` paths, so a normal Skill operation requires three host tool calls: create a request file, execute Moss, and read the response file. This is especially costly for handshake, health, retrieval, and action operations whose envelopes are small.

The existing protocol envelope, operation dispatcher, SQLite idempotency records, compile job directories, and managed Wiki are already the authoritative application boundaries. The change should replace only the process transport used by the Skill. Claude remains responsible for interpreting user intent and generating compile-stage results; Moss remains responsible for validation, persistence, deterministic indexes, plans, confirmation gates, and audit.

## Goals / Non-Goals

**Goals:**

- Make `moss call` stdin/stdout the default machine transport so one ordinary operation is one Bash invocation.
- Emit exactly one bounded JSON response on stdout and diagnostics only on stderr.
- Preserve the current request/response envelope, request IDs, idempotency keys, stable errors, and business dispatch behavior.
- Keep domain files such as source inputs, compile stage results, managed article files, and backup archives as explicit paths without treating them as protocol envelopes.
- Let Claude write only Moss-issued staging/result files and let Moss validate and apply all authoritative data changes.
- Avoid mandatory handshake, health, and capability calls before every normal user workflow.
- Keep an explicit file transport during migration for old Skills, automation, and diagnostic reproduction.

**Non-Goals:**

- Do not add human-facing operation subcommands, `--operation` flags, or a `--json` argument carrying business data.
- Do not put user content in process arguments or let Claude bypass Moss to edit SQLite, the final Wiki, indexes, plans, or audit records.
- Do not move model reasoning, knowledge answers, or intent routing into the Go binary.
- Do not introduce a daemon, Unix socket server, MCP server, or general multi-request transaction protocol in this change.
- Do not remove the compile job's managed input/schema/result files; those files are workflow artifacts rather than request/response transport files.

## Decisions

### 1. Default command mode is a single stdio request

`moss call` with no transport flags reads one complete JSON document from stdin and writes one complete JSON response to stdout. The command remains machine-only and rejects positional arguments and interactive input. An explicit `--stdin --stdout` spelling may be accepted as an alias if it helps diagnostics, but the Skill uses the shorter stable `moss call` form so host permission matching does not depend on generated paths.

The response envelope remains the existing `protocol.Response`. In stdio mode, business responses—including `ok: false` stable business errors—are written to stdout and the process exits with the existing success category. Stderr is reserved for diagnostics. If the request cannot be decoded, Moss still attempts to emit a structured `REQUEST_INVALID` response with an empty request ID; if no response can be emitted, the process returns a transport failure exit code.

The stream reader must enforce `protocol.MaxRequestBytes`, reject trailing JSON documents, and close/finish before dispatching a mutation. The response encoder must enforce `protocol.MaxResponseBytes` and write one JSON document followed by a newline. No log line may be written to stdout.

### 2. Keep file transport as an explicit compatibility adapter

`moss call --request <file> --response <file>` remains available during migration, but it is not documented in the Skill's normal workflow. It retains path validation, managed-root checks, atomic response replacement, and empty stdout semantics. The application dispatch path is shared by both transports so business behavior cannot drift.

This adapter supports old installed Skills, non-Claude automation, and deterministic transport tests. It may be removed in a later breaking release after the installed Skill population has migrated. The new Skill must not silently fall back to it when a stdio-capable binary is missing; a version mismatch must be reported as an installation/upgrade problem.

### 3. Use stdin for envelopes and files for domain artifacts

The Skill constructs a request envelope and supplies it to `moss call` through stdin, using a quoted heredoc or the host's equivalent stdin mechanism. It must not interpolate user content into argv, perform shell expansion on request values, or use a second human-oriented command.

The following remain file-backed because they are domain artifacts or model handoff boundaries:

- `source.ingest.arguments.input_file` points to the user-selected source.
- `compile.next` returns managed `input_files`, `schema_file`, and `result_file` paths.
- Claude writes only the current compile `result_file`; `compile.submit` receives that path through stdin and Moss validates it.
- Large materialized articles or backup archives may be returned as Moss-managed paths with a hash and size instead of being inlined in stdout.

Moss must never treat a Claude-written staging file as authoritative until the relevant operation validates it and applies the corresponding plan. Direct writes to SQLite, the final Wiki, or generated indexes remain outside the Skill's permission and workflow.

### 4. Reduce fixed preamble calls

The Skill performs `system.handshake`, `system.health`, and `system.capabilities` during installation, upgrade, repair, or an explicit maintenance request. Normal capture, retrieval, action, and forget workflows invoke their target operation directly after a compatible installation has been established. On `PROTOCOL_VERSION_UNSUPPORTED`, a missing binary, or a transport error, the Skill stops and enters the maintenance/repair path rather than repeating the preamble indefinitely.

If a single maintenance check is needed, the existing operations may be composed by a future `system.preflight`; this change does not add a general batch protocol. Operations whose next request depends on Claude's interpretation—such as candidates followed by materialize or compile stages—remain separate calls by design.

### 5. Preserve safety and retry semantics across transports

Request IDs are fresh per attempt. Mutating operations retain the same idempotency key for an identical retry, and the SQLite idempotency table remains the durable source for replay after a missing stdout response. A response lost after commit is therefore recovered by retrying the same request, not by retaining a temporary response file.

The stdio path uses the same request validation, operation-specific argument decoding, path checks, sensitivity checks, plan confirmation, and audit writes as the file path. Transport selection must not weaken confirmation or allow a stale plan to apply.

### 6. Refactor at the application boundary, not in each operation

The application layer should expose a transport-neutral request executor that accepts a decoded request and returns a `protocol.Response`. Thin wrappers handle bounded file reads/writes or bounded stream reads/writes. Cobra owns only transport flag validation and process exit mapping. Existing operation packages and storage transactions remain unchanged except where response data must expose a managed path for large content.

Protocol helpers should provide stream encoding/decoding alongside the existing atomic file writer. Tests must exercise both wrappers against the same dispatcher to prove parity.

## Risks / Trade-offs

- **[Risk]** Stdin JSON can appear in the Claude Code tool transcript even though it is not an argv value. → Keep sensitive or very large bodies in existing local files and pass only their paths; document this boundary in the Skill.
- **[Risk]** A response larger than the host's useful context may be emitted on stdout. → Keep bounded response limits and let materialization/export operations return managed paths, hashes, and sizes for large artifacts.
- **[Risk]** An old file-only binary may be paired with a new Skill. → Install the binary and Skill together, perform a stdio handshake during installation, and report upgrade-required instead of silently falling back.
- **[Risk]** A process can commit a mutation before stdout is lost. → Preserve SQLite idempotency replay and require the Skill to retry only retryable transport/business failures with the same idempotency key.
- **[Risk]** Removing the per-turn handshake could hide a broken installation. → Treat unsupported protocol, missing executable, and storage errors as explicit maintenance triggers; do not claim success when the response is missing.
- **[Risk]** Shell heredoc quoting could accidentally expand content. → Use a quoted delimiter, never interpolate shell variables into the request, and test metacharacters and multiline values.
- **[Trade-off]** One operation still requires one Moss process when Claude must inspect the result before choosing the next operation. → Optimize away protocol file calls and redundant preambles; do not introduce a general batch transaction whose partial-mutation semantics are harder to secure.

## Migration Plan

1. Add the transport-neutral executor and stdio reader/response writer while retaining the current file wrapper.
2. Update Cobra so `moss call` defaults to stdio and file flags are an explicit mutually exclusive compatibility mode.
3. Update protocol and application tests for valid requests, malformed input, oversized streams, structured business errors, missing stdout, idempotent replay, and file/stdio parity.
4. Update the embedded Skill, operation guide, CLI protocol reference, and compile workflow to stop creating request/response envelopes and to retain only domain staging paths.
5. Build and install the matching binary and Skill, then run real-binary handshake, health, capture, retrieval, and compile-stage simulations through stdin/stdout.
6. Keep the file adapter enabled for one migration period. If rollback is required, reinstall the previous matching Skill and binary; the SQLite schema and data layout do not change.
7. After all supported Skill installations use stdio, consider removing the compatibility flags in a separate breaking change.

## Open Questions

- Whether the first implementation should accept explicit `--stdin --stdout` aliases or make the no-flag `moss call` mode the only documented spelling.
- Which materialization operations should inline content versus return a managed path, and what conservative inline byte threshold is appropriate for Claude Code context limits.
- Whether the compatibility file adapter should be marked deprecated immediately or retained without deprecation until the next major protocol version.
