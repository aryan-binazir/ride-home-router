# Durable roster import lifecycle

## Context

Production roster imports stage their mapping, selections, geocoding jobs, and commit state in Postgres. A separate in-memory import store existed only for tests. It repeated the lifecycle and differed from production on session eviction, cancellation, and commit failure. HTTP tests used that store even though they opened Postgres for roster writes.

## Decision

Use the existing durable store as the sole roster import lifecycle. Keep `NewPersistentStore` and its Postgres workflow and import-job repositories. HTTP and module tests exercise the public import operations with isolated Postgres schemas and controlled geocoders. Parsing, mapping, and validation remain pure. A focused worker test drives claimed jobs directly to verify retry budgets and crash rounds without waiting for real leases.

Do not add a storage abstraction for a second lifecycle. Batch import writes stay separate from the roster edit preparation in ADR-0005.

## Consequences

The in-memory session map, eviction and cleanup loop, private locks, and their tests are removed. Durable transaction semantics govern mapping, selection, and commit: a failed transaction rolls back its roster writes and token consumption. Geocoding jobs survive process restarts. Import HTTP tests now need a local migrated Postgres database. The production constructor, HTTP behavior, schema, provider policy, and deployment remain unchanged.
