# Comet Design Handoff

- Change: migrate-jesse-scheduler-to-petking
- Phase: design
- Mode: full
- Context hash: 4f1bf3d98280a4adf830350ded32b1d0d2d2bf5daa355e48186f7458daac0b1c

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## openspec/changes/migrate-jesse-scheduler-to-petking/proposal.md

- Source: openspec/changes/migrate-jesse-scheduler-to-petking/proposal.md
- Lines: 1-28
- SHA256: 88b4b7ab581350354cb4264fbf54e8fc310b7525ada9a87010cd114f23e45bd3

```md
## Why

The active scheduler is sourced from `JesseEisen/mp_sched` in a dirty, session-owned checkout.  It cannot be safely extended, audited, or replaced in place.  The canonical `PetKing-Technology/mp_sched` repository must absorb the reviewed runtime behavior before it becomes the sole managed scheduler and before authenticated callback v2 is enabled.

## What Changes

- Migrate reviewed, committed scheduler behavior from Jesse source snapshot `2ef1690ce954f85fb6eb705013e37cf5cd61eb63` into a PetKing-owned branch; exclude all environment configuration, backups, and credentials.
- Preserve the existing scheduler HTTP/task and Docker-provider behavior through compatibility tests.
- Layer the signed callback v2, scheduler-owned artifact binding, and durable delivery outbox on the PetKing migration baseline.
- Define a managed blue/green replacement contract so PetKing becomes the only scheduler writer without two workers consuming the same task store.
- **BREAKING:** the formal scheduler source and service ownership change from Jesse/session-owned deployment to PetKing/managed deployment after cutover.

## Capabilities

### New Capabilities

- `canonical-scheduler-source-migration`: imports reviewed Jesse runtime behavior into the PetKing canonical source while excluding deployment-only state.
- `managed-scheduler-replacement`: defines the single-writer, rollback-safe deployment contract for replacing the existing scheduler service.

### Modified Capabilities

- None.

## Impact

- PetKing scheduler source, Docker-provider behavior, callback delivery, configuration schema, tests, and release/deployment assets.
- Existing scheduler consumers, task storage, Docker/GPU execution, and the shared Runtime callback bridge.
- No live service, database, configuration, credential, or GPU action is performed until the replacement gates in this change pass.
```

## openspec/changes/migrate-jesse-scheduler-to-petking/design.md

- Source: openspec/changes/migrate-jesse-scheduler-to-petking/design.md
- Lines: 1-53
- SHA256: e6049de612f9583d8d50a0f6ba2443df1b296c20aae22a895d25c278e21c6a4d

```md
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
```

## openspec/changes/migrate-jesse-scheduler-to-petking/tasks.md

- Source: openspec/changes/migrate-jesse-scheduler-to-petking/tasks.md
- Lines: 1-24
- SHA256: 7c6e7b0aa0450d5c93c3526b5dc696e8169a80ab089e64bb86537498443429fb

```md
## 1. Source compatibility

- [ ] 1.1 Freeze and record the Jesse committed source snapshot; prove that uncommitted configuration and backups are excluded.
- [ ] 1.2 Produce a file-level behavior classification for all Jesse/PetKing differences and identify the accepted migration set.
- [ ] 1.3 Add RED compatibility tests for every accepted provider, pipeline, model, or API behavior.

## 2. PetKing source migration

- [ ] 2.1 Transplant accepted Jesse behavior into the PetKing migration branch without copying deployment configuration, backups, credentials, or old service scripts.
- [ ] 2.2 Make the new tests and existing PetKing regression suite pass; preserve legacy callback behavior with callback auth absent.
- [ ] 2.3 Layer callback v2, artifact binding, and durable delivery outbox onto the migrated baseline and resolve any conflicts with regression coverage.

## 3. Deployment replacement contract

- [ ] 3.1 Record the authoritative PetKing deploy checkout, branch/SHA, service owner, controller/worker units, and rollback owner.
- [ ] 3.2 Prove task-store schema compatibility and define the single-writer handoff so Jesse and PetKing cannot claim concurrently.
- [ ] 3.3 Define protected non-secret injection evidence for callback signing, artifact root, namespace routing, and ZymCTRL no-preemption.

## 4. Verification and cutover

- [ ] 4.1 Run focused tests, full `go test ./...`, a PostgreSQL compatibility test, and the durable-delivery restart test.
- [ ] 4.2 Audit the frozen candidate and resolve blocking/high findings.
- [ ] 4.3 Deploy an isolated PetKing shadow candidate; verify a non-GPU representative end-to-end task without touching the old scheduler's task store.
- [ ] 4.4 Execute the authorized single-writer handoff, observe outputs/callbacks, and retain a rollback evidence record before retiring Jesse.
```

## openspec/changes/migrate-jesse-scheduler-to-petking/specs/canonical-scheduler-source-migration/spec.md

- Source: openspec/changes/migrate-jesse-scheduler-to-petking/specs/canonical-scheduler-source-migration/spec.md
- Lines: 1-21
- SHA256: 325f40746c29ad64785aa2f98e2d0d7c0c23f2c7e53298970c3b5a058a5f46da

```md
## ADDED Requirements

### Requirement: PetKing preserves reviewed scheduler behavior without inheriting runtime state

The canonical PetKing scheduler SHALL migrate only reviewed, committed source behavior from the approved Jesse snapshot.  It SHALL NOT import configuration files, backup files, credentials, machine-specific paths, service-control scripts, or uncommitted working-tree state.

#### Scenario: Migration manifest excludes deployment state

- **WHEN** the migration candidate is compared with the approved Jesse snapshot
- **THEN** every imported file is classified and reviewed
- **AND** configuration, backups, credentials, machine paths, old service scripts, and uncommitted state are absent from the imported set.

### Requirement: Legacy callback behavior remains compatible

The PetKing scheduler SHALL preserve legacy callback payload and header behavior when authenticated callback v2 is not configured.

#### Scenario: Auth is absent

- **WHEN** callback auth material is absent or incomplete
- **THEN** the scheduler sends the legacy callback contract
- **AND** it does not create an authenticated-delivery outbox record.
```

## openspec/changes/migrate-jesse-scheduler-to-petking/specs/managed-scheduler-replacement/spec.md

- Source: openspec/changes/migrate-jesse-scheduler-to-petking/specs/managed-scheduler-replacement/spec.md
- Lines: 1-21
- SHA256: e4b7ad5913354a33a91ab005fc5452a724a259abf4dcfc49ee5aa86059d85e52

```md
## ADDED Requirements

### Requirement: Cutover has exactly one scheduler task claimant

The scheduler replacement SHALL prevent Jesse and PetKing workers from concurrently claiming tasks from the same writable task store.

#### Scenario: Handoff to PetKing

- **WHEN** production task claiming is handed to PetKing
- **THEN** Jesse workers are quiesced before PetKing claim permission is enabled
- **AND** evidence identifies the sole active claimant.

### Requirement: Replacement is reversible only before ownership transfers

The replacement SHALL define a rollback boundary that does not reassign an attempt already claimed by PetKing back to Jesse.

#### Scenario: PetKing has claimed an attempt

- **WHEN** a PetKing worker has persisted a claim for an attempt
- **THEN** rollback does not offer that attempt to Jesse
- **AND** the attempt is completed or terminally reconciled by PetKing.
```

