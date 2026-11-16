# Planner restore benchmark

Run against an isolated `make dev` stack. Requires Chromium and `playwright-core` on Node's module path. Set `DEV_READY` to that stack's private `ready.json`; never commit or print its contents.

```sh
export DEV_READY=/absolute/path/to/owned/state/ready.json
export NODE_PATH=/absolute/path/to/node_modules
node scripts/bench-planner-restore/proxy2.js
# In another terminal with the same environment:
PROXY=1 node scripts/bench-planner-restore/restore.js
```

Stop the proxy and `make dev-stop` afterward. The proxy binds 127.0.0.8 on the dev listener's port, applies Brotli q4 and a four-hour static cache, and rewrites local Origin/Host headers. It calls only the dev stack.

The harness selects a location, all fixture participants and drivers, calculates a real synthetic session, then reopens five times in each fresh desktop/mobile context. It uses CDP network events for request starts and completion and animation-frame polling for visible routes. Desktop is 1440x900; mobile is 390x844, DPR 3, 4x CPU, 150 ms network latency, approximately 1.44 Mbps down and 675 kbps up. Each reopen uses the warmed asset cache. Load averages accompany every run. It waits ten seconds after calculation, but session reads can still be slow on a loaded machine; retain all runs.

`PLANNER_RESTORE_SOURCE=/absolute/path/to/old/event-planner.js BROWSER_TEST_BINARY=/path/to/chromium node --test --test-name-pattern='restore golden' web/static/js/plan-selection-browser.test.js` runs the same golden cases against an old source file. Use a CDP dump-dom adapter if Chromium's dump-dom mode hangs locally.
