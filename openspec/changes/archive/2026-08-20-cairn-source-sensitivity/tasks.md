## 1. Protocol and implementation

- [x] 1.1 Add `source.mark_sensitive` request validation, mutating classification, capability, and app dispatch.
- [x] 1.2 Implement transactional source update, lowering confirmation, monotonic article/action propagation, idempotency, and audit.
- [x] 1.3 Keep forgotten-source queries hidden and preserve stricter downstream sensitivity values.

## 2. Skill and tests

- [x] 2.1 Document sensitivity marking, confirmation, propagation, and privacy behavior in the Cairn Skill/resources.
- [x] 2.2 Add unit/integration tests for tightening, lowering gate, same-value retry, forgotten/invalid source, and downstream access.

## 3. Validation and handoff

- [x] 3.1 Run tests, race tests, vet, build, whitespace checks, strict OpenSpec validation, verify scenarios, and archive the change.
