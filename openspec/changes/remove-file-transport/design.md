## Context

Moss currently has a stdio-first `moss call` path plus a compatibility adapter that reads a request file and atomically writes a response file. The adapter was introduced as a migration bridge, but the only supported product caller is the local Claude Code Skill. Keeping both paths now means two Cobra modes, two application entrypoints, extra path validation, and continued guidance for protocol envelope files.

The JSON request/response envelope remains unchanged. This change removes only the process transport flags. Workflow artifacts remain file-backed where they represent durable user or job data: source inputs, compile schemas and results, managed articles, exports, backups, and recovery files.

## Goals / Non-Goals

**Goals:**

- Make `moss call` with stdin/stdout the only machine protocol entrypoint.
- Reject `--request`, `--response`, `--stdin`, `--stdout`, positional arguments, and other transport spellings before dispatch.
- Remove the unused file transport wrappers and protocol envelope path helpers.
- Keep one shared request decoder, validator, dispatcher, response writer, idempotency path, and structured error contract.
- Keep the existing pre-release Skill/runtime version and require stdio preflight for the matching pair during installation or upgrade.
- Preserve safe retry after a lost stdout response by reusing the original idempotency key.
- Keep domain path validation and staging ownership unchanged.

**Non-Goals:**

- Do not remove `input_file`, `schema_file`, `result_file`, article paths, export paths, backup paths, or other Moss-issued workflow artifacts.
- Do not add a general batch protocol, daemon, Web UI, MCP server, or human business subcommand.
- Do not change the JSON envelope schema or SQLite/Wiki transaction model.
- Do not add a hidden or undocumented file-transport fallback.
- Do not support non-Claude clients in this change; a future adapter would be a separate proposal.

## Decisions

### 1. Make the no-flag `call` mode the only transport

`cmd/call.go` will dispatch directly to `app.RunCallStdio` and will define no request/response or stdin/stdout transport flags. Cobra continues to enforce `NoArgs`; unknown flags fail with a usage error and no business response. This keeps the machine interface to one stable invocation and avoids a second mode that could drift.

The explicit `--stdin --stdout` aliases are removed along with the file flags. They do not provide capability beyond the default and would keep unnecessary transport syntax in the interface.

### 2. Remove only protocol envelope file code

Delete the application file wrapper, bounded file reader, atomic protocol response writer, and request/response path validators once their callers and tests are removed. Keep `ReadRequest`, `WriteResponseStream`, bounded response encoding, domain path safety, and all operation-level staging validation.

The application continues to build a structured response for malformed requests and business failures. A stdout write failure remains a transport failure; it is not converted into a false success.

### 3. Version the executable/Skill pair, not the envelope schema

Keep `protocol_version` at `1.0` because the JSON request and response documents are unchanged. Keep the existing CLI and Skill compatibility versions at `0.1.0` because Moss has not been publicly released; expose the existing pair through the handshake without introducing a release-version migration. A new Skill never retries with file flags when the installed runtime is old or incompatible.

Because command-line flag parsing occurs before a request can reach `system.handshake`, compatibility cannot be negotiated by an old Skill invoking a removed flag. Installation and upgrade therefore stage or install the matching binary and Skill as one pair, run stdio `system.handshake` and `system.health`, and only then declare readiness.

### 4. Treat lost stdout as an idempotent retry case

The Skill preserves the exact `request_id` and `idempotency_key` for every mutating call. If the process exits after committing but before Claude receives a complete response, the Skill retries the identical request. The existing idempotency store returns the original committed response or a stable conflict error; the Skill never generates a new mutation key just because stdout was lost.

### 5. Keep shell and content safety on the stdin boundary

The Skill sends one bounded JSON document through stdin and keeps user content out of argv and unquoted shell syntax. It uses only Moss-issued domain paths for staging or large content. Removing envelope files reduces plaintext leftovers and path-race exposure, but does not relax the quoting and prompt-injection rules.

## Risks / Trade-offs

- **[Breaking compatibility]** Older Skills or automation that pass `--request`/`--response` fail before handshake. → Install the matching binary and Skill together, report an actionable version mismatch, and do not provide a hidden fallback.
- **[Lost response after mutation]** A process can commit successfully and lose stdout before Claude reads it. → Require idempotency keys for all mutations and retry the identical request; retain audit records.
- **[Less convenient replay]** Protocol envelope files are no longer produced automatically. → Use request IDs, audit data, managed staging artifacts, and caller-controlled stdin/stdout redirection when an external diagnostic replay is explicitly needed.
- **[Future launcher limitations]** A scheduler or GUI that cannot provide stdin needs an adapter. → Keep that outside the v1 product boundary and introduce it as a separate transport proposal if required.
- **[Upgrade mismatch]** Updating only the Skill or only the binary leaves the pair unusable. → Preflight the new pair with handshake and health, back up before replacement, and roll back both components together on failure.

## Migration Plan

1. Update the bundled Skill and protocol specifications to remove file transport references.
2. Build and stage the new Moss binary and matching Skill resources.
3. Before replacing an installed pair, create the existing local backup and record the current binary/Skill versions.
4. Install the new pair, invoke `moss call` with stdio `system.handshake` and `system.health`, and verify the data root and migration state.
5. If preflight fails, restore the previous binary and Skill pair and keep the data backup available; do not attempt a removed file transport.
6. After readiness, normal Skill workflows invoke only `moss call` through stdin/stdout. Existing domain artifacts and SQLite data are read without migration.
7. For rollback, restore both the old binary and old Skill resources, not just one component.

## Open Questions

None for the v1 local Claude Code boundary. Supporting a non-stdin launcher should be proposed as a separate transport rather than reintroducing protocol request/response flags.
