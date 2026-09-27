# Local development

Run `make dev` from the worktree you want to use. It builds that worktree's source, creates an isolated Postgres container and starts loopback listeners. Open the printed login URL. It signs in an approved synthetic member without credentials. The identity chooser offers `admin@example.test`, `member@example.test` and `denied@example.test`. The denied account authenticates but cannot enter the application.

Prerequisites: Linux, Python 3, Go matching `go.mod`, and rootless Podman. Set `DEV_RUNTIME=docker` explicitly to use a compatible Docker CLI. The selected runtime is recorded for the environment's lifetime. The first run may download Go modules and the Postgres 18 image. No Clerk or Google account, key, or personal data is needed.

| Command | Result |
| --- | --- |
| `make dev` | Start or reuse this worktree's running environment. |
| `make dev-status` | Print status, login URL, identity chooser, logs and cleanup commands. |
| `make dev-stop` | Stop this environment's runner and database; retain data and encryption key. |
| `make dev-reset` | Stop, remove only this environment's labeled database and anonymous volume, then recreate and seed it. This deletes its local edits. |

After editing source, run `make dev-stop && make dev` to rebuild embedded browser assets and Go code. Repeating `make dev` while running does not rebuild or reseed. A failed start stops its owned resources and leaves its database for diagnosis and retry.

State lives under `${XDG_STATE_HOME:-$HOME/.local/state}/ride-home-router/<worktree-path-hash>`, with private permissions. `dev.log` contains application output. The database password, encryption key and login capability are generated there. Do not share that directory or the capability-bearing login URL. Preserve it with the local database. Moving a worktree creates a different environment; stop the old one before moving it. Use the same `XDG_STATE_HOME` for every lifecycle command.

The browser listener binds `127.0.0.1`; the backend binds `127.0.0.2` on the same port so the application's exact Host and Origin checks remain unchanged. The provider and Postgres bind random `127.0.0.1` ports. Port conflicts fail startup; no existing listener is stopped. State changes serialize with a per-worktree lock. Cleanup verifies the exact container's ownership label, and process signaling checks its Linux start time to avoid recycled PIDs.

Browser sessions expire an hour after sign-in unless explicitly renewed. Open the printed login URL again to sign in. Switching identities replaces the session in that browser profile. Use separate browser profiles or isolated browser contexts for simultaneous identities. Different worktrees use different cookie names. Signing keys and live identity sessions are regenerated on restart; database edits and the credential encryption key persist.

The fixtures include 72 riders grouped into households, 56 drivers with small and large capacities, 26 places, 26 vehicles, labels and 24 saved events. Dates and coordinate timestamps are generated at reset. History can be opened, and the planner creates editable route sessions. Imports, address editing and route measurement use deterministic local Google-shaped responses. They are synthetic and must never be used as real travel estimates. The application displays a synthetic-data banner.

Authentication is enabled. The real access gate verifies signed tokens, issuer, authorized party, times, live sessions/users and database approvals. The external browser proxy supplies the local sign-in page and Clerk-compatible browser script. Login and token helpers require an exact Host and Origin and a generated capability. The separately built dev runner redirects the known provider HTTP clients to the local provider and rejects unknown upstream hosts. This is process-level HTTP isolation, not a network namespace. Production constructors, trust checks and environment parsing are unchanged.

`tools/dev` is not copied by the Dockerfile and is not in the `cmd/server` or `cmd/migrate` dependency graph. `make build` continues to build only those production binaries. Existing `serve` and `postgres-*` commands retain their original behavior.

Before merging, use a separate owned test database:

```sh
BROWSER_TEST_BINARY=/usr/bin/chromium TEST_DATABASE_URL='postgres://…/owned_test_database?sslmode=disable' make check
```

Run `make eval` if planner behavior changes. `make check-unit` does not replace the database-backed gate. Dev-provider tests run as part of `make check`; lifecycle and real-browser acceptance evidence is recorded in the PR.
