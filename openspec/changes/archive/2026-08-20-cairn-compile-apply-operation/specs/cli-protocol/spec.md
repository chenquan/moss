## ADDED Requirements

### Requirement: Advertise the complete compile operation set
The CLI capability response SHALL include `compile.start`, `compile.next`, `compile.submit`, `compile.status`, `compile.preview`, `compile.apply`, and `compile.abort`, and SHALL classify `compile.apply` as mutating.

#### Scenario: Skill discovers compile apply
- **WHEN** a compatible Skill calls `system.capabilities`
- **THEN** the response contains `compile.apply` with `mutating: true` and a stable description

#### Scenario: Compile apply requires idempotency
- **WHEN** a caller submits `compile.apply` without an idempotency key
- **THEN** the CLI returns `IDEMPOTENCY_REQUIRED` before dispatching the operation
