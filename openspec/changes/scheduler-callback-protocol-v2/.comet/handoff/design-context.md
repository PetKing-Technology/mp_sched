# Comet Design Handoff

- Change: scheduler-callback-protocol-v2
- Phase: design
- Mode: compact
- Context hash: 8ca4b4efa09ee6d672e7f1768bbe85086e733e53cd326300cb2ba50550e191fd

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## openspec/changes/scheduler-callback-protocol-v2/proposal.md

- Source: openspec/changes/scheduler-callback-protocol-v2/proposal.md
- Lines: 1-42
- SHA256: 5c4488bcbcedb55e484cd0d4d7b75eff52fa76855775ad438f612894d39ee2c7

```md
## Why

mp_sched currently sends an unsigned, replayable callback containing only a
task snapshot and status.  A shared Runtime callback bridge cannot safely use
that message to complete a protected, fenced external run: a static header is
source identity, not a signed delivery.

## What Changes

- Add an opt-in callback protocol v2 which signs each callback delivery with a
  configured HMAC key, a key id, unique delivery id, occurrence timestamp, and
  digest of the exact transmitted body.
- Add an explicit controlled artifact-binding mode.  For the fixed
  `sequence_bundle/v1` contract, the scheduler computes and signs the
  manifest digest from a configured server-owned output root and an opaque
  run id held in submitted business data.
- Preserve legacy callback body and header behavior exactly when protocol v2
  is not configured; no existing callback consumer is migrated implicitly.
- Document the canonical signing format, replay/clock semantics, configuration
  boundary, and no-preemption limits.

## Capabilities

### New Capabilities

- `authenticated-callback-delivery`: Versioned, HMAC-authenticated callback
  deliveries with canonical signed metadata and explicit failure behavior.
- `callback-artifact-binding`: A terminal callback contract that signs the
  exact sequence-bundle manifest without allowing a callback to select a
  filesystem path or Runtime namespace.

### Modified Capabilities

None.

## Impact

Affected areas: `internal/callback`, callback configuration and documentation,
Go callback tests, and the public scheduler callback contract.  No Runtime
service, shared callback URL/port, deployed scheduler configuration, secret,
GPU job, legacy/D4 stack, or provider execution behavior is changed by this
repository change.
```

## openspec/changes/scheduler-callback-protocol-v2/design.md

- Source: openspec/changes/scheduler-callback-protocol-v2/design.md
- Lines: 1-150
- SHA256: d3f863377fcf70ee742ca1be2aa47ce50c7f899962031dfba452cccaa86f2fa8

[TRUNCATED]

```md
## Context

The existing callback client serializes a task snapshot and static headers.
It has neither an authenticated delivery identity nor a result-artifact
carrier.  Runtime V2 needs a shared callback endpoint to distinguish a
protected controlled ZymCTRL completion from an ordinary scheduler callback
without trusting callback-provided tenant, task, or namespace fields.

This change is scheduler-side only.  Its artifact source is a configured,
server-owned root; the external payload never supplies an absolute path.

## Goals / Non-Goals

**Goals:**

- Emit an opt-in, versioned v2 envelope and headers for every configured
  callback delivery, with HMAC-SHA-256 authentication over exact body bytes.
- Make each delivery uniquely identifiable and time-stamped at send time.
- On a successful controlled ZymCTRL terminal delivery, compute and sign the
  canonical digest for exactly `<artifact_root>/<run_id>/output/sequence_bundle.json`.
- Remain byte-for-byte compatible with the legacy callback when v2 is absent.

**Non-Goals:**

- Changing a deployed configuration, storing or exposing a signing secret,
  changing callback URL/port, submitting jobs, or adding a GPU/provider.
- Selecting Runtime tenant, Mongo database, Redis prefix, generation, fence,
  or wake route in scheduler payloads.
- Claiming that the current runtime timeout/restart/orphan mechanisms are
  no-preemption safe.  That is a later deployment-policy gate.

## Decisions

### 1. HMAC covers exact transmitted bytes plus delivery metadata

When `callback.auth` has a non-empty key id and HMAC secret, the client emits
the legacy body plus v2 fields `protocol_version`, `delivery_id`,
`occurred_at`, `artifact_binding`, and (when applicable)
`artifact_manifest_digest`.  It additionally sets:

- `X-MP-Sched-Callback-Version: 2`
- `X-MP-Sched-Key-Id`
- `X-MP-Sched-Delivery-Id`
- `X-MP-Sched-Occurred-At`
- `X-MP-Sched-Content-SHA256`
- `X-MP-Sched-Signature`

`X-MP-Sched-Signature` is the lower-case hex HMAC-SHA-256 of the UTF-8,
length-delimited canonical sequence:

```
mp_sched_callback_v2\n
<key-id-length>:<key-id>\n
<delivery-id-length>:<delivery-id>\n
<occurred-at-length>:<RFC3339Nano UTC occurred-at>\n
<body-sha256-length>:<lowercase hex SHA-256(body)>\n
```

The receiver verifies headers and the raw-body digest before trusting decoded
fields.  Signing a body digest avoids JSON re-serialization ambiguity; the
length prefixes prevent field-boundary ambiguity.  A per-run capability was
rejected because the present scheduler does not echo it and it would duplicate
durable Runtime authority.

### 2. New delivery identity and timestamp on every send

The client generates a cryptographically random UUID delivery id and obtains a
UTC RFC3339Nano timestamp immediately before serialization.  These are never
persisted by scheduler.  Runtime is responsible for durable replay receipts
and its configured clock-skew policy.  Scheduler retry attempts intentionally
get new delivery ids; Runtime idempotency is by protected job/current attempt
plus delivery receipt, not by assuming network retries reused an id.

### 3. Artifact digest is scheduler-computed only for the fixed contract

The configuration provides an optional `callback.auth.artifact_root`; it is a
server-owned root, not a callback field.  A controlled task identifies only an
opaque `run_id` matching `[A-Za-z0-9][A-Za-z0-9_-]{0,127}` and contract
`sequence_bundle/v1` in its opaque `Business`.
The client validates `run_id` as a safe opaque identifier and computes exactly
```

Full source: openspec/changes/scheduler-callback-protocol-v2/design.md

## openspec/changes/scheduler-callback-protocol-v2/tasks.md

- Source: openspec/changes/scheduler-callback-protocol-v2/tasks.md
- Lines: 1-33
- SHA256: e44ceddcce9cb949b0c701ed3022228724e12d5b2db8d05465a1d155a28d40e5

```md
## 1. Protocol and configuration

- [ ] 1.1 Add RED tests that freeze legacy callback body/header behavior and
  reject partial v2 auth configuration.
- [ ] 1.2 Add opt-in v2 callback auth configuration and validation without
  logging or exposing the HMAC secret.
- [ ] 1.3 Implement canonical v2 envelope, delivery metadata, exact-body
  digest, reserved headers, and HMAC signer.

## 2. Artifact binding

- [ ] 2.1 Add RED tests for canonical sequence-bundle digest, absent output,
  symlink/path traversal, invalid JSON, and non-success terminal callbacks.
- [ ] 2.2 Implement server-rooted controlled `sequence_bundle/v1` collector
  and signed bounded error outcome.

## 3. Regression and documentation

- [ ] 3.1 Add verification tests for signature tampering, header conflict,
  distinct delivery ids, timestamp shape, and legacy callback regression.
- [ ] 3.2 Document v2 configuration and wire contract without adding secrets
  to example configs.
- [ ] 3.3 Run focused and broader scheduler tests in the approved remote test
  environment; freeze a clean candidate and audit the protocol.

## 4. Cross-repository enablement gate

- [ ] 4.1 Update Runtime V2 parser/composition only after the scheduler
  candidate contract is frozen; prove header/body parity and protected-only
  routing.
- [ ] 4.2 Require clean deployment identity, service/output-root ownership,
  namespace isolation, and no-preemption evidence before any configuration
  change or one-job E2E.
```

## openspec/changes/scheduler-callback-protocol-v2/specs/authenticated-callback-delivery/spec.md

- Source: openspec/changes/scheduler-callback-protocol-v2/specs/authenticated-callback-delivery/spec.md
- Lines: 1-43
- SHA256: 56eb6aa20284d2118ef4eb2c150cc55e8688ba070b46dd554634aa90e82087b4

```md
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

```

## openspec/changes/scheduler-callback-protocol-v2/specs/callback-artifact-binding/spec.md

- Source: openspec/changes/scheduler-callback-protocol-v2/specs/callback-artifact-binding/spec.md
- Lines: 1-50
- SHA256: e96cd5c71ff15896ea262474dbc737e86ff18ce9f5b76ee457d6f52eb1a1b63c

```md
## ADDED Requirements

### Requirement: Controlled successful callbacks bind the fixed sequence bundle
mp_sched MUST compute `artifact_manifest_digest` only for a v2 callback whose
opaque task business binding declares `sequence_bundle/v1`, and only
from `<configured artifact root>/<safe opaque run id>/output/sequence_bundle.json`.
It SHALL accept only an opaque run id matching
`[A-Za-z0-9][A-Za-z0-9_-]{0,127}`, require the output directory to contain exactly that non-symlink JSON
file, validate that it contains a `sequence_bundle` object, digest its raw
bytes as `sha256:<lowercase-hex>`, and reject any run id that would escape the
configured root.

#### Scenario: Canonical sequence bundle is signed
- **WHEN** a controlled task reaches `succeeded` and the expected output JSON
  is present below the configured root
- **THEN** the v2 callback contains its canonical manifest digest and the HMAC
  covers the exact body containing that digest

### Requirement: Artifact collection failures cannot claim success binding
mp_sched MUST emit a signed callback with a bounded artifact-binding error for
a controlled v2 `succeeded` callback with missing, invalid, unsafe, or
mismatched artifact binding, and with
bounded artifact-binding error and without an artifact manifest digest.

#### Scenario: Success output is absent
- **WHEN** a controlled task reaches `succeeded` but its sequence bundle is
  absent from the expected root
- **THEN** the callback contains no manifest digest and the receiver can reject
  it without treating it as a bound completion

### Requirement: Non-success terminal callbacks do not invent artifacts
mp_sched SHALL not attach a success artifact manifest digest to failed,
stopped, or timeout callbacks.

#### Scenario: Scheduler timeout is reported
- **WHEN** a controlled scheduler task reaches timeout
- **THEN** its signed callback reports the terminal event/status without a
  sequence-bundle manifest digest

### Requirement: Callback artifact data does not choose a namespace
The callback artifact binding SHALL contain only the fixed contract, opaque run
id reference, digest/error, and mode.  It SHALL NOT carry tenant, Mongo
database, Redis prefix, Runtime generation, fence, wake target, or arbitrary
filesystem path.

#### Scenario: Submitted task contains a malicious path
- **WHEN** controlled task business contains an artifact path or a run id that
  does not match the fixed opaque-id grammar
- **THEN** mp_sched rejects it for artifact collection and does not read outside
  the configured artifact root
```

