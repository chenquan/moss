## Why

The Skill installer currently uses a special `both` target value when a user wants both editor integrations. A repeatable `--target` flag is clearer, composes with normal flag parsing, and lets Claude explicitly select each destination without maintaining a combined alias.

## What Changes

- **BREAKING** Replace the `--target both` value with repeatable `--target claude --target codex` arguments.
- Keep a single target invocation valid and preserve the no-flag default of Claude.
- De-duplicate repeated target values while preserving the first-seen installation order.
- Update the Go installer API, Cobra help, tests, Skill workflow guidance, and the installation specification.
- Reject the removed `both` value with guidance to repeat `--target`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `skill-installation`: select one or more Skill targets using repeated `--target` flags and remove the `both` alias.

## Impact

- Changes `internal/skill.InstallOptions` from one target string to a target slice.
- Changes the `skill install` Cobra flag from a scalar string to a repeatable string-array flag.
- Updates installer and command tests, embedded Skill guidance, and the main OpenSpec requirement after archive.
- No runtime `call` protocol, data model, or installation path changes.
