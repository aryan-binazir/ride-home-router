# Standalone Route editor execution

The registered `/api/v1/routes/choose` fallback accepts standalone editor forms. It previously built a JSON map, marshaled it, cloned the HTTP request and invoked a sibling JSON handler. The ordinary inline browser editor already sends JSON through the Route session orchestrator described in ADR 0016.

Keep both adapters in the existing handler modules. The form adapter parses its fields and calls private typed move, swap and add execution methods in `route_edit.go`. The JSON adapters decode their existing payloads and call those same methods. Shared execution owns move validation, Store dispatch, error translation and edit logging. Store remains the transition owner from ADR 0008. `writeRouteSession` still owns timing dispatch and HTML or JSON presentation under ADR 0006.

Preserve the form's parsing order and force HTMX only after parsing succeeds. Forms use legacy claimed-source validation and append positioning. JSON batches retain their 64-move limit and relaxed claimed-source behavior. The original request path remains available to presentation: adding a second driver through the JSON add-driver endpoint forces full rendering, while the standalone fallback retains its existing fragment behavior. Form reset remains unsupported.

This introduces no command framework or module. Server registration, browser orchestration, retired mobile handlers, templates and planner algorithms stay unchanged. HTTP characterization covers the original fallback before the refactor, including errors, fragments, timing requests, cancellation and stale-write responses.
