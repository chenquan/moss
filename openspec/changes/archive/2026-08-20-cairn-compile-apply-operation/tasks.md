## 1. Protocol and dispatch

- [x] 1.1 Add `compile.apply` to mutating validation and deterministic capability discovery.
- [x] 1.2 Route `compile.apply` to the existing compile plan application implementation while retaining generic `plan.apply` safety routing.

## 2. Skill and tests

- [x] 2.1 Update the Cairn Skill compile workflow and confirmation policy to use `compile.apply`.
- [x] 2.2 Add capability, missing-confirmation, successful-apply, and idempotent-retry coverage for `compile.apply`.
- [x] 2.3 Run strict OpenSpec validation and the full Go test/race/vet/build suite.
