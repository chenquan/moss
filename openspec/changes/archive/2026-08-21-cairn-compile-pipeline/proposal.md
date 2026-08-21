## Why

Cairn can currently preserve and list raw sources, but it cannot turn a source into reviewed knowledge. The next capability must give Claude a resumable, stage-driven compilation job while keeping schema validation, source citations, Markdown writes, conflicts, and application decisions deterministic inside Cairn.

## What Changes

- Add `compile.start`, `compile.next`, `compile.submit`, `compile.status`, `compile.preview`, and `compile.abort` operations.
- Add extract, classify, and write stage contracts with job-scoped input, schema, and result files.
- Validate every submitted Claude artifact against an embedded JSON Schema, source references, job state, file boundaries, sensitivity policy, and duplicate submission rules.
- Add managed Markdown article versions, deterministic frontmatter, source citations, and article metadata/index records.
- Add `plan.inspect`, `plan.apply`, and `plan.undo` as the single application gateway for reversible knowledge changes.
- Ensure conflicts, overwrites, sensitive content, stale plans, and external Wiki drift cannot be applied silently.
- **BREAKING**: Knowledge changes are not written directly by Claude or by compile stages; they are represented as Cairn plans and applied only through `plan.apply`.

## Capabilities

### New Capabilities

- `compile-jobs`: Resumable extract/classify/write jobs, stage files, typed submissions, validation, preview, status, and abort behavior.
- `managed-wiki`: Versioned Markdown articles, deterministic metadata, source citations, drift detection, and atomic article writes.
- `change-plans`: Plan creation, impact inspection, optimistic-version checks, idempotent application, undo, and recovery state.
- `compile-skill`: Claude Skill routing for the source-to-knowledge workflow, stage progression, user confirmation, and prompt-injection boundaries.

### Modified Capabilities

<!-- No main specs exist yet; the foundation change was archived without syncing delta specs. -->

## Impact

- Extends the Go protocol operation registry and SQLite schema with jobs, stages, articles, article versions, citations, plans, and plan items.
- Adds JSON Schema files and validation support for Claude-produced stage results.
- Adds managed Wiki files under the Cairn data root and a deterministic diff/preview representation.
- Extends the Cairn Skill with compile workflow instructions and confirmation policy.
- Adds integration tests for staged compilation, invalid submissions, conflicts, stale plans, drift, apply/undo, and crash-safe retries.
