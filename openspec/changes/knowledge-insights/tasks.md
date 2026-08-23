## 1. Protocol and query foundation

- [x] 1.1 Define `knowledge.insights` request/response types, bounded arguments, stable review-priority ordering, and sensitivity-aware result models in `internal/knowledge`.
- [x] 1.2 Implement deterministic SQLite-backed insight assembly from decision facts, fact versions, citations, sources, extractions, articles, article citations, and actions without adding tables or mutating state.
- [x] 1.3 Add topic filtering, limit validation, forgotten-source filtering, drift metadata, and bounded article/action association handling.
- [x] 1.4 Route `knowledge.insights` through `internal/app` and advertise it as a non-mutating capability in `internal/protocol`.

## 2. Skill and documentation integration

- [x] 2.1 Add the insight operation and routing rules to the bundled Skill protocol and operation guide.
- [x] 2.2 Update the retrieve workflow and Skill guidance for decision-review questions, including the non-causal `shared_source` interpretation and drill-down to existing materialize/history operations.
- [x] 2.3 Update README operation tables, usage descriptions, and product-boundary documentation for the new read-only capability.

## 3. Verification

- [x] 3.1 Add unit tests for deterministic ordering, topic/limit behavior, lifecycle review signals, and no contradiction inference.
- [x] 3.2 Add integration tests for article/action associations, sensitivity filtering, forgotten sources, drifted articles, capability discovery, and no-mutation behavior.
- [x] 3.3 Run `go test ./...`, `go vet ./...`, `git diff --check`, and `openspec validate --all --strict`; resolve all failures.

## 4. Review follow-up hardening

- [x] 4.1 Apply decision eligibility and review-priority limiting before per-fact lookups.
- [x] 4.2 Filter sensitive associations before article/action limits and include explicit cross-fact supersessions.
- [x] 4.3 Verify active extraction artifacts and add regression coverage for all four review findings.
- [x] 4.4 Re-run the full verification suite after review fixes.
