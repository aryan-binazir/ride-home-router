# Capture route feedback as a best-effort side record

Route feedback is an event-linked analysis record captured only from a live route-session commit, because direct-route and expired-session fallback saves have no trustworthy algorithm proposal. The event remains authoritative: feedback is written after the event succeeds, failures never make the session retryable, and a process failure between the two writes may lose feedback rather than duplicate the event.

The payload uses typed allowlist DTOs instead of serializing or redacting application models, so adding a name, timestamp, or other person field to those models cannot leak it into feedback. Proposed and final routes pair by driver ID rather than slice position; final stop order reflects the route optimizer's result after SME moves, not a raw edit sequence.
