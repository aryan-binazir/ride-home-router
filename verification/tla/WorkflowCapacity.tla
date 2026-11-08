-------------------------- MODULE WorkflowCapacity --------------------------
EXTENDS Naturals, FiniteSets
CONSTANTS GuardCapacitySerialization, GuardEvictionIsTransactional,
          GuardEvictionKeepsKind
VARIABLES rows, inactive, pc, owner, observed, victims, evictions, successful
vars == <<rows, inactive, pc, owner, observed, victims, evictions, successful>>
Creates == {3,4,5}
Kinds == {"import", "route"}
Kind(r) == IF r \in {1,3,4,7,8} THEN "import" ELSE "route"
Active(k) == {r \in rows \ inactive : Kind(r) = k}
Init == /\ rows = {1,2,6,7,8} /\ inactive = {7,8}
        /\ pc = [c \in Creates |-> "start"]
        /\ owner = [k \in Kinds |-> 0]
        /\ observed = [c \in Creates |-> 0]
        /\ victims = [c \in Creates |-> {}]
        /\ evictions = {} /\ successful = {}
\* workflowRepository.Create's advisory xact lock is scoped to schema/kind.
Begin(c) == /\ pc[c] = "start"
            /\ (~GuardCapacitySerialization \/ owner[Kind(c)] = 0)
            /\ owner' = [owner EXCEPT ![Kind(c)] = c]
            /\ pc' = [pc EXCEPT ![c] = "count"]
            /\ UNCHANGED <<rows,inactive,observed,victims,evictions,successful>>
Count(c) == /\ pc[c] = "count"
            /\ observed' = [observed EXCEPT ![c] = Cardinality(Active(Kind(c))) ]
            /\ pc' = [pc EXCEPT ![c] = "decide"]
            /\ UNCHANGED <<rows,inactive,owner,victims,evictions,successful>>
\* Separate count and eviction statements. Pending victims stay transaction-local.
Decide(c) == /\ pc[c] = "decide"
             /\ IF observed[c] >= 2 /\ Kind(c) = "import"
                THEN /\ pc' = [pc EXCEPT ![c] = "done"]
                     /\ owner' = [owner EXCEPT ![Kind(c)] = 0]
                     /\ UNCHANGED victims
                ELSE /\ pc' = [pc EXCEPT ![c] =
                          IF GuardEvictionIsTransactional THEN "insert" ELSE "evict"]
                     /\ UNCHANGED owner
                     /\ victims' = [victims EXCEPT ![c] =
                          IF observed[c] < 2 THEN {} ELSE
                          LET candidates == IF GuardEvictionKeepsKind
                                            THEN Active(Kind(c))
                                            ELSE rows \ inactive
                          IN {CHOOSE r \in candidates :
                            \A s \in candidates : r <= s}]
             /\ UNCHANGED <<rows,inactive,observed,evictions,successful>>
Finish(c, ok) == /\ pc[c] = "insert"
                 /\ rows' = IF ok THEN (rows \ victims[c]) \cup {c} ELSE rows
                 /\ evictions' = IF ok THEN evictions \cup ({c} \X victims[c]) ELSE evictions
                 /\ successful' = IF ok THEN successful \cup {c} ELSE successful
                 /\ pc' = [pc EXCEPT ![c] = "done"]
                 /\ owner' = [owner EXCEPT ![Kind(c)] = 0]
                 /\ UNCHANGED <<inactive,observed,victims>>
DeleteOutsideTransaction(c) ==
    /\ pc[c] = "evict"
    /\ rows' = rows \ victims[c]
    /\ evictions' = evictions \cup ({c} \X victims[c])
    /\ pc' = [pc EXCEPT ![c] = "insert"]
    /\ UNCHANGED <<inactive,owner,observed,victims,successful>>
\* Consumption/expiry of the initial import frees a slot.
Reuse == /\ 1 \notin inactive /\ inactive' = inactive \cup {1}
         /\ UNCHANGED <<rows,pc,owner,observed,victims,evictions,successful>>
Next == (\E c \in Creates : Begin(c) \/ Count(c) \/ Decide(c) \/
                            DeleteOutsideTransaction(c) \/
                            Finish(c, TRUE) \/ Finish(c, FALSE)) \/ Reuse
Spec == Init /\ [][Next]_vars
TypeOK == /\ rows \subseteq 1..8
          /\ inactive \subseteq {1,7,8}
          /\ pc \in [Creates -> {"start","count","decide","evict","insert","done"}]
          /\ owner \in [Kinds -> Creates \cup {0}]
          /\ observed \in [Creates -> 0..3]
          /\ victims \in [Creates -> SUBSET (1..8)]
          /\ evictions \subseteq Creates \X (1..8)
          /\ successful \subseteq Creates
CommittedCapacity == \A k \in Kinds : Cardinality(Active(k)) <= 2
EvictionRequiresCommittedCreate == \A e \in evictions : e[1] \in successful
OnlySameKindEvicted == \A e \in evictions : Kind(e[1]) = Kind(e[2])
InactiveRowsPreserved == {7,8} \subseteq rows
=============================================================================
