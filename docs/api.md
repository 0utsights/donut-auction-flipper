# Backend HTTP API

All JSON endpoints reject unknown fields and oversized bodies. Collector, Fabric, and administrator credentials have separate scopes and are stored as one-way hashes in the running backend. Use `Authorization: Bearer <token>`.

## Operational pages

- `GET /` — official auction API health, valuation evidence, and auction-only flips.
- `GET /order-auction-flipper` — the complete market-ranked order-to-auction frontier. It never applies a player balance; Fabric allocates locally.
- `GET /order-auction-flipper/debug` — observer health, scan coverage, disagreements, order evidence, candidate economics, and rejection reasons.
- `GET /healthz` — `200` after a successful official-API scan, otherwise `503`.

## Existing auction feed

`GET /api/v1/flips` is the stable auction-only Fabric feed. It uses the Fabric credential and supports `ETag`/`If-None-Match`. Quantity, pricing, liquidity, and navigation semantics remain documented by the immutable `auction-only-v1.0.0` release.

`GET /api/v1/debug` and `GET /api/v1/debug/valuation?signature=...` expose the corresponding machine-readable audit state and require the administrator credential.

## Collector control API

These endpoints require the observer credential. They can assign observation work but cannot describe a transaction.

### `POST /api/v1/observers/register`

Registers one stable observer and its parser/protocol capabilities.

```json
{
  "observer_id": "orders-east-1",
  "parser_version": "mineflayer-orders-1.0.0",
  "proxy_label": "oracle-us-east",
  "capabilities": ["capture", "pagination", "refresh"]
}
```

### `GET /api/v1/observers/tasks?observer_id=orders-east-1&wait_ms=25000`

Long-polls for one renewable task lease. `wait_ms` is bounded by the server. A response may contain no task. Valid task types are `discovery`, `focused_watch`, `schema_probe`, and `verification`; payloads are limited to observation targets, freshness, cadence, parser schema, and deadline.

### `POST /api/v1/observers/heartbeat`

Reports task/page latency, reconnect count, and health. A heartbeat carrying the current task ID and lease token renews a valid lease. Success returns `204`.

### `POST /api/v1/observers/order-scans`

Submits one menu snapshot. Important fields are:

```json
{
  "observer_id": "orders-east-1",
  "task_id": "...",
  "lease_token": "...",
  "session_id": "login-session-id",
  "schema_version": "orders-v1",
  "page": 1,
  "complete": true,
  "observed_at": "2026-08-23T20:00:00Z",
  "content_hash": "64-character-lowercase-sha256",
  "orders": [{
    "order_key": "stable-menu-identity",
    "signature": "minecraft:diamond|components:...",
    "item_id": "minecraft:diamond",
    "display_name": "Diamond",
    "quantity": 64,
    "max_stack_size": 64,
    "unit_reward_cents": 500000,
    "competitive_unit_reward_cents": 500001,
    "requested_quantity": 640,
    "remaining_quantity": 512,
    "owner": "buyer_name",
    "expires_at": "2026-08-24T20:00:00Z",
    "price_position": 1,
    "slot": 10,
    "raw_field_hash": "64-character-lowercase-sha256",
    "signature_complete": false
  }]
}
```

Submissions are idempotent by observer/session/page/content hash. A missing row is not treated as a fill. Only a later observation of the same order with reduced remaining quantity creates fill evidence.
Order rewards use integer cents so values such as `$0.01` and `$230.10` remain exact. `unit_reward_cents` is the value visible in the order menu. `competitive_unit_reward_cents` is the lowest bid known to cross that complete display bucket: exact prices add one cent, while abbreviated prices move to the next displayed boundary (`$1.3M` becomes `$1.4M`). Boundary-less observations are retained only as diagnostics and cannot enter candidate economics. Candidate capital, proceeds, and profit remain conservative whole-dollar values after quantity multiplication: acquisition costs round up and proceeds round down.

### `POST /api/v1/observers/task-result`

Completes or rejects the current lease and may attach a sanitized schema diagnostic. Unknown layouts must return a capture/hold result rather than navigate.

## Fabric API

These endpoints require the Fabric installation credential.

### `GET /api/v1/candidates`

Returns a bounded, ETag-enabled candidate pool. Every record includes direction, exact item signature and quantity, the visible order reward in `observed_order_unit_reward_cents`, the fully repriced first-place target in `order_unit_reward_cents`, conservative buy/sell economics, fees, completion probability, cycle time, executable volume, order/auction slot use, inventory-slot efficiency, evidence tier, state (`READY`, `HOLD`, `STALE`, or `RESEARCH`), rejection code, and safe manual commands.

The backend does not personalize this feed. Balance, cash reserve, available slots, and final allocation remain in the Fabric installation. Position inference is disabled until real message fixtures are verified.

### `POST /api/v1/watches`

Requests a focused observation for a candidate signature. This never requests a trade.

```json
{"signature":"minecraft:diamond|..."}
```

### `DELETE /api/v1/watches/{id}`

Deletes one watch by ID. Shared watches for the same signature keep the focused task active.

### `POST /api/v1/client/diagnostics`

Accepts a small, rate-limited batch of allowlisted diagnostic events. The backend drops unknown fields and values that resemble secrets or prohibited personal data. Raw chat, usernames, server addresses, inventory contents, NBT, credentials, and upstream API responses are forbidden. Diagnostics expire after 14 days.

## Authentication and deployment

- `DN_CLIENT_TOKEN` protects administrator/debug access.
- `DN_OBSERVER_TOKEN` is only for collectors.
- `DN_FABRIC_TOKEN` is only for distributed Fabric installations.
- `DONUT_API_KEY` remains backend-only.

Loopback HTTP is intended for development. Production clients use authenticated HTTPS through the included Caddy/Compose topology.
