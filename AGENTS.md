# Verification before merging

This repository uses local verification only. Keep GitHub Actions disabled and do not add CI workflows.

Before merging any PR, run `BROWSER_TEST_BINARY=/path/to/chrome-or-chromium make check` locally on the final PR head with `TEST_DATABASE_URL` set to a local test Postgres database. All lint, module checks, vet, JavaScript tests, browser tests, and Go race tests must pass. `make check-unit` alone is insufficient because it skips database tests.

For planner-related changes, also run `make eval`. Record the commands and results in the PR. Fix failures before merging; rerun verification after changes.

# Local agent environment

Use `make dev` for a populated isolated local environment. Requires Linux, Python 3, Go and Podman; `DEV_RUNTIME=docker` explicitly selects a compatible runtime. Open the printed capability-bearing URL for automatic member login. The printed chooser offers admin, approved member and denied identities; use separate browser contexts for simultaneous accounts.

`make dev-status` prints URLs and logs. `make dev-stop` preserves data. `make dev-reset` recreates only this worktree's owned data. Stop/start after source changes to rebuild. Never share local state, keys or login URLs, and never use broad runtime cleanup commands. Provider responses and travel estimates are synthetic.

See [the local development runbook](docs/runbooks/local-development.md) for state, prerequisites, isolation, auth boundaries and verification. Production auth must remain enabled; local identity/browser tooling belongs only under `tools/dev` and must stay out of production artifacts.
