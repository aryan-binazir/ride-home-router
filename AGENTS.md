# Architecture

Before changing application code, use the [architecture map](docs/architecture.md) to find the owner, request flow, and focused checks. Use [CONTEXT.md](CONTEXT.md) for domain terms.

# Verification before merging

This repository uses local verification only. Keep GitHub Actions disabled and do not add CI workflows.

Before merging any PR, run `BROWSER_TEST_BINARY=/path/to/chrome-or-chromium make check` locally on the final PR head with `TEST_DATABASE_URL` set to a local test Postgres database. All lint, module checks, the reachable-vulnerability scan, vet, JavaScript tests, browser tests, and Go race tests must pass. `make check-unit` alone is insufficient because it skips database tests.

For planner-related changes, also run `make eval`. Changes covered by `verify-workflow-tla`, or to the verification gates themselves, require `make check-branch` on a clean committed head. It includes `make check`, planner evaluation, TLC and real-code guard mutations. Other changes still require `make check` as the minimum. Record commands and results in the PR. Fix failures before merging; rerun verification after changes.

# Releases

After each PR merges to `main`, run `make release` from an up-to-date checkout. It tags `origin/main` with the next patch version and publishes a GitHub release with notes of the PRs since the last tag. Use `make release LEVEL=minor` or `LEVEL=major` only when Ar asks for it; a `minor` or `major` label on the merged PR does the same automatically. `make release DRY_RUN=1` previews without changing anything. It is a no-op when `main`'s head is already tagged.

# Local agent environment

Use `make dev` for a populated isolated local environment. Requires Linux, Go and Podman; `DEV_RUNTIME=docker` explicitly selects a compatible runtime. Open the printed capability-bearing URL for automatic member login. The printed chooser offers admin, approved member and denied identities; use separate browser contexts for simultaneous accounts.

`make dev-status` prints URLs and logs. `make dev-stop` preserves data. `make dev-reset` recreates only this worktree's owned data. Stop/start after source changes to rebuild. Never share local state, keys or login URLs, and never use broad runtime cleanup commands. Provider responses and travel estimates are synthetic.

See [the local development runbook](docs/runbooks/local-development.md) for state, prerequisites, isolation, auth boundaries and verification. Production auth must remain enabled; local identity/browser tooling belongs only under `tools/dev` and must stay out of production artifacts.
