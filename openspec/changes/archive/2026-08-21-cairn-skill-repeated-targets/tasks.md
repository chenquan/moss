## 1. Update the target-selection contract

- [x] 1.1 Change `InstallOptions` and target normalization to accept repeatable Claude/Codex values, de-duplicate them, and reject `both` with migration guidance.
- [x] 1.2 Change Cobra `skill install --target` to a repeatable flag, update help text, and preserve the Claude default.

## 2. Verify and document the behavior

- [x] 2.1 Update installer and command tests for repeated targets, de-duplication, rejected `both`, and unchanged defaults/conflict behavior.
- [x] 2.2 Update Skill guidance and embedded assets, then run Go tests, race tests, vet, and strict OpenSpec validation.
- [x] 2.3 Build and run the real binary in isolated global/project directories, sync/archive this change, and commit the result.
