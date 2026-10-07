# Selected-driver van assignment resolution

## Context

The driver picker, van assignment restoration and route calculation each collected assigned van IDs, loaded current vans and reconstructed metadata. They deliberately handle a missing van differently. Picker and restoration render the driver's personal vehicle; calculation rejects the plan before refreshing coordinates or routing.

## Decision

The existing handlers van-assignment module owns a typed lookup using the existing organization vehicle repository. It deduplicates assigned van IDs, loads only those vans, and returns current vans indexed by driver together with whether every assignment was found. Database errors pass through unchanged. Empty assignments return an empty map without querying.

Callers retain parsing, uniqueness validation, driver loading, response presentation and missing-van policy. The calculation caller maps incomplete resolution to the existing selected-van error. Picker and restoration accept partial resolution. The loader retains all assignment driver keys with current vans, including keys whose drivers may be absent from the current roster.

Effective-driver preparation uses that driver-indexed result, copies only loaded drivers and returns their van metadata. Calculation performs this preparation after coordinate refresh, preserving refreshed coordinates. It gives the router and session the effective capacities, while shortage presentation retains original drivers and assignments. Existing route metadata preparation sets van ID, name and effective capacity; personal drivers retain their own capacity.

The loader does not enforce uniqueness. Form validation already does, with its existing error order. Direct calculation retains its existing shared-van relation semantics: several drivers can resolve to the same current van, while the vans-used summary counts unique occupied vans.

## Consequences

Current-van lookup and driver reconstruction have one implementation shared by the three active consumers. A generic resolver, repository adapter or strict wrapper would add policy and interfaces without another consumer, so none is introduced. Retired mobile handlers keep their separate loaders outside this change.

Request and calculation tests use isolated Postgres and the existing production seams. They cover chosen and hidden selected vans, missing/deleted tolerance and rejection, lookup failure and cancellation, effective capacities, refreshed coordinates, route/session metadata and shortage preservation. No routing algorithms, browser selection behavior, validation precedence, schema or production configuration changes.
