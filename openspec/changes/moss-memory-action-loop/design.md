## Context

Moss has versioned `facts`, managed article citations, source sensitivity, deterministic `knowledge.insights`, and confirmed action plans. The current implementation deliberately avoids a relationship table, action-result table, background scheduler, and general knowledge graph. The requested P0/P1 completion must add those missing local capabilities while preserving v1's Skill-only user interface and plan/apply confirmation boundary.

## Goals / Non-Goals

**Goals:**

- Store explicit relationships with enough typed identity and version information to detect stale references.
- Apply compile-created article, fact, and relationship changes atomically and support safe undo.
- Record action outcomes as durable, confirmed records and expose their provenance.
- Provide deterministic review scanning and bounded context assembly for the Skill.
- Keep all reads local, privacy-filtered, hash-aware, and free of model or network calls.

**Non-Goals:**

- No inferred `contradicts` edges or automatic relationship extraction from historical artifacts.
- No automatic fact/article mutation from an action result.
- No background daemon, notification delivery, Web UI, MCP server, cloud sync, or remote connector.
- No general-purpose graph traversal or unbounded answer context.

## Decisions

### Additive schema version 6

Add `relations`, `compile_batch_relations`, `action_results`, and `action_result_plans` tables. Add nullable `review_after` to current facts and accepted fact write payloads. Existing rows default to no review deadline, and all migrations remain additive and idempotent.

### Use typed polymorphic relation endpoints

Each relation stores `from_type`, `from_id`, optional `from_version`, `to_type`, `to_id`, optional `to_version`, optional provenance `source_id`, and timestamps. Endpoint types are `source`, `fact`, `article`, `action`, and `action_result`. Relation type is restricted to the six named predicates. Polymorphic endpoints avoid a separate table per entity while validation resolves every endpoint before applying a plan.

The version field means fact version, article version, action revision, or action-result version. Sources have no version. A supplied version must match the current live endpoint; historical versions remain readable only through the existing history APIs.

### Compile relations are part of the reviewed batch

The multi-write result gains a bounded `relations` collection. Relation entries are staged in the compile batch plan, validated against the job source set and current entity versions, and inserted in the same transaction as facts/articles. Compile undo removes only relations created by that unchanged batch and refuses on drift.

### Action results have their own confirmation plan

`action.result.plan` and `action.result.apply` use a dedicated result-plan table because the existing action plan schema models action snapshots and optimistic action revisions. A result plan reserves the next per-action result version; applying it inserts that result, marks its result plan applied, and creates the `action -> produces -> action_result` relation atomically. It never changes the action status and never writes facts/articles.

### Review scan is explicit and read-only

`knowledge.review.scan` derives signals at query time. It accepts an optional `as_of` timestamp, topic, result limit, and missing-result age threshold. It reports explicit contradiction edges, stale/superseded/retracted facts, due `review_after` facts, unavailable evidence, article drift, and actions without results. It never creates a plan or persists a notification.

### Context bundle is metadata-first

`knowledge.context.bundle` composes bounded results from the existing article, fact, relation, action, result, citation, and review queries. It returns article path/version/hash references rather than article bodies by default. A future explicit inline mode may be added only as a compatible option; this change keeps the response metadata-only so `knowledge.materialize` remains the verified content gateway.

### Privacy and forget behavior

Every relation/result association is filtered by active endpoint state and source sensitivity. Forget planning and application include dependent relations/results in their impact closure and mark or hide them consistently with existing forgotten sources/actions. No unauthorized locator, title, result text, or relationship endpoint is returned.

## Risks / Trade-offs

- [Polymorphic references can become stale] → Validate endpoint type, identity, current version/revision, and forgotten state during preview and apply.
- [Relationship fan-out can leak sensitive data] → Resolve sensitivity per endpoint and provenance before applying limits; omit unauthorized rows entirely.
- [Action result may be mistaken for action completion] → Keep result status and action lifecycle separate; document that `action.update.plan` is still required to mark an action done.
- [Review scan may be expensive] → Bound every input and output, limit candidate rows before fan-out, and use deterministic indexes.
- [Existing forgotten-source semantics may leave graph residue] → Extend forget impact planning and active queries to include relation/result dependencies, with regression coverage.

## Migration Plan

1. Ship the additive schema migration and protocol metadata; existing v1 requests continue to work.
2. Deploy the new compile/action/review/context code and bundled Skill together so capability discovery and routing stay aligned.
3. Do not backfill relationships or action results automatically. Existing decision facts remain valid and can be linked by a future explicit compile result.
4. Rollback is binary-compatible for existing data: older binaries ignore the new tables, while newly created relationship/result data remains preserved for a later compatible binary.

## Open Questions

None. The relation set, explicit scan trigger, metadata-first bundle, action-result confirmation, and compile-batch/result-plan write paths are fixed by the approved implementation plan.
