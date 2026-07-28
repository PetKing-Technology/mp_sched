---
comet_change: migrate-jesse-scheduler-to-petking
role: technical-design
canonical_spec: openspec
---

# PetKing Scheduler Replacement Design

## Decision

PetKing-Technology/mp_sched is the canonical scheduler source.  The existing
JesseEisen deployment is replaced only after a staged, reversible cutover.  No
active checkout, configuration file, service, callback endpoint, or GPU job is
changed by this design artifact.

## Compatibility boundary

The candidate preserves the two behaviours that are present in Jesse's
committed scheduler source and needed for an equivalent first cutover:

1. A business may provide absolute host-path mounts using either the legacy
   `host_path`/`container_path` fields or the source/target form.
2. Docker workloads retain a writable `/var/run/docker.sock` bind mount.

This is a migration-only compatibility waiver.  It is not a default privilege
for a newly introduced controlled ZymCTRL profile.  A future least-privilege
profile must be separately designed and accepted.

## Source ownership and layering

The clean worktree starts at PetKing baseline `454334313ca0ce66fac922dc888f7266fd225633`.
Only classified code behaviour is transplanted from the frozen Jesse source
snapshot `2ef1690ce954f85fb6eb705013e37cf5cd61eb63`; local Jesse configuration,
backups, service state, and credentials are excluded.  Runtime V2 callback-v2
capabilities are then layered as source-only commits.  The migration candidate
therefore has no dependency on the active Jesse checkout's dirty configuration.

## Behavioural design

`BusinessSpec` normalizes both mount representations into `BusinessMount`.
`StartParamsSnapshot` validates absolute paths, replaces conflicting target
mounts deterministically, and rejects unsupported legacy mount sources.  The
Docker socket remains explicitly writable for compatibility.

GPU allocation is persisted in task `extra` during the same task update that
claims the task.  Start parameter construction consumes that assigned GPU ID,
so concurrent tasks do not independently choose the same free slot.

Callback protocol V2 adds canonical signed terminal delivery, artifact binding,
and a durable callback-delivery outbox.  Delivery claims are database-backed,
recover expired leases, and make a completed delivery idempotent across a
worker restart.  These changes are source-only until the Runtime V2 bridge has
an explicitly approved key resolver and service deployment identity.

## Cutover safety invariants

- One scheduler writer owns a task database during a cutover window.
- The deployment checkout is a clean PetKing checkout on the approved deploy
  branch; it is never the active Jesse tree.
- Configuration is copied as a reviewed, redacted mapping rather than read or
  committed wholesale from the dirty active checkout.
- A live switch is permitted only after service ownership, callback endpoint,
  data-store ownership, branch/ref, and rollback command are independently
  evidenced.
- No GPU task is submitted as part of source migration verification.

## Verification

Tests cover legacy mount retention, unsupported-source rejection, assigned GPU
selection, signed callback construction, artifact enforcement, idempotency,
outbox lease recovery, restart delivery, and the complete Go regression suite.
The final candidate audit reviews changed source only.  Real deployment and
end-to-end testing remain a later gated phase because current active services
are unmanaged session processes rooted in the Jesse checkout.
