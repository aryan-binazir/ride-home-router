# Standalone Route editor execution

The registered `/api/v1/routes/choose` fallback accepts standalone editor forms. It previously built a JSON map, marshaled it, cloned the HTTP request and invoked a sibling JSON handler. The ordinary inline browser editor already sends JSON through the Route session orchestrator described in ADR 0016.

Keep both adapters in the existing handler modules. The form adapter parses its fields and calls private typed move, swap and add execution methods in `route_edit.go`. The JSON adapters decode their existing payloads and call those same methods. Shared execution owns move validation, Store dispatch, error translation and edit logging. Store remains the transition owner from ADR 0008. `writeRouteSession` still owns timing dispatch and HTML or JSON presentation under ADR 0006.

Preserve the form's parsing order and force HTMX only after its numeric syntax checks succeed. Keep destination narrowing errors in the forced-HTMX stage, matching the old JSON decoder. Convert malformed UTF-8 in the session ID to replacement runes, matching the old JSON marshaler. Forms use legacy claimed-source validation and append positioning. JSON batches retain their 64-move limit and relaxed claimed-source behavior. The original request path remains available to presentation: adding a second driver through the JSON add-driver endpoint forces full rendering, while the standalone fallback retains its existing fragment behavior. Form reset remains unsupported.

Private methods suffice because the existing handlers already own execution; a command framework or new module would add another dispatch layer without hiding more behavior.
