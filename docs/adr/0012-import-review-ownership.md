# Durable import review ownership

## Context

The import panel loaded every durable row to display a page of at most 50 rows. Page selection decoded the entire import, rewrote every selection, and loaded every row again. HTTP callers coordinated row positions, selections, valid counts, and pagination. The existing import-job repository already supported targeted row reads and SQL summaries.

## Decision

The existing durable importer owns review pages. `LoadReviewPage` returns indexed rows with their selections, clamped pagination, metadata, global selected-valid counts, and geocoding progress. It reads the summary and requested payloads inside the existing workflow transaction. The workflow lock excludes commit, cancellation, mapping, and geocoder completion while constructing the page. Successful reads renew sliding expiry and advance the workflow revision through the existing transaction machinery.

`SelectRowsPatch` validates every delta index in the transaction before updating selections. Its SQL UPDATE targets only supplied indices whose selections differ. It returns aggregate progress and counts without decoding row payloads. The panel requests a review page only when it needs row HTML; the HTTP adapter formats importer-owned results.

Aggregate counts may scan import rows in SQL. A row with no errors contributes to the selected count when selected, even while geocoding is pending. Progress stays running while jobs are unfinished or any row still needs geocoding. Invalid selected rows retain their selection but do not contribute to the valid count. Choices outside the delta remain unchanged.

## Consequences

A review page transfers and decodes at most 50 row payloads. A selection delta transfers no row payloads and writes only changed selections within the delta. The workflow header and aggregate SQL work remain. These are source-backed reductions in payload and write work, not measured latency improvements.

Full snapshot operations, legacy full selection, mapping, and commit retain their existing behavior. `CommitRowsPatch` still loads all rows and applies its transient delta atomically with roster writes and token consumption. There is no second storage lifecycle, schema change, or new geocoding policy.

Tests use public durable import operations and HTTP requests with isolated real Postgres. A permitted dependency observer counts actual returned row payloads for a 2,000-row import; a SQL trigger records updated indices. Regression cases cover invalid selected counts, clamping, off-page choices, invalid delta rejection, missing and expired sessions, state errors, and rollback after roster writes but before token consumption.
