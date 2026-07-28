# PetKing Scheduler Replacement Implementation Plan

> **For implementation:** Execute in the isolated worktree
> `D:\project\mp_sched-petking-replacement-v1`. Product tests run on the
> approved remote test worktree, never on local Windows.

**Goal:** Make PetKing scheduler the audited source candidate that can replace
the Jesse deployment while retaining explicitly accepted legacy runtime
behaviour, then layer Runtime V2 callback protocol V2 without touching live
services or submitting a GPU job.

**Architecture:** Freeze Jesse committed source as behavioural input; preserve
only classified Docker mount and GPU assignment semantics in PetKing; add the
durable signed callback-v2 outbox; prove source behaviour with focused and full
remote tests.  Treat runtime deployment, secrets, service ownership, and live
cutover as separate gates.

**Tech Stack:** Go, GORM/PostgreSQL, Docker API, Go test, OpenSpec/Comet.

---

### Task 1: Freeze and classify migration input

**Files:**
- Source snapshot: `D:\project\mp_sched-jesse-source-2ef1690`
- Candidate: `D:\project\mp_sched-petking-replacement-v1`
- Change contract: `openspec/changes/migrate-jesse-scheduler-to-petking/*`

1. Record source commit, working-tree state, process ownership, and excluded
   configuration/backups without opening configuration contents.
2. Classify each imported behavioural delta as Docker mount compatibility, GPU
   assignment, or callback-v2 source capability.
3. Keep the active `/mnt/disk_1_4t/huasheng/mp_sched` untouched.

### Task 2: Preserve accepted legacy Docker behaviour

**Files:**
- Modify: `internal/provider/docker/business.go`
- Modify: `internal/provider/docker/startparams.go`
- Test: `internal/provider/docker/startparams_test.go`

1. Add failing tests for legacy host-path mount conversion, target replacement,
   socket writeability, and unsupported mount source rejection.
2. Normalize legacy and source/target mount forms, require absolute paths, and
   reject unrecognized source kinds.
3. Build Docker start parameters from normalized mounts while preserving the
   accepted socket waiver.
4. Run focused remote Docker-provider tests.

### Task 3: Preserve deterministic GPU assignment

**Files:**
- Add: `internal/provider/docker/gpuslots.go`
- Modify: `internal/pipeline/pipeline.go`
- Modify: `internal/provider/docker/startparams.go`
- Test: `internal/provider/docker/gpuslots_test.go`

1. Add a failing test proving the first free slot is assigned once.
2. Persist assignment in task extra with the task claim update.
3. Consume the persisted device ID when creating Docker device requests.
4. Run focused remote provider and pipeline tests.

### Task 4: Layer callback protocol V2

**Files:**
- Modify: `internal/callback/client.go`, `internal/callback/artifact.go`
- Modify: `internal/model/*`, `internal/database/*`, `cmd/*`, `internal/config/*`
- Test: `internal/callback/*_test.go`

1. Preserve signed envelope, canonical byte-length framing, artifact digest
   binding, and protected delivery headers.
2. Persist terminal callback deliveries and claim/reclaim them atomically.
3. Prove replay resistance, delivery idempotency, and restart recovery with
   focused callback and ephemeral PostgreSQL tests.

### Task 5: Candidate verification and audit

**Files:** all changed candidate source and tests.

1. Run `git diff --check`, strict OpenSpec validation, focused tests, and
   `go test ./...` on the remote test worktree.
2. Compare the tested remote source manifest with this candidate's relevant Go
   source manifest.
3. Audit the frozen candidate for cross-tenant callback, delivery lifecycle,
   mount privilege, GPU contention, migration configuration leakage, and
   rollback hazards.
4. Record exact SHA and results in OpenSpec tasks and the L3 progress page.

### Task 6: Discover a deployable cutover contract (gated; no live change)

**Files:** future dedicated deployment manifest/service files only after
identity and ownership evidence exists.

1. Establish an approved clean deploy checkout, branch/ref, service manager,
   callback key resolver, data-store mapping, and rollback command.
2. Require a single-writer handoff protocol and an explicit redacted config
   mapping. Do not copy or expose the active dirty Jesse configuration.
3. Only after these facts and the Runtime V2 ingress gates pass may a separate
   authorized live cutover and one-job E2E plan be created.
