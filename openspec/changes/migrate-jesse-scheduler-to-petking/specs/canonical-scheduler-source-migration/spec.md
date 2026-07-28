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
