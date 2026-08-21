## Context

The compile package already has an idempotent `Apply` implementation that validates a pending plan, checks expiry and Wiki drift, writes the managed article atomically, and records audit/idempotency state. The app dispatcher currently reaches it through `plan.apply`, which conflates compile knowledge writes with forget/rollback safety plans and omits the explicitly agreed `compile.apply` capability.

## Goals / Non-Goals

**Goals:**

- Expose `compile.apply` as a thin protocol-level gateway to the existing compile `Apply` implementation.
- Keep the operation's confirmation, plan-state, version/hash, recovery, audit, and idempotency checks unchanged.
- Retain `plan.apply` for generic safety plans and preserve compatibility for existing requests.
- Align the Skill, protocol capability list, OpenSpec requirements, and integration tests.

**Non-Goals:**

- No new storage tables, plan format, Markdown writer, confirmation mechanism, or CLI subcommand.
- No removal of `plan.apply`, no Web UI, MCP, or second model.

## Decisions

1. **Alias at dispatch, not in compile storage.** Add `compile.apply` to the mutating operation set and route it directly to `compile.Apply`. This avoids duplicating plan logic or changing persisted plan records. A separate implementation would create divergent drift and retry behavior.
2. **Use operation-specific Skill routing.** The compile workflow calls `compile.apply`; forget and rollback continue using `plan.apply`. The user still sees only Claude conversation, and all operation details remain internal.
3. **Keep backward compatibility.** Existing `plan.apply` requests continue dispatching to compile plans first and safety plans on `PLAN_NOT_FOUND`, so already-created jobs and callers remain valid while new handshakes advertise the complete set.

## Risks / Trade-offs

- **[Risk]** Two operation names can be confused by an older Skill. → `system.capabilities` advertises both, and `system.handshake` remains the compatibility gate; the old generic path stays supported.
- **[Risk]** A caller could send a safety plan to `compile.apply`. → The compile implementation returns `PLAN_NOT_FOUND` without mutation; Skill policy chooses the correct gateway.

## Migration Plan

1. Add the operation to protocol validation/capabilities and app dispatch.
2. Update Skill instructions and workflow resources.
3. Add alias, confirmation, idempotency, and capability integration tests.
4. Ship as an additive protocol operation; no database migration is required.

## Open Questions

None. Future operation deprecations require a separate protocol change.
