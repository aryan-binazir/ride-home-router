# Roster workbook intake

## Context

The upload adapter called `Sheets`, decided whether to show worksheet choices, then called `Parse`. A workbook with one non-empty worksheet was opened and inspected twice. Discovery failures used different upload guidance from errors parsing a chosen worksheet.

## Decision

The existing parsing module owns workbook discovery, worksheet selection and grid parsing. It returns a grid, a typed `WorksheetRequiredError` carrying ordered non-empty worksheet names, or an error. `WorkbookDiscoveryError` marks reading, opening and inspection failures for an unselected workbook. The upload calls `ParseWithChoices` once and projects its errors into the existing HTML and JSON responses.

`Parse` retains its signature, error strings and primary-error precedence. Both entry points return typed discovery and choice errors and use the same parser implementation, with one Excelize workbook open and close per operation. Explicit worksheet selection skips discovery. `Sheets` is removed because the upload was its only production caller; its tests now observe discovery through parsing.

`ParseWithChoices` gives a workbook close failure precedence after successful discovery, as the upload's former discovery close did before choices or grid parsing. `Parse` keeps an existing primary error when closing fails. A single close cannot distinguish a filesystem fault that would have happened only at the former second close. Forced close faults are not covered by an open hook.

## Consequences

JSON still returns 422 `WORKSHEET_REQUIRED` with worksheet details; HTML still returns a 200 chooser. Discovery failures retain spreadsheet guidance, while empty workbooks and chosen-sheet parse failures retain file guidance. CSV behavior, hidden-row handling, formula warnings, raw values and resource limits remain unchanged.

The change adds no intake module, workbook or storage abstraction, test-only open hook, or dependency. The reduction in workbook opening follows from the code path; no latency improvement is claimed. ADR-0007 governs the durable lifecycle, ADR-0012 review pages and ADR-0015 commit preparation.
