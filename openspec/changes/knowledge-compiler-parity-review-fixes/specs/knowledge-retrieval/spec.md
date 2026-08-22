## MODIFIED Requirements

### Requirement: Make backfill planning complete and replay-safe
Backfill planning SHALL propagate citation lookup errors, use a distinct manifest ID for each new request, verify reusable extraction artifacts, and replay the original protocol response shape for an idempotent retry.

#### Scenario: Idempotent retry
- **WHEN** the same backfill request is retried with the same idempotency key
- **THEN** Moss returns the original response shape and manifest data

#### Scenario: Citation lookup failure
- **WHEN** citation enumeration fails during planning
- **THEN** planning fails instead of persisting an incomplete manifest
