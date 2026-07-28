## 1. Source compatibility

- [ ] 1.1 Freeze and record the Jesse committed source snapshot; prove that uncommitted configuration and backups are excluded.
- [ ] 1.2 Produce a file-level behavior classification for all Jesse/PetKing differences and identify the accepted migration set, including the approved legacy dynamic-mount and Docker-socket compatibility waiver.
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
