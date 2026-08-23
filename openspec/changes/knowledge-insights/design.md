## Context

Moss already stores source-backed fact versions, extraction freshness, article citations, action source references, sensitivity classifications, and managed-file hashes. The compile extraction schema can identify decisions, but the runtime currently exposes them only through generic fact and article retrieval. The first product slice should make decision review useful without inventing a second knowledge store.

The runtime is local and machine-oriented: the Skill owns natural-language interpretation, while Moss owns deterministic querying, privacy filtering, provenance, and protocol validation. The new capability must therefore be read-only, bounded, replay-independent, and safe when some derived projections or managed articles are unavailable.

## Goals / Non-Goals

**Goals:**

- Expose current `decision` facts and their version/freshness state through one bounded read-only operation.
- Surface auditable review signals for stale, superseded, retracted, or unavailable evidence.
- Link decisions to cited articles and actions through explicit shared source IDs, labeling the association as evidence-based rather than causal.
- Reuse existing sensitivity filtering and avoid returning unverified article content.
- Advertise and route the operation through the existing stdio protocol and bundled Skill.

**Non-Goals:**

- No new `decisions`, `relationships`, `conflicts`, or `action_results` tables.
- No mutation, confirmation plan, model invocation, automatic correction, or notification scheduler.
- No inferred `contradicts` edges or general-purpose knowledge graph.
- No changes to the semantics of existing catalog, candidate, materialize, history, or action operations.

## Decisions

### Use a separate read-only operation

Add `knowledge.insights` instead of overloading `knowledge.candidates` or `action.query`. Decision review combines facts, articles, sources, and actions and deserves an explicit bounded contract. Existing operations remain compatible and can still be used for drill-down.

### Derive from authoritative tables at query time

Read facts, fact versions, fact citations, sources, articles, article citations, extractions, and actions directly from SQLite. Do not add a cached insight table: the result is small, deterministic, and must reflect current freshness, forgotten-source state, and action revisions immediately.

### Treat evidence association as non-causal

An action is related to a decision only when the action source ID is among the decision's citation sources. The response labels this as `shared_source`; it never claims that the decision produced or caused the action.

### Keep content bounded and privacy-safe

Return fact text only when the fact's sensitivity can be established from its cited sources and the request permits it under existing `allow_sensitive` rules. Return article/action metadata and IDs needed for drill-down, but do not inline article bodies or action details. Omit unauthorized records and source locators rather than leaking partial metadata.

### Deterministic ranking

Rank review signals in this order: retracted, stale, superseded, then current. Within the same signal, sort by `fact_key`, `fact_id`, and version. Apply the requested limit to the decision records; review items correspond only to returned decisions.

## Risks / Trade-offs

- [No explicit conflict graph] → State clearly that v1 reports lifecycle/freshness signals only; defer `contradicts` until relationship persistence is designed.
- [Shared-source false association] → Expose an explicit association type and source IDs so the Skill can describe it as evidence overlap, not causality.
- [Sensitive data leakage through joins] → Filter facts, citations, articles, and actions independently using source sensitivity and `allow_sensitive`; never return unauthorized locators or content.
- [Stale managed article] → Return article metadata with a drift marker and no body; let `knowledge.materialize` enforce the existing hash check when the Skill drills down.
- [Large fan-out] → Bound topic/limit input, use batched SQL queries or bounded per-decision lookups, and cap arrays in the response.

### Review follow-up hardening

- Apply topic, forgotten-source, and sensitivity eligibility in the candidate SQL before the requested limit; perform evidence, history, and association lookups only for the limited candidate set.
- Verify an active extraction's managed path, regular-file status, symlink safety, and content hash before treating it as available evidence.
- Include explicit cross-fact `supersedes_fact_id` references in the review signals while preserving the successor fact and version identifiers.
- Filter sensitive article and action associations in SQL before applying their per-decision limits.

## Migration Plan

No database migration is required. Deploying the new binary adds a read-only operation and capability metadata. Rolling back the binary removes the operation while preserving all existing data because no tables or rows are added.

## Open Questions

None for v1. Explicit relationships, conflict detection, action results, and proactive notification remain separate future changes.
