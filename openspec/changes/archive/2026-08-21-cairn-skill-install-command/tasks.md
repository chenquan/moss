## 1. Bundle and implement the installer

- [x] 1.1 Bundle the checked-in Cairn Skill resources under `internal/skill/assets` and add a source/bundle parity test.
- [x] 1.2 Implement target/scope path resolution for Claude/Codex, including `CODEX_HOME`, project working directory, defaults, and input validation.
- [x] 1.3 Implement conflict-safe, atomic file installation with idempotent identical installs and explicit `--force` overwrite.
- [x] 1.4 Add Cobra `skill install` with `--target`, `--scope`, and `--force`, while preserving the machine-only `call` behavior.

## 2. Tests, documentation, and delivery

- [x] 2.1 Add installer unit tests and command tests for defaults, target/scope paths, conflicts, force, both targets, and human output.
- [x] 2.2 Update Skill/protocol documentation and run Go tests, vet, strict OpenSpec validation, and isolated binary install simulations.
- [x] 2.3 Archive the completed OpenSpec change after syncing `skill-installation` and `cli-protocol` specifications.
