## Why

Cairn can now compile a reviewed source into managed Markdown, but Claude has no safe way to discover which articles are relevant to a question or to read one complete article with its provenance. Retrieval must stay local and deterministic so the Skill can select candidates while Cairn remains the authority for article paths, hashes, sensitivity, and citations.

## What Changes

- Add `knowledge.catalog` for a sensitivity-filtered, deterministic article catalogue and topic index metadata.
- Add `knowledge.candidates` for bounded local candidate search over managed article metadata and content, returning ranked summaries/snippets and source references without silently generating an answer.
- Add `knowledge.materialize` for reading the complete selected managed article and its citations after verifying the recorded Wiki hash.
- Add `knowledge.history` for reading article version metadata, hashes, timestamps, jobs, and citations, with bounded optional version content.
- Extend the Cairn Skill with a retrieve workflow: catalogue/candidates → Claude selection → materialize → answer with local article/source citations.
- Keep retrieval read-only; no second model, Web UI, MCP, direct Wiki edits, or automatic Markdown import.

## Capabilities

### New Capabilities

- `knowledge-retrieval`: Local catalogue, candidate search, complete article materialization, version history, sensitivity filtering, drift detection, and provenance.
- `retrieve-skill`: Claude Skill routing and prompt-injection boundaries for local knowledge questions.

### Modified Capabilities

<!-- No main specs exist yet; the foundation and compile delta specs are archived without syncing. -->

## Impact

- Extends the single-file JSON protocol and capability discovery with five read-only knowledge operations.
- Adds Go retrieval/search and managed-Markdown parsing over the existing SQLite article/version/citation tables.
- Extends the Skill fixture and workflow documentation.
- Adds tests for deterministic ordering, ranking, sensitivity denial, Wiki drift, bounded materialization, and provenance.
