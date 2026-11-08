-------------------------- MODULE ImportGeocodeQueue --------------------------
(***************************************************************************)
(* The durable roster-import geocoding queue, shared by every server        *)
(* instance through Postgres. See verification/tla/README.md for the        *)
(* coverage record, bounds and assumptions.                                 *)
(*                                                                          *)
(* Code (each action names the code it stands for):                         *)
(*   internal/postgres/import_jobs.go    Claim, RowsByIndices, Finish,      *)
(*                                       Release, SelectImportRows,         *)
(*                                       PatchImportSelections              *)
(*   internal/importer/persistent.go     durableWorker, processJob,         *)
(*                                       commitPersistent, CancelContext,   *)
(*                                       SelectRowsContext                  *)
(*   internal/importer/store.go          geocodeGroups, createBatch         *)
(*   internal/postgres/workflows.go      Transact (FOR UPDATE), Delete      *)
(*                                                                          *)
(* One import session in preview. ApplyMapping has staged the rows and one  *)
(* job per distinct address (geocodeGroups: a row is in at most one job;    *)
(* rows sharing an address share a job). JobRows fixes that grouping.       *)
(*                                                                          *)
(* Abstractions, each argued against the code:                              *)
(* - token[j] holds a worker id instead of a random claim token. A          *)
(*   worker's durableWorker loop runs one job at a time and a restarted     *)
(*   process starts with no job, so token[j] = w exactly when w's current   *)
(*   run holds the newest claim. The claim's returned Attempts is kept per  *)
(*   worker in round[w], since other claims may bump the stored value.      *)
(* - leaseLive[j] is "claimed_until > clock_timestamp()". The single        *)
(*   database clock makes expiry a step: LeaseExpires (time passes with     *)
(*   nobody working the job) and ProviderStalls (the holder overruns its    *)
(*   60s lease inside the provider call). ASSUMPTION: the holder cannot     *)
(*   overrun outside the provider call. After it, the persist work has a 5s *)
(*   context and a 5s lock_timeout, and the 30s workCtx leaves 25s or more  *)
(*   of the 60s lease.                                                      *)
(* - An expired but not yet cleaned-up session is "gone": Claim and Finish  *)
(*   both require expires_at > clock_timestamp(), and commit's Transact     *)
(*   finds no row. CleanupWorkflows deletes it later; the cascade changes   *)
(*   nothing the model observes.                                            *)
(* - attempts saturate at MaxGeocodeRounds + 1: processJob only compares    *)
(*   job.Attempts using <= and < MaxGeocodeRounds.                          *)
(* - The provider throttle (provider_throttles.next_at) only delays Claim.  *)
(*   Fairness on Claim stands for "the throttle reopens".                   *)
(* - Database errors (lock_timeout, connection loss) are not modelled. They *)
(*   return early, and the deferred Release runs, like a retryable failure. *)
(*                                                                          *)
(* Each Guard* constant is one guard in the code, or a group of guards      *)
(* that back each other up. The main config sets all of them TRUE. The     *)
(* configs in mutations/ set some FALSE and must fail; scripts/tla.sh       *)
(* checks both.                                                             *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    Workers,            \* server instances, one durableWorker each
    None,               \* model value: no claim token, no job
    JobRows,            \* job -> the import_rows indices it geocodes
    MaxGeocodeRounds,   \* importer.maxGeocodeRounds = 3
    MaxCrashes,         \* crashes outside the provider call (bounded)
    SelectableRows,     \* rows the person toggles while geocoding runs
    \* Claim's "attempts=attempts+1" reserves the round before the provider
    \* call. FALSE: count the round in Release instead.
    GuardClaimReservesAttempt,
    \* Finish marks the job done and writes its rows in one transaction.
    \* FALSE: mark the job done, then write the rows in a second step.
    GuardFinishIsOneTransaction,
    \* Finish sets "selected=CASE WHEN failed THEN false ELSE selected END",
    \* keeping the selection current in the database. FALSE: write back the
    \* selection that processJob read through RowsByIndices.
    GuardFinishKeepsLiveSelection,
    \* commitPersistent returns ErrGeocodingInProgress for a pending row.
    GuardCommitChecksPending,
    \* "NOT j.done" in Claim, and "AND NOT done" in Finish and Release.
    GuardDoneJobsAreClosed,
    \* Release's "claim_token=$3": a worker only releases its own claim.
    GuardReleaseChecksToken

Jobs == DOMAIN JobRows
Rows == UNION {JobRows[j] : j \in Jobs}

\* Two jobs: the first geocodes one address shared by two rows.
DefaultJobRows == <<{1, 2}, {3}>>

VARIABLES
    session,      \* "previewing" | "committed" | "gone" (cancelled or expired)
    done,         \* import_jobs.done
    token,        \* import_jobs.claim_token: a worker or None
    leaseLive,    \* import_jobs.claimed_until > clock_timestamp()
    attempts,     \* import_jobs.attempts
    geocode,      \* per row: "pending" (NeedsGeocoding), "ok" or "failed"
    selected,     \* import_rows.selected
    pc,           \* worker step
    job,          \* job the worker is processing
    round,        \* job.Attempts as returned to the worker by Claim
    failed,       \* the worker will finish its job as failed
    seenSelected, \* import_rows.selected as read by RowsByIndices
    crashes,      \* crashes outside the provider call so far
    choice,       \* ghost: the person's latest selection of each row
    rounds,       \* ghost: provider rounds started per job (saturating)
    finishes,     \* ghost: successful Finish transactions per job (saturating)
    committedPending \* ghost: Commit ran while some row was pending

workerVars == <<pc, job, round, failed, seenSelected>>
ghostVars == <<choice, rounds, finishes, committedPending>>
vars == <<session, done, token, leaseLive, attempts, geocode, selected, crashes,
          workerVars, ghostVars>>

Cap == MaxGeocodeRounds + 1
Sat(n) == IF n >= Cap THEN Cap ELSE n
NoRead == [r \in Rows |-> TRUE]

TypeOK ==
    /\ session \in {"previewing", "committed", "gone"}
    /\ done \in [Jobs -> BOOLEAN]
    /\ token \in [Jobs -> Workers \cup {None}]
    /\ leaseLive \in [Jobs -> BOOLEAN]
    /\ attempts \in [Jobs -> 0..Cap]
    /\ geocode \in [Rows -> {"pending", "ok", "failed"}]
    /\ selected \in [Rows -> BOOLEAN]
    /\ pc \in [Workers -> {"idle", "provider", "read", "finish", "writeRows", "release"}]
    /\ job \in [Workers -> Jobs \cup {None}]
    /\ round \in [Workers -> 0..Cap]
    /\ failed \in [Workers -> BOOLEAN]
    /\ seenSelected \in [Workers -> [Rows -> BOOLEAN]]
    /\ crashes \in 0..MaxCrashes
    /\ choice \in [Rows -> BOOLEAN]
    /\ rounds \in [Jobs -> 0..Cap]
    /\ finishes \in [Jobs -> 0..2]
    /\ committedPending \in BOOLEAN

\* ApplyMapping stages rows and jobs and moves the session to previewing in
\* one Transact: every job open, every row pending and selected by default.
Init ==
    /\ session = "previewing"
    /\ done = [j \in Jobs |-> FALSE]
    /\ token = [j \in Jobs |-> None]
    /\ leaseLive = [j \in Jobs |-> FALSE]
    /\ attempts = [j \in Jobs |-> 0]
    /\ geocode = [r \in Rows |-> "pending"]
    /\ selected = [r \in Rows |-> TRUE]
    /\ pc = [w \in Workers |-> "idle"]
    /\ job = [w \in Workers |-> None]
    /\ round = [w \in Workers |-> 0]
    /\ failed = [w \in Workers |-> FALSE]
    /\ seenSelected = [w \in Workers |-> NoRead]
    /\ crashes = 0
    /\ choice = [r \in Rows |-> TRUE]
    /\ rounds = [j \in Jobs |-> 0]
    /\ finishes = [j \in Jobs |-> 0]
    /\ committedPending = FALSE

Open(j) == ~GuardDoneJobsAreClosed \/ ~done[j]
Live == session = "previewing"

\* Leave the worker's run: back to the durableWorker loop with no job. The
\* run's locals are reset so idle workers do not multiply states.
Idle(w) ==
    /\ pc' = [pc EXCEPT ![w] = "idle"]
    /\ job' = [job EXCEPT ![w] = None]
    /\ round' = [round EXCEPT ![w] = 0]
    /\ failed' = [failed EXCEPT ![w] = FALSE]
    /\ seenSelected' = [seenSelected EXCEPT ![w] = NoRead]

Goto(w, step) == pc' = [pc EXCEPT ![w] = step]

-----------------------------------------------------------------------------
(* Worker steps: durableWorker and processJob.                             *)

\* importJobRepository.Claim: one UPDATE over a job picked FOR UPDATE SKIP
\* LOCKED, whose lease is NULL or past, in a session that is neither consumed
\* nor expired. It returns the new attempts value as job.Attempts.
\* processJob calls the geocoder only while job.Attempts <= maxGeocodeRounds;
\* past that it finishes the job as failed without a provider call.
Claim(w, j) ==
    LET r == Sat(attempts[j] + 1) IN
    /\ pc[w] = "idle"
    /\ Live
    /\ Open(j)
    /\ ~leaseLive[j]
    /\ token' = [token EXCEPT ![j] = w]
    /\ leaseLive' = [leaseLive EXCEPT ![j] = TRUE]
    /\ attempts' = IF GuardClaimReservesAttempt THEN [attempts EXCEPT ![j] = r]
                                                ELSE attempts
    /\ round' = [round EXCEPT ![w] = r]
    /\ job' = [job EXCEPT ![w] = j]
    /\ IF r <= MaxGeocodeRounds
       THEN /\ Goto(w, "provider")
            /\ rounds' = [rounds EXCEPT ![j] = Sat(@ + 1)]
            /\ failed' = [failed EXCEPT ![w] = FALSE]
       ELSE /\ Goto(w, "read")
            /\ failed' = [failed EXCEPT ![w] = TRUE]
            /\ UNCHANGED rounds
    /\ UNCHANGED <<session, done, geocode, selected, crashes, seenSelected,
                   choice, finishes, committedPending>>

\* GeocodeWithRetry returns. processJob then:
\*   ctx cancelled (shutdown)                -> return; deferred Release runs
\*   job.Attempts < max and the round timed
\*     out or failed retryably               -> return; deferred Release runs
\*   otherwise                               -> persist, ok or failed
ProviderReturns(w) ==
    /\ pc[w] = "provider"
    /\ \E outcome \in {"ok", "permanent", "retryable", "timeout", "shutdown"}:
        IF \/ outcome = "shutdown"
           \/ round[w] < MaxGeocodeRounds /\ outcome \in {"retryable", "timeout"}
        THEN /\ Goto(w, "release")
             /\ UNCHANGED failed
        ELSE /\ Goto(w, "read")
             /\ failed' = [failed EXCEPT ![w] = (outcome # "ok")]
    /\ UNCHANGED <<session, done, token, leaseLive, attempts, geocode, selected,
                   crashes, job, round, seenSelected, ghostVars>>

\* The process dies inside the provider call: a panic on a poison address,
\* an OOM, a deploy that kills the container. No deferred Release runs.
\* Unbounded: the retry budget, not luck, must end this loop.
ProviderCrashes(w) ==
    /\ pc[w] = "provider"
    /\ Idle(w)
    /\ UNCHANGED <<session, done, token, leaseLive, attempts, geocode, selected,
                   crashes, ghostVars>>

\* RowsByIndices, outside any transaction and without a lock. Missing rows
\* (session committed or deleted) -> ErrNotFound; the deferred Release runs.
ReadRows(w) ==
    /\ pc[w] = "read"
    /\ IF Live
       THEN /\ Goto(w, "finish")
            /\ seenSelected' = [seenSelected EXCEPT ![w] =
                   [r \in Rows |-> IF r \in JobRows[job[w]] THEN selected[r] ELSE TRUE]]
       ELSE /\ Goto(w, "release")
            /\ UNCHANGED seenSelected
    /\ UNCHANGED <<session, done, token, leaseLive, attempts, geocode, selected,
                   crashes, job, round, failed, ghostVars>>

\* The import_rows write of a successful Finish.
WriteResult(w, j) ==
    /\ geocode' = [r \in Rows |->
                     IF r \in JobRows[j] THEN IF failed[w] THEN "failed" ELSE "ok"
                     ELSE geocode[r]]
    /\ selected' = [r \in Rows |->
                      IF r \notin JobRows[j] THEN selected[r]
                      ELSE IF failed[w] THEN FALSE
                      ELSE IF GuardFinishKeepsLiveSelection THEN selected[r]
                      ELSE seenSelected[w][r]]

\* importJobRepository.Finish: one transaction that locks the session row
\* (FOR UPDATE, not consumed, not expired), then
\*   UPDATE import_jobs SET done=true, claim_token=NULL, claimed_until=backoff
\*   WHERE claim_token=$3 AND NOT done AND claimed_until>clock_timestamp()
\* and, when that matched, writes every row of the job. A failed geocode
\* deselects the rows. Zero jobs matched -> ErrWorkflowConflict, and the
\* deferred Release still runs.
Finish(w) ==
    LET j == job[w] IN
    /\ pc[w] = "finish"
    /\ IF Live /\ token[j] = w /\ Open(j) /\ leaseLive[j]
       THEN /\ done' = [done EXCEPT ![j] = TRUE]
            /\ token' = [token EXCEPT ![j] = None]
            /\ leaseLive' = [leaseLive EXCEPT ![j] = TRUE]
            /\ finishes' = [finishes EXCEPT ![j] = IF @ < 2 THEN @ + 1 ELSE 2]
            /\ IF GuardFinishIsOneTransaction
               THEN /\ WriteResult(w, j)
                    /\ Goto(w, "release")
               ELSE /\ UNCHANGED <<geocode, selected>>
                    /\ Goto(w, "writeRows")
       ELSE /\ Goto(w, "release")
            /\ UNCHANGED <<done, token, leaseLive, finishes, geocode, selected>>
    /\ UNCHANGED <<session, attempts, crashes, job, round, failed, seenSelected,
                   choice, rounds, committedPending>>

\* Mutation only (GuardFinishIsOneTransaction = FALSE): the separate row
\* write after the job was marked done.
WriteRows(w) ==
    /\ pc[w] = "writeRows"
    /\ IF Live THEN WriteResult(w, job[w]) ELSE UNCHANGED <<geocode, selected>>
    /\ Goto(w, "release")
    /\ UNCHANGED <<session, done, token, leaseLive, attempts, crashes, job, round,
                   failed, seenSelected, ghostVars>>

\* importJobRepository.Release, deferred by processJob on every return:
\*   UPDATE import_jobs SET claim_token=NULL, claimed_until=backoff
\*   WHERE claim_token=$3 AND NOT done
\* A no-op when another worker took the claim over, or the job is done or
\* deleted. Mutation GuardClaimReservesAttempt = FALSE counts the round here.
Release(w) ==
    LET j == job[w] IN
    /\ pc[w] = "release"
    /\ IF Live /\ (~GuardReleaseChecksToken \/ token[j] = w) /\ Open(j)
       THEN /\ token' = [token EXCEPT ![j] = None]
            /\ leaseLive' = [leaseLive EXCEPT ![j] = TRUE]
            /\ attempts' = IF GuardClaimReservesAttempt THEN attempts
                           ELSE [attempts EXCEPT ![j] = Sat(@ + 1)]
       ELSE UNCHANGED <<token, leaseLive, attempts>>
    /\ Idle(w)
    /\ UNCHANGED <<session, done, geocode, selected, crashes, ghostVars>>

\* The process dies outside the provider call (bounded by MaxCrashes).
Crash(w) ==
    /\ pc[w] \in {"read", "finish", "writeRows", "release"}
    /\ crashes < MaxCrashes
    /\ crashes' = crashes + 1
    /\ Idle(w)
    /\ UNCHANGED <<session, done, token, leaseLive, attempts, geocode, selected,
                   ghostVars>>

-----------------------------------------------------------------------------
(* Time and the database clock.                                            *)

HolderWorking(j) ==
    /\ token[j] # None
    /\ job[token[j]] = j
    /\ pc[token[j]] # "idle"

\* claimed_until passes while nobody works the job: a Release or Finish
\* backoff ends, or a crashed holder's 60s lease runs out.
LeaseExpires(j) ==
    /\ leaseLive[j]
    /\ ~HolderWorking(j)
    /\ leaseLive' = [leaseLive EXCEPT ![j] = FALSE]
    /\ UNCHANGED <<session, done, token, attempts, geocode, selected, crashes,
                   workerVars, ghostVars>>

\* The holder overruns its lease inside the provider call (GC pause,
\* suspended VM, a provider call that ignores its deadline).
ProviderStalls(w) ==
    LET j == job[w] IN
    /\ pc[w] = "provider"
    /\ token[j] = w
    /\ leaseLive[j]
    /\ leaseLive' = [leaseLive EXCEPT ![j] = FALSE]
    /\ UNCHANGED <<session, done, token, attempts, geocode, selected, crashes,
                   workerVars, ghostVars>>

-----------------------------------------------------------------------------
(* The person importing, and session expiry.                               *)

\* SelectRowsContext / the review page's selection patch: a Transact that
\* rewrites import_rows.selected while the session is previewing.
Select(r, value) ==
    /\ Live
    /\ selected' = [selected EXCEPT ![r] = value]
    /\ choice' = [choice EXCEPT ![r] = value]
    /\ UNCHANGED <<session, done, token, leaseLive, attempts, geocode, crashes,
                   workerVars, rounds, finishes, committedPending>>

\* commitPersistent inside Transact: the session row lock serializes it with
\* Finish. It rejects a consumed or non-previewing session (two separate
\* checks), returns ErrGeocodingInProgress while a row needs geocoding,
\* creates the roster rows, clears import rows and jobs, and marks the
\* session consumed.
Commit ==
    /\ Live
    /\ ~GuardCommitChecksPending \/ \A r \in Rows: geocode[r] # "pending"
    /\ committedPending' = \E r \in Rows: geocode[r] = "pending"
    /\ session' = "committed"
    /\ UNCHANGED <<done, token, leaseLive, attempts, geocode, selected, crashes,
                   workerVars, choice, rounds, finishes>>

\* CancelContext (Delete WHERE NOT consumed, cascading to rows and jobs), or
\* the 8h sliding TTL running out.
Abandon ==
    /\ Live
    /\ session' = "gone"
    /\ UNCHANGED <<done, token, leaseLive, attempts, geocode, selected, crashes,
                   workerVars, ghostVars>>

-----------------------------------------------------------------------------

WorkerStep(w) ==
    \/ \E j \in Jobs: Claim(w, j)
    \/ ProviderReturns(w)
    \/ ProviderCrashes(w)
    \/ ReadRows(w)
    \/ Finish(w)
    \/ WriteRows(w)
    \/ Release(w)

Next ==
    \/ \E w \in Workers: WorkerStep(w) \/ Crash(w) \/ ProviderStalls(w)
    \/ \E j \in Jobs: LeaseExpires(j)
    \/ \E r \in SelectableRows, value \in BOOLEAN: Select(r, value)
    \/ Commit
    \/ Abandon

\* ASSUMPTIONS: every worker keeps taking steps (the 250ms poll and the wake
\* channel), time keeps passing, and the throttle reopens. A crash is a
\* restart, so some worker always runs. Crashes, stalls, selections, commit
\* and abandonment get no fairness: they may happen or not, forever.
Fairness ==
    /\ \A w \in Workers: WF_vars(WorkerStep(w))
    /\ \A j \in Jobs: WF_vars(LeaseExpires(j))

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
(* Properties.                                                             *)

\* A geocoding result lands on a job at most once, so a stale worker can
\* never overwrite a resolved address (flip a match to an error and
\* deselect the rows).
AtMostOneResultPerJob == \A j \in Jobs: finishes[j] <= 1

\* A poison address costs at most maxGeocodeRounds worker rounds, however
\* often workers crash, stall or are redeployed mid-call. Each round is one
\* GeocodeWithRetry, which may itself retry; paid-quota limits live in the
\* Google usage ledger, which this model leaves out.
ProviderRoundsBounded == \A j \in Jobs: rounds[j] <= MaxGeocodeRounds

\* A geocoded row keeps the person's latest selection; geocoding never
\* re-selects a row they excluded (createBatch would import it) or drops one
\* they picked. Only a failed geocode deselects, on purpose.
GeocodingKeepsSelection ==
    \A r \in Rows: geocode[r] = "ok" /\ Live => selected[r] = choice[r]

\* The import creates roster rows only after every row is geocoded or failed.
CommitOnlyWhenResolved == ~committedPending

\* While the import stays open, every row eventually finishes geocoding, so
\* the person can commit. Holds under unbounded provider crashes and stalls.
\* Cancelling or expiring the session is never forced, so TLC checks every
\* behavior in which the session stays open forever.
ImportEventuallyCommittable ==
    <>(~Live \/ \A r \in Rows: geocode[r] # "pending")

=============================================================================
