# Shared roster edit preparation

## Context

Participant and driver creation and updates repeat coordinate preparation and persistence orchestration across desktop forms, JSON requests and mobile forms. Each caller must geocode before writing, retain coordinates when the address is unchanged, and choose the atomic writer that matches label replacement intent.

Validation and lookup order differ across these adapters. Desktop update handlers load the existing record before decoding the body, so not-found wins over a malformed request. Mobile validates before lookup. Desktop validates driver capacity before label existence; mobile checks labels first. Combining those steps would change the error shown for requests with several problems.

## Decision

A private roster editor in handlers exposes typed participant and driver create/update operations. Adapters pass already validated and normalized fields, explicit label replacement intent, and the existing record for updates. The editor constructs a fresh write model, carries coordinates and creation time forward, and geocodes only when the exact address string changes. Creates always geocode. Geocoding receives the original request context and the existing retry count of three.

The editor calls the existing atomic CreateWithLabels, Update or UpdateWithLabels operations. It does not split entity and membership persistence. A typed geocoding error preserves the underlying error for adapter-specific presentation; persistence errors pass through unchanged.

Decoding, trimming, validation, lookup timing, logging and HTTP response mapping stay in the adapters. Import batches retain their separate persistence semantics. The shared mobile geocoding helper remains available for Places.

## Consequences

Desktop and mobile callers no longer coordinate model construction, conditional geocoding or write selection. Deliberate validation duplication remains. Mobile still trims names and addresses; desktop retains its existing normalization. Omitted JSON labels retain memberships, explicit empty labels clear them, and forms replace them.

Carrying creation time into mobile update models is inert: the database does not update that column, and mobile discards the returned entity. Desktop JSON continues returning the retained timestamp. Existing provider-error exposure also remains different: JSON and desktop forms expose the provider message; mobile shows its generic lookup failure.

Tests exercise the editor with the real Postgres repositories and a controlled geocoder, plus HTTP regressions for adapter differences. Failed geocoding, cancellation and membership writes leave the persisted record and labels unchanged.
