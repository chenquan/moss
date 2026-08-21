## 1. Generate the Cobra project boundary

- [x] 1.1 Run `cobra-cli init . --author "Cairn" --license none` at the module root and verify the generated root layout and Cobra dependency.
- [x] 1.2 Run `cobra-cli add call`, then replace generated placeholders with a root command factory and a machine-only `call` command.
- [x] 1.3 Remove the old nested `cmd/cairn-cli/main.go` entrypoint and make the generated root `main.go` the only executable entrypoint.

## 2. Preserve the application protocol

- [x] 2.1 Extract the existing request/response execution into a reusable application call function with the existing exit categories and diagnostics.
- [x] 2.2 Bind only `--request` and `--response` in Cobra, enforce required flags/no positional arguments, and disable human-oriented help, completion, usage, and suggestions.
- [x] 2.3 Keep the JSON protocol, operation dispatch, atomic response writes, stdout/stderr discipline, and Skill invocation unchanged.

## 3. Tests, documentation, and validation

- [x] 3.1 Add Cobra command tests for the exact command tree, required flags, invalid arguments, machine-only output, and valid call dispatch.
- [x] 3.2 Update application/entrypoint tests and any build references to use the root binary without changing domain behavior.
- [x] 3.3 Run strict OpenSpec validation, unit tests, race tests, vet, root build, and CLI smoke checks.
