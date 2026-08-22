# Roadmap

Highest-value next work, in order:

1. Validate the official collector and Fabric auction adapter against a real Donut account in notify-only mode; capture sanitized fixtures for contract tests.
2. Batch PostgreSQL writes with `COPY`, persist observation identities and valuation/snapshot history, and add Testcontainers migration/query-plan tests.
3. Calibrate the existing base fallback, add modifier-family priors, and measure prediction error against eventual sales by signature family and liquidity band.
4. Replace development bearer tokens with expiring signed sessions bound to verified Minecraft UUID ownership, including rotation and revocation.
5. Add incremental snapshot diffs, checksums and a 100k-signature/1,000-WebSocket slow-consumer benchmark before introducing multi-node NATS fanout.

Subsequent work: operator RBAC, chat moderation, retention jobs, shard-aware scheduling learned from actual opportunity outcomes, live UI commands/overlay, and ordinary interaction support behind an explicit safety flag.
