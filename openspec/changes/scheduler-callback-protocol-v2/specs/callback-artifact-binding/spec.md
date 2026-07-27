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
