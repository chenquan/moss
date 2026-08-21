## Why

The shipped Cairn Skill tells Claude Code which workflows exist, but it does not provide one authoritative operation-level guide for constructing requests, carrying IDs between calls, handling responses, or applying confirmation and retry rules. Without that guide, a model can understand the product concept yet still route or invoke the local CLI inconsistently.

## What Changes

- Add a machine-oriented Skill operation guide with intent routing, request templates, required arguments, mutation/idempotency rules, and response/error handling.
- Expand the protocol resource with the request/response envelope, file lifecycle, output discipline, and operation matrix.
- Update `SKILL.md` to make the guide a required internal reference and to define a deterministic route from user intent to operation sequence.
- Add content tests that prevent the shipped Skill from losing required operation and safety instructions.
- Keep the user-facing boundary unchanged: Claude Code remains the only interface and the CLI still exposes only `call`.

## Capabilities

### New Capabilities

<!-- No new product capability; this improves the existing Skill contract. -->

### Modified Capabilities

- `cairn-skill`: require an operation-level guide so Claude can safely construct, execute, resume, and explain Cairn protocol calls.

## Impact

- Updates `.claude/skills/cairn/SKILL.md` and its protocol resources.
- Adds no Go dependencies, CLI commands, database migrations, or JSON protocol fields.
- Extends Skill content tests in `internal/skill`.
