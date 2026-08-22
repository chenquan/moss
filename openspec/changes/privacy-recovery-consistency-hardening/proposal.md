# Privacy, Recovery, and Projection Consistency Hardening

Moss has passed its normal-path end-to-end flow, but audit testing found privacy leaks in historical versions, incomplete FTS restoration, and failure windows between managed files and SQLite. This change hardens those boundaries without changing the managed-Wiki ownership model.

The change also validates provenance references during backup/restore, rejects unauthorized sensitive compile context, and turns unresolved filesystem/SQLite divergence into an explicit, recoverable state.

