--------------------------- MODULE RouteSessionSave ---------------------------
(***************************************************************************)
(* Concurrent edits and saves of one durable route session, from any       *)
(* number of browser tabs and server instances. See                         *)
(* verification/tla/README.md for the coverage record, bounds and           *)
(* assumptions.                                                             *)
(*                                                                          *)
(* Code (each action names the code it stands for):                         *)
(*   internal/routesession/persistent.go  update, ResetContext,             *)
(*                                        CommitEvent, DeleteContext        *)
(*   internal/routesession/store.go       ApplyMoves and the other edits    *)
(*   internal/postgres/workflows.go       Load, CompareAndSwap, Transact,   *)
(*                                        Delete, CleanupWorkflows          *)
(*   internal/handlers/events.go          commitEventSession, persistEvent, *)
(*                                        HandleDeleteEvent                 *)
(*   migrations/20260912211512_event_route_session.up.sql                   *)
(*                                        events_route_session_id_idx       *)
(*                                                                          *)
(* Every durable edit (moves, swaps, driver changes, ResetContext) goes     *)
(* through Store.update, in two steps as ADR 0008 asks:                     *)
(* Load returns payload and revision without a lock; the edit is applied    *)
(* and distances recalculated outside any transaction; CompareAndSwap then  *)
(* writes the result under the row lock. The payload is modelled as the set *)
(* of route edits it contains: an edit adds its id, a reset empties it.    *)
(* The reviewer note is left out (a reset keeps it; the Event omits it).    *)
(* The ghost "expected" applies each acknowledged request, in the order its *)
(* write committed, to its own copy, so the payload must always equal it.   *)
(* "Acknowledged" means the write committed; HTTP responses may still reach *)
(* the browser in another order.                                            *)
(*                                                                          *)
(* A save is one step: CommitEvent runs inside Transact, which holds the    *)
(* session row lock (FOR UPDATE) from the read until commit, and inserts    *)
(* the Event in the same transaction.                                       *)
(*                                                                          *)
(* Each Guard* constant is one guard in the code, or a group of guards      *)
(* that back each other up. The main config sets all of them TRUE. The     *)
(* configs in mutations/ set some FALSE and must fail; scripts/tla.sh       *)
(* checks both.                                                             *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Edits,    \* edit requests, each adding its own change
    Resets,   \* ResetContext requests: back to the calculated routes
    Saves,    \* save requests (a double click, two tabs)
    \* CompareAndSwap: "record.Revision != revision" -> ErrWorkflowConflict.
    GuardCASChecksRevision,
    \* update: "record.Consumed" after Load -> ErrNotFound, and
    \* CompareAndSwap: "record.Consumed" -> ErrWorkflowConflict.
    GuardEditRejectsSaved,
    \* CommitEvent: "record.Consumed" -> ErrAlreadyCommitted.
    GuardSaveRejectsSaved,
    \* events_route_session_id_idx: a second live Event for the session fails.
    GuardUniqueEventPerSession

Requests == Edits \cup Resets

\* The session payload after request q is applied to payload p.
Apply(p, q) == IF q \in Resets THEN {} ELSE p \cup {q}

VARIABLES
    exists,       \* workflow_sessions row present and unexpired
    revision,     \* workflow_sessions.revision
    data,         \* workflow_sessions.payload: the edits it contains
    consumed,     \* workflow_sessions.consumed
    pc,           \* request step
    seenRev,      \* revision returned by the request's Load
    seenData,     \* payload returned by the request's Load
    liveEvents,   \* Events rows for this route_session_id
    expected,     \* ghost: acknowledged requests applied in order
    created,      \* ghost: Events ever created for this session (saturating)
    savedOk,      \* ghost: every Event matched "expected" when created
    ackAfterSave  \* ghost: a request was acknowledged after the save

vars == <<exists, revision, data, consumed, pc, seenRev, seenData, liveEvents,
          expected, created, savedOk, ackAfterSave>>

MaxRevision == Cardinality(Requests) + Cardinality(Saves)

TypeOK ==
    /\ exists \in BOOLEAN
    /\ revision \in 0..MaxRevision
    /\ data \subseteq Edits
    /\ consumed \in BOOLEAN
    /\ pc \in [Requests \cup Saves -> {"start", "loaded", "acked", "rejected"}]
    /\ seenRev \in [Requests -> 0..MaxRevision]
    /\ seenData \in [Requests -> SUBSET Edits]
    /\ liveEvents \subseteq Saves
    /\ expected \subseteq Edits
    /\ created \in 0..2
    /\ savedOk \in BOOLEAN
    /\ ackAfterSave \in BOOLEAN

\* CreateContext inserts the calculated routes at revision 0.
Init ==
    /\ exists = TRUE
    /\ revision = 0
    /\ data = {}
    /\ consumed = FALSE
    /\ pc = [q \in Requests \cup Saves |-> "start"]
    /\ seenRev = [q \in Requests |-> 0]
    /\ seenData = [q \in Requests |-> {}]
    /\ liveEvents = {}
    /\ expected = {}
    /\ created = 0
    /\ savedOk = TRUE
    /\ ackAfterSave = FALSE

Finished(q, outcome) == pc' = [pc EXCEPT ![q] = outcome]

\* workflowRepository.Load: UPDATE ... SET expires_at RETURNING payload,
\* revision, consumed. It slides the expiry without bumping the revision,
\* so reads never conflict with edits. update() turns a missing or consumed
\* record into ErrNotFound.
EditLoads(q) ==
    /\ pc[q] = "start"
    /\ IF exists /\ (~GuardEditRejectsSaved \/ ~consumed)
       THEN /\ Finished(q, "loaded")
            /\ seenRev' = [seenRev EXCEPT ![q] = revision]
            /\ seenData' = [seenData EXCEPT ![q] = data]
       ELSE /\ Finished(q, "rejected")
            /\ UNCHANGED <<seenRev, seenData>>
    /\ UNCHANGED <<exists, revision, data, consumed, liveEvents, expected,
                   created, savedOk, ackAfterSave>>

\* workflowRepository.CompareAndSwap, through Update and Transact: lock the
\* row, reject a changed revision or a consumed record, write the payload
\* the request computed from its Load, bump the revision.
EditWrites(q) ==
    /\ pc[q] = "loaded"
    /\ IF /\ exists
          /\ ~GuardCASChecksRevision \/ revision = seenRev[q]
          /\ ~GuardEditRejectsSaved \/ ~consumed
       THEN /\ data' = Apply(seenData[q], q)
            /\ revision' = revision + 1
            /\ Finished(q, "acked")
            /\ expected' = Apply(expected, q)
            /\ ackAfterSave' = (ackAfterSave \/ consumed)
       ELSE /\ Finished(q, "rejected")
            /\ UNCHANGED <<data, revision, expected, ackAfterSave>>
    /\ UNCHANGED <<exists, consumed, seenRev, seenData, liveEvents, created, savedOk>>

\* Store.CommitEvent inside Transact: lock the row, reject a consumed record
\* with ErrAlreadyCommitted, build the CommitSnapshot from the payload,
\* persistEvent -> CreateEvent in the same transaction, then mark the record
\* consumed with payload {} and bump the revision. A failing CreateEvent
\* (the unique index) rolls the whole transaction back. Not modelled: a later
\* save of the cleared {} payload also fails eventsnapshot.Build (no route
\* mode), a backstop outside CommitEvent's own contract.
Save(s) ==
    /\ pc[s] = "start"
    /\ IF /\ exists
          /\ ~GuardSaveRejectsSaved \/ ~consumed
          /\ ~GuardUniqueEventPerSession \/ liveEvents = {}
       THEN /\ Finished(s, "acked")
            /\ liveEvents' = liveEvents \cup {s}
            /\ created' = IF created < 2 THEN created + 1 ELSE 2
            /\ savedOk' = (savedOk /\ data = expected)
            /\ consumed' = TRUE
            /\ data' = {}
            /\ revision' = revision + 1
       ELSE /\ Finished(s, "rejected")
            /\ UNCHANGED <<liveEvents, created, savedOk, consumed, data, revision>>
    /\ UNCHANGED <<exists, seenRev, seenData, expected, ackAfterSave>>

\* HandleDeleteEvent: the person deletes the saved Event later.
DeleteEvent(s) ==
    /\ s \in liveEvents
    /\ liveEvents' = liveEvents \ {s}
    /\ UNCHANGED <<exists, revision, data, consumed, pc, seenRev, seenData,
                   expected, created, savedOk, ackAfterSave>>

\* DeleteContext (DELETE ... WHERE NOT consumed: cancel, a new calculation
\* replacing this one, capacity eviction in Create), or the sliding TTL
\* lapsing and CleanupWorkflows removing the row.
Discard ==
    /\ exists
    /\ exists' = FALSE
    /\ UNCHANGED <<revision, data, consumed, pc, seenRev, seenData, liveEvents,
                   expected, created, savedOk, ackAfterSave>>

Next ==
    \/ \E q \in Requests: EditLoads(q) \/ EditWrites(q)
    \/ \E s \in Saves: Save(s) \/ DeleteEvent(s)
    \/ Discard

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Properties.                                                             *)

\* The live session holds exactly the acknowledged edits, applied in order:
\* no edit reported as applied is silently undone by a concurrent one.
SessionMatchesAckedEdits == (exists /\ ~consumed) => data = expected

\* The saved Event holds exactly the acknowledged edits at the moment of
\* the save.
SavedEventMatchesAckedEdits == savedOk

\* No edit is reported as applied after the session was saved, when it can
\* no longer reach the Event.
NoEditAckedAfterSave == ~ackAfterSave

\* One route session becomes at most one saved Event, however often Save is
\* clicked, from however many tabs, and even after the Event is deleted.
EventSavedAtMostOnce == created <= 1

=============================================================================
