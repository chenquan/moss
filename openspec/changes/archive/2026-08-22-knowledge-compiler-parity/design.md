## Context

Moss v1 is a short-lived Go runtime with SQLite, managed Raw/Wiki files, idempotent operations, and a Skill-owned model boundary. Compile jobs are single-source and Skill-driven; stage output is stored under a job directory, while articles are versioned and applied through confirmation-gated plans. Retrieval scans managed articles deterministically.

The change adds KaaS-like knowledge compilation without moving model calls into Moss. A compile request remains a protocol operation, but a new job can contain a source set, reusable extraction records, versioned facts, and multiple article operations. Existing v1 requests and data remain readable.

## Goals / Non-Goals

**Goals:**

- Add backward-compatible multi-source compile jobs and one atomic multi-article plan.
- Persist extraction provenance and freshness fingerprints.
- Track source lineage with explicit `origin_key` and reconcile versioned facts.
- Maintain deterministic SQLite FTS5 retrieval and idempotent reindexing.
- Provide explicit legacy backfill without model work during database upgrade.
- Preserve sensitivity, drift, recovery, confirmation, and idempotency invariants.

**Non-Goals:**

- No model invocation, daemon, worker pool, cost accounting, Web UI, MCP server, vector database, or remote source fetcher.
- No automatic lineage inference from paths or filenames.
- No automatic LLM backfill during startup or migration.
- No direct manual Wiki import or direct Skill writes to authoritative state.

## Decisions

### Compatibility adapter

`compile.start` accepts either legacy `source_id` or new `source_ids`; the former is normalized to a one-item source set. Existing single-source stage payloads and plans remain readable. New jobs use source-set metadata and batch plan tables, while `compile.apply` dispatches by plan kind.

### Extraction and freshness

Each accepted extraction is an immutable managed JSON artifact plus an SQLite record containing source hash, extractor version, prompt hash, schema version, strategy, and result hash. A fingerprint is the hash of those inputs. Matching fingerprints are reused; mismatches create stale records and require an explicit compile job.

### Facts and lineage

`source.ingest` accepts an optional opaque `origin_key`. Moss assigns monotonically increasing revisions only inside an origin key; absent keys never participate in propagation. Fact versions retain status (`active`, `superseded`, `retracted`), extraction ID, source citations, and optional superseded fact ID. Source revision marks dependent records stale but does not rewrite Wiki content without a reviewed plan.

### Batch output and application

New write results contain article operations and fact operations. `compile.preview` creates a batch plan with one header and ordered article/fact items. Confirmation applies the complete plan. Every current article hash/version and fact version is checked before any filesystem or SQLite mutation. Files are staged with a recovery manifest; partial filesystem completion blocks future mutations until health reconciliation.

### Search index

SQLite FTS5 is the primary article index because the bundled `modernc.org/sqlite` build enables FTS5 and it keeps search local and deterministic. Index rows are updated in the same transaction as article application and can be rebuilt by an idempotent `knowledge.reindex` operation. Existing substring scoring remains a fallback for CJK and malformed FTS queries.

### Migration

Schema version 5 is additive. Existing v4 tables and files remain valid. Existing articles are indexed immediately without model work and marked with legacy provenance until an explicit backfill job creates extraction records. Backfill uses the same preview/apply and recovery rules as normal compilation.

## Risks / Trade-offs

- **[Risk]** Multiple file renames cannot be rolled back by SQLite alone. → Use a recovery manifest, verify every staged hash, and block mutation on incomplete reconciliation.
- **[Risk]** External Skill output can claim unstable fact identities. → Treat keys as opaque data, require source citations, validate all referenced IDs, and preserve every version.
- **[Risk]** FTS5 tokenization is weak for some Chinese queries. → Keep deterministic Unicode substring fallback and test both paths.
- **[Risk]** Large source sets produce oversized stage files. → Bound source count, stage bytes, item counts, and response sizes; reject before persistence.
- **[Risk]** Old v1 jobs have no extraction metadata. → Keep legacy execution and explicitly mark missing provenance instead of guessing.

## Migration Plan

1. Add schema v5 tables/columns and FTS5 virtual tables; health validates the new structures.
2. Rebuild the article index from existing managed articles without invoking a model.
3. Keep legacy single-source compile and plan records readable through the compatibility adapter.
4. Expose new capabilities only after the runtime and bundled Skill versions match.
5. Run explicit backfill jobs for selected source sets; review and apply resulting batch plans.
6. Rollback by retaining v4-compatible records and marking new in-flight jobs/planes failed; do not remove existing Raw or Wiki data.

## Open Questions

None. This design follows the approved choices: phased full scope, external Skill model execution, extraction as truth layer, SQLite FTS5, explicit backfill, backward-compatible protocol, multi-article atomic plans, versioned fact retractions, one batch confirmation, and explicit `origin_key` lineage.
