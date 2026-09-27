# Local development provider boundary

Status: accepted for the local development tooling.

Agents need useful synthetic data and credential-free browser entry while exercising real authentication and authorization. Production startup deliberately has no fake-provider configuration.

A separately built `tools/dev` runner constructs the real server and an external loopback HTTP provider and browser proxy. Only this runner installs a deny-by-default HTTP transport before constructing clients. Existing Clerk HTTP-client injection and Google's nil-transport clients supply the provider seams. No production constructor or authentication check changes. The proxy substitutes the browser identity integration and passes signed credentials to the ordinary backend gate.

The proxy and backend use different loopback addresses on the same port. This preserves the browser Host and Origin through the backend's existing exact allowlist without introducing a production host-validation exception. Every environment owns its ports, cookie name, labeled container and private state. Lifecycle commands serialize with a lock and verify process start time and container labels before stopping resources.

Fixtures are inserted in one database transaction with a seed marker. Restarts retain edits. Explicit reset removes only the owned database container and volume. Local Google responses are deterministic synthetic data, not travel predictions. The planner implementation is unchanged.

The dev runner is outside production Docker COPY paths and executable dependencies. Process-level HTTP redirection is not kernel-enforced network isolation. Browser/provider emulation is intentionally limited to the APIs the application uses; it does not implement OAuth or test a real Clerk integration.
