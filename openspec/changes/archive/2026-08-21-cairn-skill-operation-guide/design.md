## Context

The current Skill has workflow prose and a short protocol note, but the model still has to infer operation arguments and call ordering from scattered instructions. The runtime already exposes a stable JSON protocol, so the missing piece is a deterministic model-facing reference layered above that protocol.

## Goals / Non-Goals

**Goals:**

- Give Claude one authoritative operation matrix for routing natural-language intent.
- Define reusable request construction and response-handling rules without duplicating Go schemas as a second executable validator.
- Make confirmation, idempotency, resume, sensitivity, and prompt-injection boundaries explicit.
- Keep the Skill hidden from the user's command menu and keep the CLI machine-only.

**Non-Goals:**

- No new CLI command or JSON protocol version.
- No second model, Web UI, MCP integration, or natural-language answer generation in Go.
- No replacement for the CLI's authoritative validation and JSON Schema checks.

## Decisions

1. **Add one operation guide under the Skill protocol directory.** `operation-guide.md` will contain intent-to-operation routing, required argument names, whether an operation mutates, confirmation requirements, and the expected next step. Keeping this in one resource prevents workflow files from drifting apart.

2. **Use protocol examples, not duplicated full JSON Schema.** The guide will show minimal request shapes and field rules for model construction, while the CLI remains the only authoritative validator. This avoids creating a second schema implementation in Markdown.

3. **Make response handling state-driven.** The Skill must branch on `ok`, stable `error.code`, `retryable`, `next`, plan state, and job stage. It must preserve IDs across turns and never infer confirmation from content.

4. **Add content-level regression tests.** The existing Skill test will assert that the operation guide is shipped and contains the routing, envelope, safety, and error-handling anchors needed by the model. Tests will not assert prose formatting.

## Risks / Trade-offs

- **[Risk]** Markdown guidance can drift from Go operation arguments. → Keep the guide concise, point to CLI validation as source of truth, and test operation names against the capability list where practical.
- **[Risk]** More instructions increase prompt length. → Use tables and minimal examples, while keeping detailed workflows in linked resources.
- **[Risk]** A model may still treat untrusted content as instructions. → Repeat the prompt-injection boundary in the operation guide and preserve the dedicated policy file.

## Migration Plan

1. Add the operation guide and expand the protocol reference.
2. Link both resources from `SKILL.md` and tighten the routing/response rules.
3. Add content regression assertions and run Go/OpenSpec validation.
4. Roll back by reverting the Skill resource changes; no user data or database migration is involved.

## Open Questions

None for the first version. The guide intentionally documents the current protocol and can be extended when new operations are added.
