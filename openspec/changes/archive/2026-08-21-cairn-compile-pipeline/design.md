## Context

The foundation change provides a short-lived Go process, SQLite metadata, immutable Raw sources, idempotency, and the initial Skill. It does not yet create knowledge. This change adds a resumable job protocol in which Claude reads managed input files and writes stage results, while Cairn validates every result and owns all Markdown persistence.

The workflow is intentionally single-source per job for v1. A job progresses through `extract`, `classify`, and `write`; each stage has a schema, input references, and a result file under the job directory. A submitted write result becomes a plan preview, not an immediate article mutation.

## Goals / Non-Goals

**Goals:**

- Implement resumable, idempotent compile jobs and stage transitions.
- Keep stage data in job-scoped files with CLI-owned schemas and path checks.
- Validate source citations and sensitivity before a result can progress.
- Generate deterministic Markdown article content and metadata.
- Provide one optimistic, auditable `plan.apply` gateway plus `plan.inspect` and `plan.undo`.
- Detect external Wiki drift and stale plans before modifying content.

**Non-Goals:**

- No model invocation, natural-language answer generation, PDF/OCR/Office extraction, multi-source jobs, full-text search, action reminders, forget semantics, or installer/upgrade workflow.
- No supported direct editing or automatic import of Markdown files.
- No background job worker; Claude drives each stage through the file protocol.

## Decisions

### Job state machine

Jobs use explicit states: `running`, `preview_ready`, `applied`, `aborted`, and `failed`. Stages use `pending`, `available`, `submitted`, and `rejected`. `compile.next` is read-only and returns the next available stage; `compile.submit` is the only operation that advances a job.

```text
running
  extract available → submitted
  classify available → submitted
  write available → submitted
        │
        ▼
preview_ready ── plan.apply ──> applied
        │
        └──────────────> compile.abort / failed
```

Alternatives considered: a free-form list of steps would make retries and invalid ordering ambiguous; an asynchronous worker would violate the local one-shot runtime boundary.

### Embedded JSON Schemas

The CLI embeds `extract`, `classify`, and `write` JSON Schema files with `go:embed`. `compile.next` copies the relevant schema into the Job directory for Claude to read; `compile.submit` validates both the schema and Cairn-specific invariants. The `santhosh-tekuri/jsonschema/v5` library provides draft validation, while Go checks enforce source IDs, stage order, sensitivity, and allowed paths.

### Stage payloads

- `extract` contains facts, decisions, preferences, projects, people, relationships, actions, conflicts, and citations.
- `classify` contains typed categories and an article outline derived from the extraction result.
- `write` contains one article candidate: optional article ID, title, slug, summary, body, sensitivity, tags, source IDs, and citations.

The write stage is deliberately one article per job. Later changes can add multi-article compilation without changing the stage envelope.

### Job and article persistence

SQLite adds `compile_jobs`, `compile_stages`, `articles`, `article_versions`, `article_citations`, `plans`, and `plan_items`. Job result files remain in `jobs/<job-id>/output`; the database stores their hashes and state. Article files live at `wiki/articles/<slug>.md`; frontmatter field order, quoting, source ordering, and newline policy are deterministic.

### Plan gateway and optimistic concurrency

`compile.preview` creates a pending plan containing the proposed article content, affected source IDs, target article ID, current article version, and risk flags. `plan.inspect` reads it. `plan.apply` checks plan status, expiry, current article version, and the article file hash before writing. It writes a staged Markdown file, updates SQLite article/version/plan state in a transaction, and atomically replaces the article. `plan.undo` restores the prior version or removes a newly created article into Cairn's managed trash.

The file/SQLite boundary is protected with a recovery marker. A plan cannot apply while `system.health` reports recovery-required or when the Wiki file hash differs from the last managed version.

### Idempotency

All state-changing compile and plan operations require idempotency keys. The foundation idempotency table stores the serialized response. Job and plan state transitions additionally enforce the current state in SQLite, so a retry cannot advance a stage twice or apply a plan twice.

### Skill orchestration

The Skill receives the user's request, calls `compile.start`, loops through `compile.next`/Claude file generation/`compile.submit`, then calls `compile.preview`. It shows the diff and risks in Claude before calling `plan.apply` when policy requires confirmation. Source text is data, not instructions; the Skill never forwards source content in shell arguments.

## Risks / Trade-offs

- **[Risk]** Claude writes malformed or instruction-injected results. → Validate JSON Schema, source citations, stage state, and path ownership; treat rejected results as data errors and never execute their contents.
- **[Risk]** A plan applies against an article changed since preview. → Store article version and file hash in the plan; return `PLAN_STALE` or `WIKI_DRIFT` before mutation.
- **[Risk]** A crash occurs between article replacement and SQLite commit. → Write a recovery marker containing plan ID and staged path; health blocks future mutations until reconciliation.
- **[Risk]** A user submits a huge result file. → Enforce per-stage byte limits before parsing and use bounded reads.
- **[Risk]** Article slugs collide or contain traversal. → Normalize slugs, reject path separators, and allocate a deterministic suffix on collision during preview.
- **[Risk]** Undo removes unrelated later edits. → Undo only applies when the current article version equals the version produced by the target plan; otherwise return `PLAN_STALE`.

## Migration Plan

The database migration adds new tables without changing foundation tables. Existing Raw sources remain valid. The first compile job can use any existing source. Existing Wiki directories are created if absent; no pre-existing Markdown is imported. The protocol capability list gains the new operations only after the migration is complete.

Rollback keeps existing Raw and source data, marks in-flight jobs and plans failed, and removes only newly generated article files if their hashes match the plan. Database schema rollback is not attempted; a later binary can continue to read the additive schema.

## Open Questions

No open product questions remain for this change. Multi-source jobs, richer article conflict merging, and manual Wiki import remain explicit future changes.
