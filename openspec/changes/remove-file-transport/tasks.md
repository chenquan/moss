## 1. Remove the compatibility transport

- [x] 1.1 Remove request/response and stdin/stdout transport flags from `cmd/call.go`; dispatch only the default stdin/stdout path and reject all positional or transport arguments.
- [x] 1.2 Remove `app.RunCall`, the bounded request-file reader, and compatibility-only application branching while preserving shared request validation, dispatch, idempotency, and structured errors.
- [x] 1.3 Remove protocol request/response file writers and request/response path validators that have no remaining callers; retain bounded stream decoding and response writing.
- [x] 1.4 Keep the pre-release CLI and Skill compatibility version at `0.1.0` while keeping the JSON `protocol_version` unchanged.

## 2. Update the Skill and upgrade boundary

- [x] 2.1 Remove all normal, migration, and troubleshooting references to protocol request/response files and transport flags from the bundled Skill and operation guide.
- [x] 2.2 Update capture, compile, retrieval, action, forget, and maintenance workflows to use only stdin/stdout while preserving Moss-issued domain staging and backup paths.
- [x] 2.3 Strengthen bootstrap and upgrade guidance to install the matching binary/Skill pair, run stdio handshake and health checks, and roll back both components without a file-transport fallback.
- [x] 2.4 Keep the content-safety, prompt-injection, confirmation, idempotent retry, and authoritative-storage ownership rules explicit after the transport cleanup.

## 3. Repair regression coverage

- [x] 3.1 Update Cobra tests for the stdio-only command surface, including rejection of `--request`, `--response`, `--stdin`, `--stdout`, positional arguments, and unknown flags.
- [x] 3.2 Remove file-transport application and protocol tests and preserve tests for bounded stdin, one response document, diagnostics-only stderr, and stdout failure handling.
- [x] 3.3 Update Skill content tests to forbid compatibility transport syntax while requiring stdin/stdout, matching-version, staging-ownership, and retry guidance.
- [x] 3.4 Add an idempotent retry test that executes a mutation twice with the same key and confirms the second call returns the original result without duplicating state.

## 4. Validate the migration boundary

- [x] 4.1 Build the real binary and verify stdio handshake, health, source ingestion, retrieval, action planning, and compile staging submit without protocol envelope files.
- [x] 4.2 Verify stale transport flags fail before dispatch, stdout contains no business response for usage failures, and user content never appears in process arguments.
- [x] 4.3 Verify the installed Codex and Claude Skill resources match the built binary version and that failed preflight does not alter the data root.
- [x] 4.4 Run the complete Go suite, OpenSpec strict validation, `git diff --check`, and the Skill validator; record any intentional cross-runtime frontmatter limitation.
- [x] 4.5 Review the final diff for accidental removal of domain artifacts or authoritative storage safeguards, then mark the change ready to archive.

### Verification notes

- `GOCACHE=/private/tmp/moss-gocache go test ./...`, `openspec validate --all --strict` (21/21), and `git diff --check` pass.
- The generic Skill validator rejects the intentional `compatibility` and `user-invocable` frontmatter keys because its allowlist covers a narrower runtime schema. These keys remain required for the Claude/Codex Skill contract; the stdio-only Moss invocation boundary is unchanged.
- `go vet ./...` now passes; the storage path tests use a local `os.Chdir`/`t.Cleanup` helper compatible with the module's Go 1.23 declaration instead of `testing.Chdir`.
- Moss is not publicly released, so the implementation intentionally keeps the existing `0.1.0` CLI/Skill compatibility version; only the transport boundary changes.
