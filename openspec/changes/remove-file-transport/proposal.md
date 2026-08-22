## Why

Moss is an internal execution runtime for the Moss Skill, and the supported user environment already provides a process stdin/stdout boundary. Keeping `--request <file> --response <file>` preserves a second protocol transport, forces callers to manage protocol envelope files, and creates a compatibility surface that the Skill never needs. The product is still in the pre-release convergence phase, so this is the appropriate point to make the machine boundary unambiguous.

## What Changes

- **BREAKING**: Remove the `--request` and `--response` flags and the request/response-file transport from `moss call`.
- Make stdin/stdout the only protocol transport; reject transport flags and positional arguments before dispatch.
- Remove file-transport application wrappers, protocol response-file helpers, and compatibility-only path validation that have no remaining callers.
- Keep domain artifacts such as `input_file`, `schema_file`, `result_file`, managed article paths, and backup paths; these are workflow data, not protocol envelopes.
- Update the Moss Skill, operation guide, workflows, and OpenSpec requirements so normal and maintenance calls use stdin/stdout only.
- Keep the existing pre-release CLI/Skill compatibility version and check the installed Skill and binary as a matching pair; never fall back to a file transport.
- Preserve idempotent mutation retry semantics for a completed operation whose stdout response is lost.
- Add regression, migration, and real-binary tests proving that file flags are rejected and no protocol envelope files are required.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cli-protocol`: make stdin/stdout the sole machine transport and remove the file-based call contract.
- `cairn-skill`: remove file-envelope invocation and require a stdio-only compatible Moss runtime.
- `bootstrap-upgrade`: require matching Skill/runtime replacement and stdio preflight before normal writes after an upgrade.

## Impact

- Affected Go code: `cmd/call.go`, `internal/app`, `internal/protocol`, and their tests.
- Affected bundled resources: `internal/skill/assets/SKILL.md`, operation guidance, workflows, and protocol documentation.
- Existing callers that still pass `--request` or `--response` will fail with a usage error and must upgrade together with the Skill.
- SQLite, managed Wiki, Raw sources, compile staging, plans, backups, and audit storage remain unchanged; only the protocol envelope transport is removed.
