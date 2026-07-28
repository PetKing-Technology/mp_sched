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
