# Scheduler Callback Protocol V2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in, signed scheduler callback v2 with an authenticated sequence-bundle manifest binding while leaving legacy callbacks unchanged.

**Architecture:** `internal/callback` owns envelope serialization, signing, and safe artifact collection. `internal/config.Callback` holds only configuration references; Runtime remains responsible for protected-run authority and namespace routing. The exact contract is OpenSpec `scheduler-callback-protocol-v2`.

**Tech Stack:** Go 1.25, standard library `crypto/hmac`, `crypto/sha256`, `net/http/httptest`, existing `google/uuid`, OpenSpec/Comet.

## Global Constraints

- Do not read, print, commit, or log real signing keys; tests use fixture values only.
- Do not edit a deployed callback configuration, run services, submit a job, or alter providers/D4/legacy.
- Legacy auth-absent callback bytes and static headers must remain unchanged.
- Artifact collector accepts only configured root + opaque run id + `sequence_bundle/v1`.

---

### Task 1: Freeze legacy and v2 wire contract

**Files:**
- Create: `internal/callback/client_test.go`
- Modify: `internal/callback/client.go`

**Interfaces:**
- Produces `buildDelivery(event string, task *model.Task) (body []byte, headers http.Header, err error)` used by `Fire`.

- [ ] **Step 1: Write failing wire tests**

```go
func TestFireWithoutAuthKeepsLegacyBodyAndHeaders(t *testing.T) { /* httptest records body/header */ }
func TestV2DeliveryVerifiesExactBodyHMAC(t *testing.T) { /* tamper body -> digest/signature fails */ }
```

- [ ] **Step 2: Run RED**

Run: `go test ./internal/callback -run 'Test(FireWithoutAuth|V2Delivery)' -count=1`

Expected: compilation or assertion failure because v2 delivery builder does not exist.

- [ ] **Step 3: Implement canonical builder and signer**

```go
type deliveryMeta struct { Version int; DeliveryID, OccurredAt string }
func canonicalMACInput(keyID, deliveryID, occurredAt, bodyDigest string) []byte
func signV2(secret []byte, keyID string, body []byte, meta deliveryMeta) http.Header
```

Use a body SHA-256 and length-prefixed canonical fields; write reserved headers after configured headers.

- [ ] **Step 4: Run GREEN and commit**

Run: `go test ./internal/callback -count=1`

Commit: `feat(callback): sign protocol v2 deliveries`

### Task 2: Add fail-closed opt-in configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/callback/client_test.go`

**Interfaces:**
- Produces `config.CallbackAuth{KeyID, HMACSecret, ArtifactRoot string}` and `Enabled() bool`.

- [ ] **Step 1: Write failing configuration tests** for absent key, absent key id, and complete fixture configuration.
- [ ] **Step 2: Run RED** with `go test ./internal/callback -run TestV2Config -count=1`.
- [ ] **Step 3: Add `Callback.Auth`** with mapstructure/yaml tags and `Enabled()` requiring both key id and secret. Never include secret in an error.
- [ ] **Step 4: Run GREEN and commit** with `feat(config): add callback v2 auth settings`.

### Task 3: Implement fixed artifact binding

**Files:**
- Create: `internal/callback/artifact.go`
- Create: `internal/callback/artifact_test.go`
- Modify: `internal/callback/client.go`

**Interfaces:**
- Produces `collectSequenceBundle(root string, business []byte) (digest string, binding map[string]any, err error)`.

- [ ] **Step 1: Write failing collector tests** for valid JSON, missing file, symlink, `../` run id, invalid JSON, and failed/timeout status.
- [ ] **Step 2: Run RED** with `go test ./internal/callback -run TestCollectSequenceBundle -count=1`.
- [ ] **Step 3: Implement collector** that parses only a fixed internal business binding, accepts a run id only when it matches `[A-Za-z0-9][A-Za-z0-9_-]{0,127}`, requires exactly one non-symlink `sequence_bundle.json` below root, validates its JSON object, and hashes its raw bytes as `sha256:<lowercase-hex>`.
- [ ] **Step 4: Integrate only on `succeeded`**; on collection failure emit signed bounded `artifact_binding_error` without a digest.
- [ ] **Step 5: Run GREEN and commit** with `feat(callback): bind controlled sequence bundle digest`.

### Task 4: Documentation and regression

**Files:**
- Modify: `configs/default.yaml`
- Modify: `docs/CONFIG.md`
- Modify: `docs/API.md`
- Modify: `internal/callback/client_test.go`

- [ ] **Step 1: Add non-secret commented configuration schema** with placeholder names only.
- [ ] **Step 2: Document exact headers, MAC grammar, timestamp/replay ownership, artifact failure semantics, and legacy compatibility.**
- [ ] **Step 3: Add regression tests** for two unique delivery ids, static reserved-header conflict, HMAC tampering, and secret non-leak.
- [ ] **Step 4: Run focused GREEN**: `go test ./internal/callback ./internal/config -count=1`.
- [ ] **Step 5: Commit**: `docs(callback): specify protocol v2`.

### Task 5: Candidate verification and Runtime handoff

**Files:**
- Modify: `openspec/changes/scheduler-callback-protocol-v2/tasks.md`
- Create: `docs/superpowers/reports/2026-07-27-scheduler-callback-protocol-v2-verify.md`

- [ ] **Step 1: Run remote focused and broader Go tests** in the approved test worktree; do not run a service.
- [ ] **Step 2: Freeze clean candidate SHA and audit protocol implementation.**
- [ ] **Step 3: Record exact header fixture and artifact fixture handoff for Runtime.**
- [ ] **Step 4: Do not enable/deploy/E2E** until Runtime parser, service identity, mount/no-overlap, namespace, and no-preemption gates pass.

## Self-review

- Spec coverage: Tasks 1–2 cover authenticated delivery; Task 3 covers fixed artifact binding; Task 4 covers compatibility/documentation; Task 5 covers required verification and Runtime handoff.
- Placeholder scan: no deferred implementation placeholder is used; each coding task names files, interfaces, commands, and acceptance behavior.
- Type consistency: `buildDelivery` owns bytes/headers; `CallbackAuth` owns opt-in settings; `collectSequenceBundle` is the only artifact collector.
