## MODIFIED Requirements

### Requirement: Advertise supported operations
The capability response SHALL advertise the backward-compatible multi-source compile fields, source lineage support, indexed retrieval, and `knowledge.reindex` while retaining the v1 operation names and mutation classifications.

#### Scenario: New capability handshake
- **WHEN** a matching Skill performs a handshake
- **THEN** the response lists the new supported capability metadata and still accepts legacy compile requests
