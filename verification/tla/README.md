# TLA+ models

TLC models of the durable workflows that several server instances share through Postgres:

- `ImportGeocodeQueue.tla`: the roster-import geocoding queue (claim, lease, retry budget, finish, commit).
- `RouteSessionSave.tla`: concurrent route-session edits and event saves.

Run `make tla`. It needs Java 11 or later. The `verify-workflow-tla` skill (`.agents/skills/verify-workflow-tla/SKILL.md`) holds the properties, the coverage record, the bounds and assumptions, and the sync commit (`6d6112c`). It also explains how to tell a bug from a modelling error, and how to update a model when the code changes on purpose.

`mutations/` holds configs that turn off one guard each and must fail. They prove the passing checks can still catch the bug each guard prevents.
