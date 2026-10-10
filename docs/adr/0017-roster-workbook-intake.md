# Roster workbook intake

## Context

The upload adapter called `Sheets`, decided whether to show worksheet choices, then called `Parse`. A workbook with one non-empty worksheet was opened and inspected twice. Discovery failures used different upload guidance from errors parsing a chosen worksheet.

`Parse` and the upload also had different close-error precedence. `Parse` kept an existing parse or worksheet-choice error when closing failed. The upload's successful discovery had to close before choices or parsing, so a discovery close failure took precedence over either.

## Decision

The existing parsing module owns workbook discovery, worksheet selection and grid parsing. `ParseWithChoices` returns a grid, a typed `WorksheetRequiredError` carrying ordered non-empty worksheet names, or an error. `WorkbookDiscoveryError` marks failures reading, opening or inspecting an unselected workbook and closing it after successful discovery. It preserves underlying error text and identity. The upload adapter makes one parsing call and projects those errors into its existing HTML and JSON responses.

`Parse` retains its signature, error strings and primary-error precedence. Both entry points use the same parser implementation, with one Excelize workbook open and close per operation. `ParseWithChoices` preserves the upload's discovery close-error precedence, including when grid parsing also fails. Explicit worksheet selection skips discovery and retains chosen-sheet error precedence. `Sheets` remains unchanged, although only tests call it after this change.

## Consequences

JSON still returns 422 `WORKSHEET_REQUIRED` with worksheet details; HTML still returns a 200 chooser. Discovery failures retain spreadsheet guidance, while empty workbooks and chosen-sheet parse failures retain file guidance. CSV behavior, hidden-row handling, formula warnings, raw values and resource limits remain unchanged.

The change adds no intake module, workbook or storage abstraction, test-only open hook, or dependency. The reduction in workbook opening follows from the code path; no latency improvement is claimed. ADR-0007 governs the durable lifecycle, ADR-0012 review pages and ADR-0015 commit preparation.
