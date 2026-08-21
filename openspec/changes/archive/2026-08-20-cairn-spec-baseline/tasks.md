## 1. Reconcile archived contracts

- [x] 1.1 Read the archived foundation, capture, compile, retrieval, and action delta specs and preserve their requirement/scenario text in this change.
- [x] 1.2 Add one baseline spec file for each missing capability named by the proposal.

## 2. Validate and publish the main catalog

- [x] 2.1 Run strict OpenSpec validation for the reconciliation change and all repository specs.
- [x] 2.2 Archive the change with spec syncing enabled and verify that every reconciled capability exists under `openspec/specs/`.
- [x] 2.3 Re-run the runtime test, race, vet, build, and protocol-operation audit after the catalog update.
