# Security and trust model

## Assets and boundaries

Clients, chat text, screen-derived item data and simulator traffic are untrusted. Official API data is authenticated but still schema-validated. Only the backend computes valuations and assignments. Database credentials, Donut API keys and network tokens stay in environment-backed configuration.

## Implemented controls

- Constant-time comparison for bearer tokens.
- Separate worker/admin bearer credentials. Operator projections and metrics require the admin credential, which the dashboard supplies only from its server-side proxy.
- WebSocket Origin allow-listing and header-based authentication for native clients.
- HTTP request and WebSocket frame size limits.
- Strict JSON field validation and one-value bodies.
- Bounded HTTP per-client rate-window identities and WebSocket total/chat limits.
- Allow-listed chat channels and 500-character messages.
- Bounded priority queues that shed chat before market traffic, report drops, and disconnect unrecoverably slow market consumers.
- PostgreSQL constraints for positive prices, valid quantities and confidence range.
- Safe simulated purchase default and mandatory stale-screen/item/price/seller revalidation.
- Structured logs without secrets.

## Principal threats

Fabricated observations can bias active depth. V1 deduplicates fingerprints and tracks observer counts, but production should weight clients by reputation and quarantine statistically inconsistent observers. A client can reconnect to evade in-memory rate state; production should move identity rate limits to Redis or gateway enforcement. Development bearer tokens are not account identity; production needs short-lived signed tokens, rotation, revocation and verified UUID binding. Chat moderation and audit retention are intentionally minimal.

No live purchase mode should be enabled until server rules, user consent, live UI state handling and operational kill switches are reviewed.
