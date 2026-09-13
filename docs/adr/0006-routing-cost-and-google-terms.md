# Routing cost and Google Maps Platform terms

Status: decided 2026-09-13; implemented in the estimate-then-measure routing change (see README "How it runs"). Deferred to follow-ups: purging metrics from pre-existing saved events, dropping the distance cache table and the legacy matrix engine, timings on history pages, and a replay harness comparing old and new plans.

## Context

Route calculation asks Google Routes for a full distance matrix: every rider to every other rider plus every rider to every driver, about N(N−1) + N·D elements. At 80 riders and 20 drivers that is roughly 8,000 billable elements per calculation; at 500 riders and 100 drivers roughly 300,000. Results are cached in Postgres indefinitely so repeat calculations are free.

Google's Maps Platform Terms forbid caching Google Maps Content except where the Service Specific Terms expressly allow it (Terms §3.2.3(b)). Those terms allow Geocoding latitude and longitude for 30 consecutive days (§6.3.1) and only latitude and longitude from the Routes API for 30 days (§19.3); distances and durations have no caching permission at any duration. The Terms also forbid creating derived content from Google results (§3.2.3(c)). The permanent distance cache is therefore outside the terms, and a 30-day cache (tried in PR #96, reverted in PR #99) is too.

The operator is a nonprofit with a budget of a few dollars a month, so the design must live inside Google's per-SKU monthly free allowances: 10,000 Compute Routes requests, 10,000 Route Matrix elements, 10,000 Geocoding requests, 10,000 Autocomplete requests (Essentials tier, prices verified on 2026-09-13). Google Route Optimization is not budget-compatible (1,000 free shipments, then $30 per 1,000).

## Decision

Replace the distance matrix with "estimate, then measure once":

1. Keep the balanced router as is: household grouping, capacity, seeds, local search, and the lexicographic comparator (every selected driver is used, then corridor spread, latest completion, maximum detour, aggregates). The local search also exchanges whole cars between two drivers, so a driver who lives near another car's riders takes that car. Feed it a fixed straight-line estimator instead of Google distances: `metres = haversine × 1.3`, `seconds = metres ÷ 11.18`. The constants are source constants and are never fitted to Google output.
2. Measure each occupied car's ordered route once with Compute Routes (TRAFFIC_UNAWARE, fixed order, Essentials fields, at most 10 intermediate waypoints per request, chunked at household stops for larger vans) plus one direct baseline per driver for detour display. That is at most 2·D + floor(N/11) requests per calculation: 47 at 80/20, 245 at 500/100, and zero matrix elements.
3. Edits re-measure only the changed cars. Reopening a live session shows the itinerary without times and offers a per-car "Show timings" action. Saved events show the itinerary only; timings on history pages are a possible follow-up.
4. Persist only the itinerary: assignments, stop order, addresses, Maps links, user-entered times. No Google durations, distances or ETAs in sessions, saved events, handoff text or logs. Show Google Maps attribution with measured results. Refresh coordinates before 30 days.
5. Enforce an application ceiling with an atomic Postgres reservation ledger per SKU: reserve before dispatch, count every attempt, fail closed at 8,000 Compute Routes requests a month.
6. Compare plan quality against the matrix planner with a replay of saved events (kept selectable as `ROUTING_ENGINE=matrix` for that purpose). The replay harness is a follow-up; the estimator ships first because the cost and terms problems are immediate.

Estimated monthly usage: about 2,800 requests at 80/20 and 6,000 at 500/100, both free.

## Alternatives considered

- Adaptive nearest-neighbour pruning of the matrix (about 25·N + D elements per calculation): roughly $120 a month today and $970 at scale. Rejected on cost.
- Capacity-aware clustering with cross-cluster repair: similar cost, higher complexity. Rejected.
- Google Route Optimization (Fleet Routing): $8 a month today, $210 at scale, and it does not reproduce the balance-first objective. Rejected.
- Iterative measure-and-repair (up to 100 extra requests per calculation): affordable but adds an engine that cannot guarantee quality either. Deferred; a single bounded repair batch may follow if replay shows isolated failures.
- A self-hosted road graph built from US Census TIGER/Line roads (public domain, not OpenStreetMap) as the estimator: real road distances at zero marginal cost and no provider terms, but a week to prototype and several more to trust, and TIGER carries no speeds or turn restrictions. Deferred; this is the fallback if straight-line estimates fail the replay gate. OpenStreetMap-derived data and services are excluded by operator decision.

## Consequences

Google cost becomes linear in riders and stays inside the free tier at the scales planned. Compliance depends on never persisting measured values, which touches sessions, saved events, handoff text and route feedback. Users lose stored travel times in history; saved events show the itinerary only, and on-demand timings for history pages are a deferred follow-up. Route quality relative to the all-pairs solver is unproven until the replay runs; the estimator cannot see rivers or one-way streets. The ledger month rolls over at midnight Pacific time to match Google's billing calendar.

Consultation record: `_scratch/_reviews/algorithm/r1.out` through `r4.out` (local, not committed); `r4.out` holds the implementation specification.
