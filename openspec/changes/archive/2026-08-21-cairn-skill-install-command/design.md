## Context

Cairn is distributed as a local Go binary, while the model-facing Skill is a directory of Markdown resources. Claude Code and Codex resolve global and project-local skills from different directories. The installer must work from an installed binary, so it cannot rely on the repository being present at runtime.

## Goals / Non-Goals

**Goals:**

- Provide a deterministic `skill install` command with global/project scope and Claude/Codex target selection.
- Bundle the exact Skill resources into the binary and install them without shell interpolation or network access.
- Resolve global Codex installation through `CODEX_HOME` when set, otherwise the user's `.codex` directory.
- Refuse accidental overwrites by default and make repeated identical installs succeed.
- Preserve the machine-only semantics of `call` and the Skill's hidden user-interface role.

**Non-Goals:**

- No remote downloader, package manager, Web UI, MCP integration, or automatic account configuration.
- No installation of the Cairn binary itself; this command installs Skill resources only.
- No deletion of stale files from an existing Skill directory.

## Decisions

1. **Use `skill install` as the explicit setup exception.** The existing `call` command remains the only machine protocol path. The installer prints human-readable status because it is intentionally invoked by a user during setup.

2. **Bundle resources with `go:embed`.** Copy the checked-in `.claude/skills/cairn` tree into `internal/skill/assets` and embed it. A content test compares the bundled tree with the source-visible tree so the installed Skill cannot silently drift.

3. **Use target and scope flags.** `--target` accepts `claude`, `codex`, or `both` and defaults to `claude`. `--scope` accepts `global` or `project` and defaults to `global`. Global Claude resolves to `~/.claude/skills/cairn`; global Codex resolves to `$CODEX_HOME/skills/cairn` or `~/.codex/skills/cairn`; project paths are relative to the current working directory.

4. **Preflight conflicts before writing.** Existing files with identical bytes are skipped. Any differing file causes a conflict unless `--force` is set. Writes use private temporary files and rename, and force never deletes unrelated files.

5. **Keep the command human-safe.** The installer validates target/scope values, reports destination and counts, never executes installed Markdown, and never writes user data or Cairn database state.

## Risks / Trade-offs

- **[Risk]** Duplicated source and embedded Skill trees can drift. → Add a test that walks both trees and compares every file's bytes.
- **[Risk]** `--force` can overwrite user edits to Skill files. → Require the explicit flag, preflight all conflicts, and never remove extra files.
- **[Risk]** Global path conventions vary by environment. → Honor `CODEX_HOME`, use the OS home directory, and report the resolved path.
- **[Risk]** Adding a visible command weakens the previous “only call” wording. → Document it as the sole setup exception and keep all business operations behind `call`.

## Migration Plan

1. Add embedded assets and installer path/write logic.
2. Add `skill install` to the Cobra tree and update command tests.
3. Add Skill and CLI documentation/specs and run install simulations in temporary directories.
4. Roll back by reverting the installer command and embedded assets; no user data migration is required.

## Open Questions

None. The default target is Claude because Cairn's primary runtime is Claude Code; Codex and both-target installs are explicit options.
