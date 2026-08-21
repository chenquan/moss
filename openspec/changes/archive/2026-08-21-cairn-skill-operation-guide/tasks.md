## 1. Build the model-facing operation guide

- [x] 1.1 Add `protocol/operation-guide.md` with intent routing, request envelope, operation matrix, required arguments, mutation/idempotency rules, and confirmation boundaries.
- [x] 1.2 Expand `protocol/cli-protocol.md` with file lifecycle, response branching, stable errors, resume rules, and output discipline.
- [x] 1.3 Update `SKILL.md` to require these protocol resources and define deterministic routing across capture, compile, retrieve, action, maintenance, forget, and rollback workflows.

## 2. Validate and deliver the Skill resource

- [x] 2.1 Add Skill content tests for the operation guide, operation coverage, request/response rules, and safety anchors.
- [x] 2.2 Run Go tests, strict OpenSpec validation, and inspect the final Git diff for accidental local settings or user data.
- [x] 2.3 Archive the completed OpenSpec change after syncing the `cairn-skill` specification.
