---
role: technical-design
comet_change: scheduler-callback-protocol-v2
canonical_spec: openspec
status: approved-for-build
---

# mp_sched callback protocol v2 technical design

The canonical behavioral requirements are in OpenSpec change
`scheduler-callback-protocol-v2`; this document records implementation
boundaries and cross-repository handoff constraints.

## Boundary

The scheduler authenticates a callback **delivery**. Runtime V2 remains the
authority for tenant, task, work-unit, generation, attempt, fence, durable
current-attempt selection, namespace routing, and completion/wake side
effects.  A callback body must never select those authorities.

Protocol v2 is enabled only with both `callback.auth.key_id` and an in-process
HMAC key.  The scheduler constructs one JSON body, hashes its exact bytes, and
signs a length-delimited tuple of protocol version, key id, delivery id,
occurrence timestamp, and body hash.  The bridge verifies this before parsing
the body as protected completion input.

The sender performs a new delivery-id/timestamp generation for every send
attempt.  Runtime stores receipt idempotency across its own restart.  This is
deliberate: a network retry is an authenticated new delivery, while a replay
of captured bytes is detectable by Runtime's persisted receipt/current attempt
gate.

## Artifact binding

For the controlled `sequence_bundle/v1` contract, scheduler callback code reads
only a configured root plus a validated opaque run id from task business.  It
must resolve the exact expected path, require only `sequence_bundle.json`,
reject paths/symlinks before reading, validate the JSON object, and digest the
raw file bytes as `sha256:<lowercase-hex>`.  The result digest is included in
the signed body only for a successful, correctly bound artifact.  Errors are
signed but non-completable.

This creates a hard deployment dependency: the scheduler service must have
read access to exactly the dedicated controlled output root, and no other
namespace.  That mount/identity proof is not created, inspected, or changed in
this source change.

## Compatibility

No auth configuration means the old callback code path stays byte-compatible:
same JSON fields and static configured headers.  V2 reserved headers are
written last to prevent configuration values from falsifying signed metadata.
Versionless callback recipients retain their present behavior; Runtime will
require v2 only after the protected job lookup resolves a controlled job.

## Acceptance handoff

Before Runtime integration, freeze the scheduler candidate and provide test
fixtures proving raw-body HMAC verification, body/header tamper rejection,
delivery uniqueness, legacy behavior, artifact success, and artifact failure.
Runtime then adds header-aware ingress validation, compares scheduler job
mapping/fence/current-attempt state, recomputes the output manifest as a
second binding check, and routes only the fixed controlled namespace.

No actual secret, callback configuration, shared service, GPU job, old stack,
or D4 component is involved in this design or its tests.
