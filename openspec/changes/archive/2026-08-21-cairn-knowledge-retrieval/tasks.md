## 1. Protocol and retrieval model

- [x] 1.1 Add typed knowledge request/response models, strict argument decoding, read-only capability entries, and app dispatch for catalog, candidates, materialize, and history.
- [x] 1.2 Add bounded limits, sensitivity option handling, stable error codes, and deterministic response ordering helpers.

## 2. Managed Markdown parsing and integrity

- [x] 2.1 Implement a bounded parser for Cairn's deterministic article frontmatter, tags, source IDs, summary, and body snippet.
- [x] 2.2 Implement managed article lookup, Wiki path/symlink checks, current-hash verification, and drift metadata without importing manual edits.
- [x] 2.3 Implement citation loading for current and historical article versions with sensitivity filtering.

## 3. Knowledge operations

- [x] 3.1 Implement `knowledge.catalog` with stable article and topic index output and normal/sensitive filtering.
- [x] 3.2 Implement deterministic local query tokenization, scoring, snippets, topic filtering, result limits, and drift flags for `knowledge.candidates`.
- [x] 3.3 Implement `knowledge.materialize` for verified complete current article content and provenance.
- [x] 3.4 Implement `knowledge.history` with newest-first version metadata, citations, optional bounded stored content, and permission checks.

## 4. Skill and tests

- [x] 4.1 Extend the Cairn Skill with catalogue/candidate selection, materialization, citation answer rules, resume behavior, and retrieval safety boundaries.
- [x] 4.2 Add parser and scoring tests for deterministic ordering, snippets, malformed metadata, limits, and drift.
- [x] 4.3 Add app integration tests for catalogue, candidates, materialization, history, sensitive denial, and source/article provenance.

## 5. Validation and handoff

- [x] 5.1 Run unit/integration tests, race tests, vet, build, strict OpenSpec validation, and whitespace checks.
- [x] 5.2 Review every scenario in both specs, complete verification, and archive the change only after all tasks pass.
