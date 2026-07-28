## Context

The server runs `mp-controller` and `mp-worker` from a Jesse checkout at committed SHA `2ef1690c`, plus two uncommitted configuration edits and a backup file.  The PetKing baseline is `4543343`; it is a shallow boundary whose merge ancestry cannot be used as the sole migration mechanism.  A file-level, behavior-first migration is therefore required.

The target is not an in-place overwrite.  PetKing becomes a separately deployed, managed scheduler only after it proves compatibility and exclusive ownership of new task claims.

## Source boundary

| Source | Accepted | Excluded |
|---|---|---|
| Jesse committed snapshot `2ef1690c` | reviewed Go behavior, tests, API and schema-compatible changes | `configs/*.yaml`, tracked configuration backup, scripts that start/stop the old process, credentials, machine paths |
| Jesse working tree | nothing | all uncommitted files and the backup |
| PetKing callback-v2 branch `bf56405` | signed callback, artifact binding, durable outbox and tests | deployment configuration and live service state |

The migration branch must retain `origin` as `PetKing-Technology/mp_sched`.  The Jesse snapshot is comparison input only and must never become a push remote.

### Accepted migration risk: legacy container privileges

The operator selected compatibility option 1 on 2026-07-28.  The migration SHALL preserve Jesse's committed dynamic absolute host-path mounts and writable `/var/run/docker.sock` mount for compatibility.  This is an explicit temporary acceptance of the legacy container-privilege boundary, not a default for new ZymCTRL profiles.  The frozen candidate audit and cutover evidence MUST name this waiver and prove that no uncommitted configuration or backup was imported.

## Architecture and flow

1. Compare the two committed trees and classify every difference as provider behavior, API/model/config-schema behavior, callback behavior, test-only, documentation, or deployment-only.
2. Write regression tests for each accepted Jesse behavior before transplanting it.  Tests must cover GPU slot accounting, Docker start parameters, resource/mount handling, and task pipeline behavior without machine-specific configuration.
3. Apply only the accepted source changes to the PetKing migration branch, preserving PetKing-safe defaults and excluding both committed and uncommitted configuration artifacts.
4. Rebase/cherry-pick the reviewed callback-v2 commits onto that baseline and resolve conflicts by preserving legacy callback behavior when callback auth is absent.
5. Validate schema/API compatibility against a disposable PostgreSQL database.  No scheduler instance may share a writable task store with another worker during cutover.
6. Build a dedicated PetKing controller and worker release plus managed service definitions.  They must use an explicit deploy SHA, service owner, task-store ownership mode, artifact root, and callback destination; signing material is injected by protected deployment configuration and never printed.

## Cutover state machine

```text
DISCOVERED -> COMPATIBLE_CANDIDATE -> SHADOW_VALIDATED -> QUIESCED_OLD
-> PETKING_SINGLE_WRITER -> OBSERVATION_WINDOW -> RETIRED_OLD
```

- `DISCOVERED`: source, consumers, database/schema and service identities recorded.
- `COMPATIBLE_CANDIDATE`: source and callback v2 regressions pass.
- `SHADOW_VALIDATED`: PetKing controller/worker run against isolated test storage; no production task claims.
- `QUIESCED_OLD`: old controller stops admitting new work and old workers stop claiming before the shared production task store is handed over.
- `PETKING_SINGLE_WRITER`: only PetKing worker can claim new work; old processes remain available for evidence/rollback but do not write task state.
- `OBSERVATION_WINDOW`: business outputs, callbacks, GPU/container records and task state are reconciled.
- `RETIRED_OLD`: old services are stopped and checkout retained read-only for the agreed retention period.

Rollback is allowed only before PetKing claims a task from the shared production store.  Once PetKing has claimed a task, that attempt is completed by PetKing; reverting it to Jesse would create duplicate execution risk.

## Safety and no-preemption

The later managed PetKing worker must use one explicit controlled policy for ZymCTRL: one concurrent job, no automatic runtime timeout sweeper, no orphan reaper that calls provider stop, and no automatic retry that resubmits a claimed attempt.  This policy is separately evidenced before any ZymCTRL job.  The global scheduler migration itself does not change task timeout semantics without a consumer compatibility decision.

## Verification

- Tree manifest proves no `configs/*.yaml`, backup, secret, or machine-specific file was migrated.
- Focused regression tests first, then `go test ./...` and PostgreSQL-backed callback restart/retry test.
- Source/API/schema compatibility report records accepted and rejected Jesse deltas.
- Static deployment review verifies an isolated PetKing checkout, managed units, single-writer handoff, explicit rollback boundary, and no secret exposure.
- Real replacement E2E requires a non-GPU representative task first; ZymCTRL E2E remains a final, separately gated single job.
