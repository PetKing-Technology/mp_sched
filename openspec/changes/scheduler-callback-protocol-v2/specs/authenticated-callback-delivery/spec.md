## ADDED Requirements

### Requirement: Callback protocol v2 authenticates each exact delivery
When callback v2 authentication is completely configured, mp_sched SHALL emit
a version-2 callback with a fresh delivery id, UTC RFC3339Nano occurrence time,
key id, SHA-256 digest of the exact transmitted body, and HMAC-SHA-256
signature over the documented length-delimited canonical sequence.  The body
SHALL contain the matching version, delivery id, occurrence time, event, task
id, status, and task snapshot.

#### Scenario: Terminal delivery has verifiable headers
- **WHEN** a worker sends a terminal callback with v2 authentication enabled
- **THEN** the receiver can verify the headers against the exact request body
  and the fixture key, and the body identity matches the signed metadata

### Requirement: Delivery metadata is unique and non-secret
mp_sched SHALL generate a new cryptographically random delivery id and UTC
occurrence time for each v2 send attempt.  It SHALL never put the HMAC secret
in headers, body, logs, or returned errors.

#### Scenario: Retry gets a distinct delivery id
- **WHEN** the client sends the same event and task twice with v2 enabled
- **THEN** both callbacks have independently valid signatures and distinct
  delivery ids without exposing the fixture secret

### Requirement: Incomplete v2 configuration fails closed to legacy mode
mp_sched SHALL use v2 only when its required key id and HMAC key are both
configured.  Otherwise it SHALL retain the legacy callback body and header
behavior and SHALL not add partial v2 headers.

#### Scenario: Existing callback deployment is not migrated
- **WHEN** callback authentication configuration is absent
- **THEN** the emitted request body and configured static headers are unchanged
  from legacy behavior

### Requirement: Reserved v2 headers cannot be overridden by static headers
mp_sched SHALL write v2 protocol headers after any configured static headers
and SHALL reject/ignore any static attempt to select conflicting v2 metadata.

#### Scenario: Static header conflicts with signed metadata
- **WHEN** static callback headers include an v2 reserved header name
- **THEN** the transmitted header value is the scheduler-generated signed value

