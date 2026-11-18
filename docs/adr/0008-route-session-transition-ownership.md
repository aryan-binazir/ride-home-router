# Route-session transition ownership

Route sessions are temporary plans that callers edit before saving an Event. The route-session Store builds and edits their state. The shared workflow repository owns persistence, expiry, and capacity.

An edit loads and decodes a session, applies the transition, and writes the result with a revision check. Distance calls run outside the transaction. A stale write returns `ErrWorkflowConflict`. The load refreshes expiry even if validation or recalculation later fails. A failed transition discards the decoded state.

When the lookup is the local estimator, decoding re-estimates both original and current routes or clears metrics it cannot estimate. A read returns the scrubbed snapshot. A successful edit persists that state. Measured provider metrics stay out of persisted sessions and Events.

Saving rejects an unbalanced plan and builds a copied `CommitSnapshot`. The callback writes the Event with the transaction's writer. The Store marks the record consumed and clears its payload in the same transaction. Feedback is captured after a successful commit on a best-effort basis, as ADR 0001 requires.

Using `WorkflowRepository.Update` for edits would hold the row lock during distance work and replace the stale-write conflict with a serialized second edit. The Store uses compare-and-swap to preserve this conflict behavior.

Tests use the same Store as production. Postgres tests cover expiry, capacity, conflicts, atomic rollback, and commit-once behavior. Database-free tests supply a workflow repository fake.
