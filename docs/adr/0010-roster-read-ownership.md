# Roster read ownership

## Context

Roster listing, pages and mutation refreshes loaded participant or driver rows before passing them to view helpers. Those helpers separately read the request search and offset. Callers had to supply correctly filtered rows. Address confirmation loaded every live row, so a filtered refresh could show unrelated rows and paging links based on the wrong result count.

## Decision

The private roster kind in handlers owns request normalization, source selection, repository loading, label enrichment and pagination. The typed `rosterReader` methods accept the request and return the existing participant and driver list views for page and refresh callers. Search is trimmed once per operation, retains its case, and reaches the existing SQL search unchanged.

The exact deleted-list endpoint selects the deleted repository source and paging target together. Deleted lists retain their existing policy: search does not filter rows, but any submitted search remains in paging URLs. Active lists and every mutation refresh use the live source. Offsets retain the existing 50-row window and clamping behavior.

Participant and driver JSON lists use the same request and source loading and enrich the full result with label IDs. Each kind keeps its response shape. JSON lists remain unpaged, `total` counts all matches, and empty result and label-ID arrays remain arrays. HTML reads retain the row, labels, label-ID map order without a new transaction or cache.

Listing, page and refresh adapters choose templates, response status, toasts and error policy. A committed create still returns its created event and a warning with HTTP 204 when roster reading fails. A completed import still acknowledges its commit when refreshing fails. Single-record JSON enrichment, write preparation and validation order from ADR-0005, import lifecycle from ADR-0007, and home planner reads remain separate.

## Consequences

Confirmation refreshes retain the current search and compute paging over its matching rows, including clamping when confirming an address removes the last match on a page. Both kinds use the existing repositories and models through a handler-local interface for their shared lookup, list, delete and restore operations.

Handler tests use isolated Postgres and cover filtered confirmation, active and deleted pages, JSON shape, label enrichment, refresh adapters and committed-create acknowledgment. The reader adds no SQL limits or snapshot guarantee. Existing name-only ordering, SQL wildcard search and browser import search-state behavior remain outside this change.
