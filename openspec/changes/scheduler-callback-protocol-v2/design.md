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
one manifest from the expected JSON file below the configured root.  It
requires that `output` contains exactly `sequence_bundle.json`, rejects
symlinks and any unexpected path component, validates the JSON object contains
`sequence_bundle`, hashes the **raw file bytes** as `sha256:<lowercase-hex>`,
and emits that digest only on a `succeeded` event.  This exact byte rule is
deliberately aligned with the existing Runtime controlled collector.

If a v2 controlled success lacks valid artifact binding or collection fails,
the client emits a signed v2 delivery with `artifact_binding_error` and no
digest.  That is intentionally non-completable at Runtime.  It avoids turning
a missing artifact into a forged success while retaining diagnostic evidence.
Non-success terminal events never claim a success artifact digest.

The alternative—Runtime recomputation without a scheduler digest—was rejected
for this target because the required final completion envelope explicitly
contains an authenticated manifest digest.

### 4. V2 is opt-in and ordinary callbacks are unchanged

Absent a complete auth configuration, `Client.Fire` follows the exact legacy
code path: legacy body shape and configured static headers only.  V2 adds
headers only after body construction succeeds.  Existing headers cannot
override v2 reserved header names; the scheduler writes reserved headers last.
This permits Runtime to retain ordinary callback behavior for versionless jobs
while its protected route requires v2.

### 5. No secret in logs, errors, or callback payloads

The auth secret is used only in-process as HMAC key material.  Configuration
validation and errors name fields but never include values.  Tests use fixture
keys only.  The payload contains key id and signature, not the secret.

## Risks / Trade-offs

- [Output root is not mounted/owned by scheduler] -> collector produces a
  signed binding error; Runtime rejects completion. Deployment must prove the
  mount, service identity, and no-overlap condition before enablement.
- [Clock skew] -> Runtime enforces its configured window; scheduler emits
  precise UTC time but does not decide receiver acceptance.
- [HMAC key rotation] -> key id identifies a verifier; overlap/retirement is
  managed by deploy configuration, not automatic fallback.
- [Static legacy headers] -> retained for old consumers only; they cannot
  activate v2 or protected routing.
- [Runtime source changes while this branch is built] -> protocol tests freeze
  scheduler semantics; later cross-repo integration validates parser parity.

## Migration Plan

1. Add contract tests for legacy byte compatibility and v2 header/body
   authenticity before implementation.
2. Add opt-in config parsing, signer, canonical artifact collector, and docs.
3. Run scheduler unit tests and relevant integration tests in an isolated test
   environment; do not run services or alter config.
4. Freeze a scheduler candidate and audit the protocol.
5. Only after Runtime header-aware integration, clean deployment identity,
   service/mount/no-preemption proofs, and all approval gates, configure a key
   and conduct the one-job controlled E2E.

Rollback is configuration-only: disable v2 auth and retain the legacy callback
path.  No database migration is required.

## Open Questions

- What exact scheduler service identity and output-root mount will be used in
  the eventual controlled deployment? This change deliberately does not read
  a live configuration to answer it.
- Does the existing controlled Runtime submit payload already contain the
  opaque `run_id` and fixed artifact contract in mp_sched `Business`, or is a
  small Runtime submission-adapter follow-up required? Cross-repo integration
  tests must prove this before enablement.
