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
