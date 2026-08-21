## Context

Cairn's explicit setup command currently accepts `--target claude`, `--target codex`, or the combined `--target both`. The combined value is a special case in both the CLI surface and installer API. The requested interface is a repeatable flag so the same option expresses one or multiple destinations.

## Goals / Non-Goals

**Goals:**

- Make `--target` repeatable, including `--target codex --target claude`.
- Preserve Claude as the default when no target is supplied.
- Preserve deterministic destination resolution, conflict preflight, and result reporting for every selected target.
- Reject `both` and other unsupported values with actionable guidance.

**Non-Goals:**

- No changes to global/project path conventions, force semantics, embedded resources, or the machine `call` protocol.
- No comma-separated target syntax or interactive target selection.

## Decisions

1. **Use Cobra's `StringArrayVar`.** Each `--target value` occurrence becomes one slice element, so the documented repeated form is explicit and values containing commas are not silently split.

2. **Normalize in the installer API.** `InstallOptions.Targets` is normalized centrally: blank/no values default to Claude, accepted values are `claude` and `codex`, duplicates are removed in first-seen order, and `both` receives a migration hint.

3. **Keep result order stable.** The installer resolves and preflights targets in normalized order, then installs and reports results in that same order. This makes command output and tests deterministic.

4. **Keep the compatibility boundary narrow.** The old exported `TargetBoth` constant and scalar `Target` option are removed because they encode the interface the user asked to replace. Existing callers must use `Targets: []string{...}`.

## Risks / Trade-offs

- **[Risk]** Callers using the old Go API or `--target both` will fail. → Treat this as an intentional breaking setup-interface change and return a direct repeat-flag migration message.
- **[Risk]** Users repeat the same target accidentally. → De-duplicate before preflight/install so a target is installed once.
- **[Risk]** Help text drifts from Skill guidance. → Update both checked-in guidance and its embedded parity test.

## Migration Plan

1. Change the installer API and Cobra flag, then update unit/command tests.
2. Update Skill workflow guidance and the main `skill-installation` specification through the OpenSpec archive flow.
3. Run package tests, race tests, vet, strict OpenSpec validation, and an isolated built-binary simulation for both targets and the rejected alias.

## Open Questions

None.
