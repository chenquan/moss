## Context

Cairn's feature work was delivered as a sequence of independent OpenSpec changes. The archive contains the original delta specifications, while the main catalog contains only changes whose archive step performed a sync. This creates a documentation drift problem: a fresh reader of `openspec/specs/` cannot see the complete supported contract even though the Go implementation and Skill are already tested against it.

## Goals / Non-Goals

**Goals:**

- Reconcile the main specification catalog with the archived foundation, capture, compile, retrieval, and action requirements.
- Preserve the original requirement text and scenario boundaries so historical validation remains meaningful.
- Make the reconciliation reviewable as a normal OpenSpec change and leave no runtime code changes.

**Non-Goals:**

- Adding or changing operations, storage behavior, Skill routing, dependencies, or installation behavior.
- Replacing the archived change records or claiming that this documentation change implemented the features.

## Decisions

1. **Use the archived delta specs as the source material.** The archived artifacts are the reviewed contracts that governed the existing implementation; copying their requirements into this change avoids inventing a second, subtly different contract.
2. **Create missing main capabilities as ADDED specs.** None of the thirteen capability directories exists in `openspec/specs/`, so each reconciliation file will contain the complete requirement blocks under `## ADDED Requirements`; the archive operation will merge them into the main catalog.
3. **Keep history explicit.** The proposal and tasks identify this as a specification baseline only. The archived dates remain the implementation history, while the new archive records when the main catalog was reconciled.

## Risks / Trade-offs

- **[Risk]** A copied requirement could diverge from the implementation if the archived source was stale. → Validate all archived and main specs strictly, then rerun the complete Go test and protocol audit; do not introduce behavioral edits in this change.
- **[Risk]** Large baseline files make future edits harder to locate. → Keep one file per capability and retain stable requirement/scenario names.

## Migration Plan

1. Add the thirteen missing capability delta specs and mark the reconciliation task complete.
2. Run strict OpenSpec validation.
3. Archive the change with spec syncing enabled, which creates the main catalog files.
4. Re-run strict validation and the complete runtime test/build suite; no data migration or rollback is needed.

## Open Questions

None for this reconciliation. Any behavioral change must be proposed as a separate capability change.
