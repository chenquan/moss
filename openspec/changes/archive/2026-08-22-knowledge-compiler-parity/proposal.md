## Why

Moss currently provides a safe single-source, Skill-driven knowledge runtime, but it cannot compile a coherent knowledge base from multiple sources, preserve reusable extraction state, reconcile revised facts, or search efficiently as the corpus grows. This change brings the core KaaS knowledge-compiler capabilities into Moss while preserving its deterministic Go runtime and explicit confirmation boundaries.

## What Changes

- Extend compile jobs from one source and one article to backward-compatible multi-source jobs and atomic multi-article plans.
- Persist extraction results with source and pipeline freshness metadata.
- Add explicit source lineage, versioned facts, supersession, and retraction operations.
- Add SQLite FTS5-backed deterministic retrieval and reindex maintenance.
- Provide explicit legacy backfill jobs without invoking models during upgrade.
- Keep LLM invocation in the external Skill; do not add a daemon, Web UI, MCP server, or vector database.

## Capabilities

### New Capabilities

- `multi-source-compilation`: Compile source sets into extraction records, fact operations, and multiple article operations.
- `extraction-provenance`: Persist reusable extraction versions and freshness fingerprints.
- `fact-reconciliation`: Version, supersede, retract, and propagate source-backed facts.
- `knowledge-indexing`: Maintain SQLite FTS5 indexes and deterministic reindexing.

### Modified Capabilities

- `compile-jobs`: Add source sets, multi-stage payloads, batch plans, and legacy compatibility.
- `knowledge-retrieval`: Use persistent indexed search while preserving bounded, sensitivity-aware responses.
- `source-capture`: Add optional explicit `origin_key` lineage.
- `managed-wiki`: Apply multiple article projections atomically with fact provenance.
- `local-storage`: Add additive schema migration, extraction/fact/index tables, and recovery metadata.
- `cli-protocol`: Register new fields and maintenance operations without breaking v1 requests.

## Impact

- Affects `internal/compile`, `internal/knowledge`, `internal/source`, `internal/storage`, `internal/protocol`, `internal/app`, and bundled Skill workflows.
- Adds SQLite schema migrations and FTS5 virtual tables; existing v4 data remains readable and is explicitly backfilled.
- Requires new JSON Schemas for extraction metadata, multi-article write results, fact operations, and batch plans.
- Requires integration tests for migration, idempotency, stale plans, recovery, FTS5 search, source lineage, and legacy single-source behavior.
