## Why

Cairn's machine CLI currently parses its sole `call` entrypoint manually in `internal/app`, which makes command parsing, required flags, exit behavior, and future command-boundary tests harder to maintain. The project should use the installed `cobra-cli` generator and the Cobra library while preserving the strict machine-only interface promised to the Claude Skill.

## What Changes

- Initialize the repository with `cobra-cli init .` and generate the `call` command skeleton with `cobra-cli add call`.
- Adopt the standard Cobra layout with root `main.go`, `cmd/root.go`, and `cmd/call.go`; remove the old nested executable entrypoint.
- Move request/response execution behind a reusable application function while Cobra owns only command parsing and process-boundary dispatch.
- Keep exactly one visible operation, `call`, with required `--request` and `--response` paths.
- Disable Cobra's human-oriented help, completion, suggestions, and usage output so stdout/stderr and exit-code behavior remain machine-safe.
- Add the direct Cobra dependency without introducing Viper or changing the JSON protocol, storage, Skill workflows, or operation set.

## Capabilities

### New Capabilities

<!-- No new product capability; this is a CLI boundary refactor. -->

### Modified Capabilities

- `cli-protocol`: preserve the file-based `call` contract while making Cobra the command parsing boundary and retaining machine-only output discipline.
- `cairn-skill`: preserve the exact `cairn-cli call` invocation and compatibility behavior after the entrypoint move.

## Impact

- Adds the `github.com/spf13/cobra` dependency and generated Cobra command files.
- Changes the development build target from `./cmd/cairn-cli` to the repository root (`go build .`).
- Does not add human-facing subcommands, Web UI, MCP, interactive prompts, model calls, database migrations, or protocol changes.
