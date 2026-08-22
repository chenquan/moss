## Context

The compiler uses SQLite for authoritative control state and managed Markdown/files for Wiki content. Batch apply therefore needs a database transaction plus an explicit recovery marker around filesystem renames. Extraction files are immutable managed artifacts referenced by SQLite. The external Skill remains responsible for model execution and confirmation.

## Design

### Batch lifecycle

Require `preview_ready` before selecting either legacy or batch preview. During apply, validate every article target, duplicate target, fact identity/version, provenance reference, source sensitivity, and managed path before the first final rename. Reserve new fact identities by checking `(kind, fact_key)` inside the apply transaction.

Create the recovery marker before staging/finalization. Once the first final rename begins, retain the marker and staged/recovery state on every failure. Remove them only after all renames and the SQLite transaction commit succeed; health/recovery handling remains the authority for interrupted finalization.

### Provenance and facts

Decode multi-extract output into a source-keyed map and require exactly one item for every Job source. Persist each item independently with its own content and hash. Reuse is allowed only when the managed path is within the extraction root, is a regular non-symlink file, and its hash matches the stored hash.

All batch source references must satisfy the maximum sensitivity rule. Fact extraction and supersession references are resolved during preview. Retraction appends a new fact version with `retracted` status while preserving prior versions.

### Storage and indexing

Treat `Paths.Extractions` as durable managed state in backup manifests and restore swaps. Centralize FTS replacement in the storage layer and call it from every article mutation transaction, including legacy apply and safety rollback/undo.

### Determinism and backfill

Sort normalized source IDs lexically after trimming/deduplication. Enforce an aggregate source byte budget before creating job files. Generate a unique backfill manifest ID for each new request while storing the exact protocol response for idempotent replay. Propagate all citation query, scan, and iteration errors.

### Compatibility

Legacy single-source compile behavior remains unchanged except that it now benefits from the same path, index, provenance integrity, and recovery safeguards. Existing persisted plans remain readable; missing optional review fields are represented as empty values.
