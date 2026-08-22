## 1. Transport-neutral protocol and application execution

- [x] 1.1 Add bounded stream request decoding and one-document validation in `internal/protocol`, preserving `MaxRequestBytes`, strict unknown-field rejection, and stable malformed-input errors.
- [x] 1.2 Add response encoding to an `io.Writer` with `MaxResponseBytes`, one-document newline output, and no diagnostic output on the business stream.
- [x] 1.3 Refactor `internal/app` so request validation, version checks, dispatch, idempotency, and response construction are shared by stdio and compatibility file transports.
- [x] 1.4 Add `RunCallStdio` (or equivalent) with structured responses for malformed input, bounded reads, dispatch failures, and stdout transport failures while preserving existing exit categories.
- [x] 1.5 Keep the existing atomic file response wrapper and path safety checks as an explicit compatibility transport, with no business behavior duplicated from the stdio executor.

## 2. Cobra machine boundary

- [x] 2.1 Change `cmd/call.go` so `moss call` defaults to stdin/stdout and rejects positional arguments or incomplete/mixed transport flags.
- [x] 2.2 Retain `--request <file> --response <file>` only when both flags are supplied, preserving empty stdout and existing usage diagnostics for the compatibility path.
- [x] 2.3 Update CLI tests for valid stdio calls, file-mode parity, malformed/oversized stdin, unknown flags, mixed modes, missing paths, and the absence of human business subcommands.

## 3. Skill and operation guidance

- [x] 3.1 Update `internal/skill/assets/SKILL.md` runtime contract and response discipline to invoke `moss call` through stdin/stdout without creating protocol request/response files.
- [x] 3.2 Update capture, maintenance, retrieval, action, forget, and bootstrap workflows to remove per-call envelope files and to invoke handshake/health/capabilities only during installation, upgrade, repair, or explicit maintenance.
- [x] 3.3 Document the quoted-stdin safety rule: no user content in argv or unquoted shell syntax, and only Moss-issued `input_file`, `schema_file`, `result_file`, article, and backup paths may be used as domain artifacts.
- [x] 3.4 Update `internal/skill/assets/protocol/cli-protocol.md` and `operation-guide.md` with the stdio envelope lifecycle, error handling, compatibility file mode, idempotent retry, and large-artifact path behavior.
- [x] 3.5 Update compile workflow guidance and content tests so Claude writes only the managed current-stage result file and never writes authoritative SQLite, Wiki, index, plan, trash, backup, or audit files directly.

## 4. Large response and staging behavior

- [x] 4.1 Audit `knowledge.materialize`, export, restore, and other large-result operations to ensure stdout responses stay within protocol bounds and expose managed paths, hashes, and sizes where inline content is too large.
- [x] 4.2 Preserve compile `input_files`, `schema_file`, and `result_file` lifecycle and verify `compile.submit` still validates exact managed staging paths when its request envelope arrives through stdin.
- [x] 4.3 Add or update path and sensitivity tests covering model-written staging files, traversal/symlink rejection, and the prohibition on direct authoritative writes.

## 5. Integration, migration, and verification

- [x] 5.1 Update active OpenSpec-linked documentation/tests for the changed `cli-protocol` and `cairn-skill` requirements without altering unrelated archived capabilities.
- [x] 5.2 Run the complete Go test suite and protocol/Skill content tests, including stdio/file transport parity and idempotent mutation retry after discarded stdout.
- [x] 5.3 Build a real Moss binary and simulate handshake, health, source ingestion, retrieval, action planning, and a compile staging submit using stdin/stdout with no protocol request/response files.
- [x] 5.4 Verify stderr contains diagnostics only, stdout contains exactly one response document per call, no user content appears in process arguments, and the installed Skill uses the matching `moss` binary.
- [x] 5.5 Run `openspec validate --all --strict`, `git diff --check`, and the Skill validator; record any intentional cross-runtime frontmatter limitation without weakening the Moss invocation boundary.

### Verification notes

- The Go suite, protocol/Skill content tests, real-binary stdio smoke flow, `openspec validate --all --strict`, and `git diff --check` pass.
- The generic Skill validator rejects the intentional `compatibility` and `user-invocable` frontmatter keys because its allowlist only covers a narrower runtime schema. These keys are retained for the Claude/Codex Skill contract; they do not change the `moss call` stdin/stdout invocation boundary.
