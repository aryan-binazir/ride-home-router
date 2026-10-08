# Manual roster creation owns its duplicate decision

## Context

The roster editor geocoded a manual create, listed live rows, and rejected a matching `RosterKey` under a process mutex. It then called `CreateWithLabels`, which acquired the Postgres advisory transaction lock before inserting the entity and labels. An import or another server process could insert the same identity between the editor's list and the locked insert. Imports already checked live keys after acquiring that lock.

Raw `Create` and `CreateWithLabels` callers allow duplicates. Restoration deliberately allows an archived identity to become live alongside another match. Changing the raw creation contract or adding a unique constraint would change those policies.

## Decision

Add `CreateManualWithLabels` to the existing participant and driver repositories. The existing roster write core acquires its table's advisory transaction lock, reads live keys with the shared `RosterKey` normalization, and returns `database.ErrDuplicate` for a nonempty matching key. Otherwise it inserts the entity and label memberships in the same transaction. Raw creation uses the same write core without rejecting duplicates.

The editor keeps geocoding before persistence and maps `ErrDuplicate` to its existing name-bearing duplicate error. Its process mutex and live-list check are removed. Adapters retain validation order and duplicate response wording.

Import upserts keep their existing update semantics and oldest live matching row. Archived rows do not match manual creation or imports. Restoration, ordinary updates, and raw writes keep their existing contracts. There is no new repository layer, normalization rule, schema, or planner behavior.

## Consequences

Concurrent manual creates through independent stores yield one insert and a duplicate error. If an import commits first, a waiting manual create rejects its live match. If a manual create commits first, a waiting import updates that row and preserves its labels. Failed label writes roll back the manual entity and memberships.

This is a path-specific duplicate policy. Restore and delete do not acquire the roster lock; restore may introduce duplicates intentionally. Raw creation and ordinary updates may also produce duplicate identities. Direct SQL can bypass the protocol. No global uniqueness guarantee is added.

Integration tests force overlapping transactions through independent connection pools and exercise both manual/import winner orders for participants and drivers. They also cover Unicode and punctuation normalization, archived matching, restoration, raw duplicates, oldest live import matching, and label rollback. These tests prove repository contention without a multi-process HTTP deployment run.
