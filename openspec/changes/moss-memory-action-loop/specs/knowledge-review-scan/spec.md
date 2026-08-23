## ADDED Requirements

### Requirement: Return a bounded deterministic review scan

Moss SHALL implement read-only `knowledge.review.scan` with bounded topic, limit, `as_of`, missing-result threshold, and sensitivity arguments, returning stable review items without mutating any store.

#### Scenario: Scan current review signals

- **WHEN** the Skill requests a scan for a permitted topic
- **THEN** Moss returns deterministically ordered review items for the bounded candidate set and leaves SQLite, managed files, plans, and audit state unchanged

#### Scenario: Empty scan

- **WHEN** no permitted item requires review
- **THEN** Moss returns an empty result with stable count and no fabricated action or notification

### Requirement: Detect lifecycle, evidence, and explicit conflict signals

The scan SHALL report stale, superseded, retracted, due-for-review, unavailable-evidence, article-drift, and explicitly stored `contradicts` signals, without inferring conflicts from text overlap or shared sources.

#### Scenario: Explicit contradiction

- **WHEN** two permitted decision facts have an explicit `contradicts` relation
- **THEN** the scan returns that relation as a conflict review item with both endpoint identities

#### Scenario: Unavailable evidence

- **WHEN** a decision's cited source or active extraction cannot be verified
- **THEN** the scan reports `evidence_unavailable` and does not strengthen the decision's evidence status

### Requirement: Detect action follow-up gaps

The scan SHALL report permitted actions that exceed the requested missing-result age threshold without an action result, and results that have not yet been linked to a fact/article through an explicit `resulted_in` relation.

#### Scenario: Action without result

- **WHEN** an open or completed permitted action is older than the threshold and has no result
- **THEN** the scan returns a bounded missing-result review item without changing the action

#### Scenario: Result without knowledge feedback

- **WHEN** an action result has no permitted outgoing `resulted_in` relation
- **THEN** the scan reports a feedback-gap item without creating a relation or fact

### Requirement: Filter sensitive and forgotten records

The scan SHALL omit unauthorized sources, facts, articles, actions, results, relations, and locators rather than returning partial identifying metadata.

#### Scenario: Sensitive review item

- **WHEN** a review item depends on sensitive or restricted data and permission is absent
- **THEN** Moss omits the item and returns no sensitive endpoint, title, text, or locator
