# Durable import commit preparation

## Context

The HTML import panel loaded and decoded every staged row before committing, solely to recover the row count and Roster kind. The durable commit then loaded and decoded those rows again inside its workflow transaction. The JSON adapter already committed directly.

## Decision

The panel uses the existing metadata and progress read before parsing its form. This preserves sliding expiry and missing or expired session precedence over malformed input without transferring staged row payloads. The row count lets the adapter retain its legacy full-selection parsing and page-index validation rules. The durable commit revalidates the submitted choices against the rows it reads under the workflow lock.

The existing durable commit owner returns a `CommittedImport` containing the committed Roster kind and the existing result counts. The panel uses that kind for its roster refresh. The JSON adapter projects only the existing count fields, and the stored workflow result keeps its existing shape.

Final-choice application, the required full-row read and decode, roster writes, row clearing and token consumption remain together in the existing transaction. HTTP form parsing, error messages and presentation stay in adapters. A successful commit still receives its acknowledgment when refreshing the roster fails.

## Consequences

A panel commit transfers and decodes the staged row payloads once in Go. The preflight adds an aggregate SQL scan over staged row payloads to recover the row count; the former preflight queried geocoding jobs and fetched the rows instead. Reusing the existing summary avoids a new repository operation, while Postgres still parses row payload fields for its counts. The dependency observer establishes this reduction through an actual panel HTTP request for a 2,000-row import; it does not measure latency or memory gains.

Row payload decoding errors now occur inside the commit after valid form parsing.

Legacy full selections, page deltas, off-page choices, invalid selected counts, geocoding rejection, expiry, commit-once and rollback behavior retain their contracts. ADR-0007 governs the durable lifecycle, ADR-0010 governs roster refreshes, and ADR-0012 governs bounded review pages. This change adds no orchestration module, storage abstraction, schema or geocoding policy.
