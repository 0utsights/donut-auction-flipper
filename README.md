# Donut Auction + Order Flipper

A functionality-first market system with three permanent roles:

- **Mineflayer observers** read DonutSMP order menus and submit evidence. They cannot trade.
- **Go backend** reads the official auction API, coordinates observers, retains SQLite evidence, and scores order/auction routes.
- **Fabric client** receives scored candidates, keeps each player's balance and slot usage local, allocates 20 order and 18 auction slots, and provides manual navigation.

The stable API auction-only release remains at branch `codex/auction-only` and tag `auction-only-v1.0.0`. This branch, `codex/auction-orders`, is the research/production path for combined order and auction evidence. No fake rows are generated.

## Local backend

Requirements: Go 1.26. The Donut API key stays in this process.

```powershell
$env:DONUT_API_KEY='your-key'
$env:DN_CLIENT_TOKEN='long-admin-token'
$env:DN_OBSERVER_TOKEN='different-long-observer-token'
$env:DN_FABRIC_TOKEN='different-long-fabric-token'
go run ./cmd/server
```

Open:

- [http://127.0.0.1:8080/](http://127.0.0.1:8080/) — official auction API debug
- [http://127.0.0.1:8080/order-auction-flipper](http://127.0.0.1:8080/order-auction-flipper) — observers, evidence, watches, and combined candidates
- [http://127.0.0.1:8080/healthz](http://127.0.0.1:8080/healthz) — health

Auction transactions remain in `data/history.json.gz`. Orders, fills, observer health, watches, diagnostics, and optimizer evidence use SQLite WAL at `data/market.db`, with daily seven-day backups.

## Mineflayer observers

Mineflayer is pinned to Minecraft 1.21.11. It collects **orders only**; the official API remains authoritative for auctions.

```powershell
cd collector
Copy-Item accounts.example.json accounts.json
Copy-Item order-schemas.example.json order-schemas.json
npm ci
npm run build
npm run auth -- --account observer-1
npm start
```

Configure one entry per Microsoft account in `collector/accounts.json`. Every account has an isolated token cache and authenticated SOCKS5 or HTTP CONNECT proxy. The configured egress IP must match before the account joins. On Linux, use `chmod 600 collector/accounts.json`.

The checked-in schema list is deliberately empty. On the first run, the collector sends `/orders`, captures a sanitized local fixture under `collector/captures/`, reports `schema_hold`, and performs no clicks. Add a verified title/slot/control schema only after reviewing that fixture. The generic parser marks modifier signatures incomplete, so its rows remain `RESEARCH`; a versioned real-menu adapter must prove canonical modifier equivalence before `READY` is possible. Even with a schema, the only permitted controls are pagination, refresh, filter, and search. Purchase, fulfillment, creation, confirmation, cancellation, claim, listing, and inventory-transfer operations are absent from the collector interface.

One manager process launches and restarts every configured observer. The backend leases discovery, focused-watch, verification, and schema-probe tasks over HTTP long polling.

## Fabric client

Requirements: Minecraft 1.21.11, Java 21, Fabric Loader 0.19.2+, and Fabric API. Build with Gradle 9.5+:

```powershell
gradle -p minecraft/client-mod clean test build
```

Copy the remapped JAR from `minecraft/client-mod/build/libs/` to the instance's `mods` directory. A verified shadow build is also checked in at `outputs/donut-market-flips-2.0.0-alpha.1.jar`. Press `N` or run `/dn`.

The generated `config/donut-network.properties` contains:

```properties
backend_url=https://your-backend.example
client_token=your-fabric-token
poll_millis=250
chat_alerts=true
balance=10000000
used_order_slots=0
used_auction_slots=0
diagnostics=true
```

Balance, used slots, reserve, and the final portfolio stay local. Balance messages containing a clear `balance`, `money`, or `cash` label update the local value; the screen also provides a manual adjustment. Position/outcome inference remains disabled in shadow mode until sanitized real transaction-message fixtures exist—the mod does not guess that opening a menu means a trade occurred. The mod applies a dynamic 15–35% reserve and selects the highest risk-adjusted daily-profit batches that fit the player's cash, 20 order slots, 18 auction slots, executable volume, and exposure caps.

Selecting a combined candidate starts a backend focused watch and opens only `/orders` or a validated canonical `/ah <item_id>` search. Every economic action remains manual.

Sanitized diagnostics are enabled by default and can be disabled in `/dn`. They contain only documented state, version, latency, route, decision, and error-code fields—never chat, usernames, credentials, server URLs, NBT, or inventories.

## Remote deployment

Copy `collector/accounts.container.example.json` to `collector/accounts.json`, configure `collector/order-schemas.json`, set the required environment variables, then run:

```powershell
docker compose up -d --build
```

The composition runs the backend, multi-account collector, and Caddy HTTPS termination together. Set `DN_DOMAIN` to the public hostname and point Fabric clients at `https://<DN_DOMAIN>`. Do not expose the backend container directly.

## Configuration

| Variable | Default | Purpose |
|---|---:|---|
| `DONUT_API_KEY` | required | Official auction API; backend only |
| `DN_CLIENT_TOKEN` | empty on loopback | Administrative/debug credential |
| `DN_OBSERVER_TOKEN` | empty on loopback; required remotely | Mineflayer task/observation credential |
| `DN_FABRIC_TOKEN` | empty on loopback; required remotely | Candidate/watch/diagnostic credential |
| `DN_DATABASE_FILE` | `data/market.db` | SQLite order/evidence state |
| `DN_HISTORY_FILE` | `data/history.json.gz` | Bounded auction-sale history |
| `DN_AUCTION_FEE_BPS` | `250` | Auction exit fee assumption |
| `DN_ORDER_FEE_BPS` | `0` | Order exit fee assumption |
| `DN_LISTING_PAGES` | `220` | Broad recent-auction pages |
| `DN_FAST_INTERVAL` | `250ms` | Newest-page API lane interval |

All three tokens must be distinct when configured. The older `DN_MIN_*` settings apply only to the standalone API-auction feed. Combined candidates have no fixed dollar-profit floor; Fabric selects from the resource-constrained portfolio frontier.

## Verification

```powershell
go test ./...
go vet ./...
cd collector
npm ci
npm test
npm audit --omit=dev
gradle -p ../minecraft/client-mod clean test build
```

See [architecture](docs/architecture.md), [API](docs/api.md), and [decisions](DECISIONS.md).
