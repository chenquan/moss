## Context

Cairn is already a stable machine protocol runtime. The current `cmd/cairn-cli/main.go` delegates to `internal/app.Run`, which manually recognizes `call`, `--request`, and `--response`. The requested refactor changes only the process boundary: Cobra should own command construction and flag parsing, while the application layer should continue to own request validation, dispatch, response-file atomicity, and stable business/transport semantics.

## Goals / Non-Goals

**Goals:**

- Use the installed `cobra-cli` generator to initialize the repository and generate the `call` command skeleton.
- Adopt the standard root `main.go` + `cmd/root.go` + `cmd/call.go` layout.
- Expose exactly one command, `call`, with required request/response file flags and no positional arguments.
- Keep business data in response files, stdout empty for valid calls, stderr diagnostic-only, and existing exit-code behavior stable.
- Make the command construction testable without launching a process.

**Non-Goals:**

- No changes to the JSON protocol, operation names, storage, database schema, Skill workflow, or domain packages.
- No Viper configuration, shell completion, human command set, interactive prompt, Web UI, MCP, or second model.

## Decisions

1. **Initialize at the module root.** Run `cobra-cli init . --author "Cairn" --license none`, then `cobra-cli add call`. This produces the standard layout and lets the generator add the direct Cobra dependency. The generated root `main.go` becomes the binary entrypoint; the old nested `cmd/cairn-cli/main.go` is removed to avoid two executable packages.
2. **Keep the application boundary below Cobra.** `cmd/call.go` binds only the two file paths and invokes an exported `internal/app` call-execution function. It does not parse JSON or dispatch operations. This keeps Cobra replaceable and prevents domain packages from importing command code.
3. **Use a command factory.** `cmd.NewRootCommand(stdout, stderr)` constructs a fresh command tree for every invocation/test. The root registers only `call`, uses `SilenceUsage`/`SilenceErrors`, disables suggestions, and replaces the default help behavior with a machine-only diagnostic. The command marks both flags required and rejects positional arguments.
4. **Preserve process semantics.** The application call function returns the existing exit categories (`0` for a written business response, `1` for transport failure, `2` for usage/path failure). The root entrypoint maps those categories to `os.Exit` without allowing Cobra to print usage or duplicate errors.
5. **No Viper.** `cobra-cli init` is run without `--viper`; the project remains environment/data-root driven through its existing storage code.

## Risks / Trade-offs

- **[Risk]** Cobra's generated help and usage could expose a human-oriented interface. → Replace the generated root behavior, register only `call`, silence usage/errors, and add tests for command discovery and help output.
- **[Risk]** Moving the binary from `./cmd/cairn-cli` to the module root could break local scripts. → Update build/test documentation and verify `go build .`; the installed binary name and Skill invocation remain `cairn-cli`.
- **[Risk]** Required-flag validation could return a different error before the application writes a response. → Treat missing CLI flags as usage errors, while valid request files continue through the existing response-file business-error path.

## Migration Plan

1. Create and validate this OpenSpec change.
2. Run the two Cobra generator commands at the module root.
3. Replace generated placeholders with the machine-only command factory and move the current call execution behind it.
4. Remove the old nested entrypoint and update tests/build references.
5. Run unit, race, vet, build, strict OpenSpec, and CLI smoke checks.
6. Verify the generated dependency and archive the change with specs synced.

Rollback is a source-level revert of the command-boundary files and `go.mod`/`go.sum`; no user data or database migration is involved.

## Open Questions

None. The standard Cobra root layout was selected before implementation.
