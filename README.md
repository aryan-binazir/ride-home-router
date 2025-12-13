# Ride Home Router

Local-first route optimizer for assigning drivers to drop off participants after events.

## What it does

Helps you efficiently assign participants to available drivers after events (workshops, meetings, gatherings). The app:

- Calculates optimal routes that minimize total driving distance
- Respects vehicle capacity limits
- Handles an optional shared/institute vehicle for overflow
- Saves event history for reference

Ideal for organizations that regularly coordinate rides home for attendees.

## Privacy

**All data stays on your machine.** Participant names, driver info, and event history are stored in a local SQLite database.

The only external communication is:
- **OSRM** (routing service) — receives plain addresses to calculate driving distances
- **Nominatim** (geocoding) — receives addresses to convert them to coordinates

No accounts, no cloud sync, no tracking.
