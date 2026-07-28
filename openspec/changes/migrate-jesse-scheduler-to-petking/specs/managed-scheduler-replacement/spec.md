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
