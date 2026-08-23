## Why

Moss now stores source-backed facts, version history, managed articles, decision review signals, and an action ledger, but it still lacks explicit relationships, action outcomes, proactive review, and a bounded evidence context for composing answers. This change completes the local P0/P1 memory loop without expanding Moss into a daemon, Web UI, cloud service, or second answer model.

## What Changes

- Add six explicit, source-aware relationship types: `supports`, `contradicts`, `supersedes`, `depends_on`, `produces`, and `resulted_in`.
- Extend reviewed compile batches so article, fact, and relationship writes apply atomically and can be undone safely.
- Add confirmed action-result plans and durable action-result records without automatically completing actions or changing facts.
- Add read-only `knowledge.review.scan` for deterministic stale, conflict, expiration, drift, evidence, and missing-result review signals.
- Add read-only `knowledge.context.bundle` that returns bounded facts, decisions, relations, actions, results, citations, and managed article references.
- Preserve existing sensitivity, idempotency, recovery, confirmation, and non-causal `shared_source` semantics.
- Update the current stdio protocol, bundled Skill, README, tests, and OpenSpec documentation.

## Capabilities

### New Capabilities

- `knowledge-relations`: Persist and validate explicit typed relationships across managed Moss entities.
- `action-results`: Record confirmed outcomes for actions and link them to the relationship graph.
- `knowledge-review-scan`: Return bounded, deterministic memory-maintenance review signals without mutation.
- `knowledge-context-bundle`: Return a privacy-filtered, evidence-oriented retrieval bundle without generating answers.

### Modified Capabilities

None. Existing operations remain backward compatible; compile and action behavior is extended through the new capabilities.

## Impact

The change affects SQLite migrations and storage models, compile batch schemas and apply/undo paths, action planning, knowledge retrieval, protocol dispatch/capability metadata, bundled Skill workflows, README, OpenSpec specs, and integration tests. It adds no external dependency and keeps the current local stdio runtime boundary.
