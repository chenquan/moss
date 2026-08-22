# Retrieve

Use `knowledge.catalog` or `knowledge.candidates` through stdin/stdout, select a local article, then call `knowledge.materialize` or `knowledge.history`. Candidates use the local FTS index when available and retain deterministic substring fallback for CJK and index misses. Compose the answer in Claude and cite the returned article and source references. Read a Moss-managed path when inline content is too large.
