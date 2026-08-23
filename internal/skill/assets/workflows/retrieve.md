# Retrieve

For decision-review questions, call `knowledge.insights` first with an optional topic and bounded limit to obtain current decision facts, lifecycle review signals, citations, and explainable article/action associations. Review `stale`, `superseded`, `retracted`, and `evidence_unavailable` before composing the answer. Preserve successor fact/version IDs when a cross-fact supersession is reported; treat `shared_source` as evidence overlap rather than causality. Use `knowledge.materialize` or `knowledge.history` for selected articles, and never quote or rely on drifted or unavailable evidence.

For an explicit maintenance pass, call `knowledge.review.scan` with a bounded topic, `as_of`, and missing-result threshold. It is read-only and may report stored `contradicts` relations, stale/retracted facts, due reviews, article drift, actions without results, and action results without `resulted_in` feedback. It never infers a contradiction or creates a follow-up action.

For answer composition, prefer `knowledge.context.bundle` when a bounded set of facts, managed article references, explicit relations, action results, citations, and review signals is needed. The bundle never inlines article bodies by default; drill into a permitted article with `knowledge.materialize` and use its verified hash/path.

For general knowledge questions, use `knowledge.catalog` or `knowledge.candidates` through stdin/stdout, select a local article, then call `knowledge.materialize` or `knowledge.history`. Candidates use the local FTS index when available and retain deterministic substring fallback for CJK and index misses. Compose the answer in Claude and cite the returned article and source references. Read a Moss-managed path when inline content is too large.
