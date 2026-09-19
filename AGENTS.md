# Verification before merging

This repository uses local verification only. Keep GitHub Actions disabled and do not add CI workflows.

Before merging any PR, run `BROWSER_TEST_BINARY=/path/to/chrome-or-chromium make check` locally on the final PR head with `TEST_DATABASE_URL` set to a local test Postgres database. All lint, module checks, vet, JavaScript tests, browser tests, and Go race tests must pass. `make check-unit` alone is insufficient because it skips database tests.

For planner-related changes, also run `make eval`. Record the commands and results in the PR. Fix failures before merging; rerun verification after changes.
