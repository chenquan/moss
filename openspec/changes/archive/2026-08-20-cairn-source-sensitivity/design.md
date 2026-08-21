## Context

Sources are immutable Raw content with mutable metadata. Ingestion records a sensitivity classification, and compile/action validation already prevents newly derived records from being less sensitive than their source. Users still need to correct a classification after capture without duplicating the Raw blob or editing SQLite directly.

## Goals / Non-Goals

**Goals:**

- Add a single idempotent `source.mark_sensitive` mutation with stable validation and audit behavior.
- Require explicit confirmation for a privacy loosening (restricted → sensitive/normal or sensitive → normal).
- When tightening a source, propagate the stricter classification to active derived articles and actions that cite/link that source; never lower an already stricter derived record.
- Keep forgotten sources unavailable and never return source content in the response.

**Non-Goals:**

- Rewriting Raw bytes, re-running compilation, or changing historical article versions.
- Automatically lowering derived sensitivity when a source is made less sensitive.
- Introducing a separate human CLI command or interactive prompt.

## Decisions

### Direct idempotent mutation

`source.mark_sensitive` is a direct machine operation rather than a plan because the user requested metadata correction, not content deletion or Wiki replacement. It still uses the standard request fingerprint, idempotency table, SQLite transaction, stable errors, and audit event. Same-value requests return `changed: false` and replay safely.

### Monotonic propagation for tightening

The transaction reads the active source and updates only active articles cited by that source and actions linked to it when their rank is below the requested rank. Existing stricter values are preserved. A lowering request changes only the source after explicit confirmation; derived records remain at their current classification so a mistaken downgrade cannot expose compiled content.

### Stable response metadata

The response reports source ID, previous/new sensitivity, whether the source changed, and bounded IDs of propagated articles/actions. It contains no Raw bytes, Wiki body, action details, or origin name.

## Risks / Trade-offs

- **[Risk]** Tightening a source may make previously visible articles/actions unavailable. → Return affected IDs/counts and let the Skill explain the privacy consequence before or after the mutation.
- **[Risk]** A source can be lowered while derived records remain stricter, creating classifications that no longer match. → Preserve monotonic derived values and document that lowering does not automatically relax downstream records.
- **[Risk]** Concurrent metadata updates can race. → Use a transaction, source snapshot predicate, and idempotency reservation; return `PLAN_STALE`-style `SOURCE_CHANGED` when the row changed before update.

## Migration Plan

No schema migration is required. Ship the additive operation and Skill resource; existing source rows retain their current sensitivity and become updateable immediately.

## Open Questions

None for v1. A later release may expose a separate reviewed plan for bulk reclassification across unrelated sources.
