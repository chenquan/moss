## ADDED Requirements

### Requirement: Keep article search projection current
Every article mutation path SHALL update or replace its FTS projection in the same SQLite transaction as the article metadata/version mutation.

#### Scenario: Legacy article update
- **WHEN** a legacy plan or safety rollback changes an article
- **THEN** FTS returns the new current text and version
