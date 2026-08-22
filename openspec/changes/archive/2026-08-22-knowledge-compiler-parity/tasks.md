## 1. Schema and protocol foundation

- [x] 1.1 Add schema v5 additive migrations for source lineage, source-set membership, extraction records, fact versions, batch plans, and FTS5 index state.
- [x] 1.2 Add `origin_key` validation/storage to source ingestion and expose lineage metadata without weakening sensitivity filtering.
- [x] 1.3 Extend protocol argument decoding, capability registration, dispatch, and bundled operation guidance for source sets, pipeline fingerprints, batch plans, and `knowledge.reindex`.
- [x] 1.4 Add bounded JSON Schemas for multi-source extraction metadata, fact operations, and multi-article write output.

## 2. Extraction and multi-source compilation

- [x] 2.1 Normalize legacy `source_id` and new `source_ids` into deterministic source-set compile jobs and managed input files.
- [x] 2.2 Persist accepted extraction artifacts and provenance fingerprints; reuse matching fresh extractions and mark mismatches stale.
- [x] 2.3 Extend stage validation to accept source-set references, extraction metadata, fact operations, article operations, and bounded result collections.
- [x] 2.4 Provide existing article/fact compile context to classify and write stages without exposing unauthorized sources.
- [x] 2.5 Preserve legacy single-source stage/result behavior through a compatibility adapter.

## 3. Facts, batch plans, and managed Wiki

- [x] 3.1 Implement versioned fact records with active, superseded, and retracted status plus extraction/source citations.
- [x] 3.2 Implement batch preview records containing article operations, fact operations, base versions/hashes, diffs, risks, and expiry.
- [x] 3.3 Apply confirmed batch plans with all-or-nothing validation, staged file writes, recovery markers, SQLite transactions, audit events, and idempotent replay.
- [x] 3.4 Integrate source revision and safe-forget planning with stale/retracted fact propagation while preserving historical evidence.
- [x] 3.5 Keep legacy article plans, article history, rollback, drift checks, and sensitivity propagation working.

## 4. Indexed retrieval and maintenance

- [x] 4.1 Add the SQLite FTS5 article index and deterministic index row/update helpers.
- [x] 4.2 Rework `knowledge.candidates` to use FTS5 ranking with stable tie-breaking, sensitivity/drift filtering, and CJK substring fallback.
- [x] 4.3 Add idempotent `knowledge.reindex`, index health checks, and legacy article index rebuild without model calls.
- [x] 4.4 Preserve catalogue/materialize/history response contracts and expose index/freshness metadata where useful.

## 5. Migration, Skill, and verification

- [x] 5.1 Add explicit legacy backfill job behavior and document that upgrades never invoke models automatically.
- [x] 5.2 Update bundled Skill workflows and protocol assets for multi-source compilation, batch confirmation, lineage, stale extraction, and reindex maintenance.
- [x] 5.3 Add unit and integration coverage for migration, idempotency, source lineage, freshness gates, multi-article atomicity, fact reconciliation, FTS5 ranking, drift, sensitivity, and recovery.
- [x] 5.4 Run formatting, `go test ./...`, strict OpenSpec validation, and final worktree/status checks.
