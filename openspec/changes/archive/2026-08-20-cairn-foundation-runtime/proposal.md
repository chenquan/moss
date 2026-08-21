## Why

Cairn is a new local personal knowledge assistant whose only user interface is a Claude Code Skill. The workspace currently has no runtime, storage contract, or Skill implementation, so the first change must establish a reliable machine-only foundation before compilation, retrieval, actions, and forgetting can be built on top of it.

## What Changes

- Add the file-based `cairn-cli call --request ... --response ...` protocol with version negotiation, stable errors, idempotency, atomic responses, and no business data on stdout.
- Add a macOS user-scoped Cairn data layout with SQLite metadata, content-addressed Raw storage, managed temporary response files, and audit records.
- Add deterministic `system.handshake`, `system.health`, and `system.capabilities` operations.
- Add idempotent `source.ingest`, `source.get`, and `source.list` operations for preserving local source files without modifying the originals.
- Add the initial `cairn` Claude Code Skill routing layer that invokes only the machine protocol and treats Wiki files as Cairn-managed output.
- Establish the implementation seams required by later compile, knowledge, action, forget, backup, and upgrade changes without implementing those workflows yet.
- **BREAKING**: There is no human-facing CLI command set, interactive CLI mode, Web UI, MCP server, or stdout business output in this product boundary.

## Capabilities

### New Capabilities

- `cli-protocol`: File-based request/response execution, protocol compatibility, idempotency, stable errors, and process exit semantics.
- `local-storage`: Cairn's user-scoped directories, SQLite metadata, Raw content-addressed files, response artifacts, permissions, and audit foundation.
- `source-capture`: Immutable source ingestion, content hashing and deduplication, source metadata retrieval, and source listing.
- `system-health`: Handshake, health checks, capability discovery, storage validation, and deterministic diagnostics.
- `cairn-skill`: The minimal Claude Code Skill that routes natural-language capture and maintenance requests to `cairn-cli` without exposing a human CLI.

### Modified Capabilities

<!-- No existing capabilities or requirements exist in this greenfield workspace. -->

## Impact

- Adds a Go module and command binaries for `cairn-cli` plus internal protocol, storage, source, and audit packages.
- Adds the versioned Skill resources under the repository's Claude configuration for later installation into `~/.claude/skills/cairn/`.
- Adds SQLite and filesystem integration tests, protocol fixtures, and local test data under temporary directories.
- Establishes public machine contracts that future OpenSpec changes must preserve; no external service or model dependency is introduced.
