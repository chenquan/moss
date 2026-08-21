## Why

The product operation contract explicitly includes `compile.apply`, but the first compile implementation exposed only the generic `plan.apply` gateway. The behavior is available internally, yet the advertised protocol and Skill route do not match the agreed operation set, which makes capability negotiation and workflow recovery ambiguous.

## What Changes

- Add machine-only `compile.apply` as the canonical gateway for applying a reviewed compile knowledge plan.
- Keep generic `plan.apply` for safety plans such as forget and rollback, with the existing idempotency, confirmation, drift, expiry, and audit checks preserved.
- Advertise `compile.apply` in capabilities and classify it as mutating.
- Update the Cairn Skill and compile workflow to call `compile.apply` after explicit confirmation.
- Add protocol and integration coverage for the new operation and its retry behavior.

## Capabilities

### New Capabilities

<!-- No new capability area; this is an operation-level completion of the compile contract. -->

### Modified Capabilities

- `cli-protocol`: advertise and classify `compile.apply` in the machine operation set.
- `compile-jobs`: expose the compile-plan application gateway and preserve its safety contract.
- `compile-skill`: route reviewed compilation changes through `compile.apply`.
- `change-plans`: distinguish the compile-specific `compile.apply` gateway from generic safety-plan application.

## Impact

- Changes `internal/protocol`, `internal/app`, and the compile/Skill documentation and tests.
- No database migration, new dependency, human CLI command, Web UI, MCP integration, or second model.
- Existing `plan.apply` callers remain supported for safety plans and older compile requests.
