# Donut Auction + Order Flipper

[![CI](https://github.com/0utsights/donut-auction-flipper/actions/workflows/ci.yml/badge.svg)](https://github.com/0utsights/donut-auction-flipper/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A functionality-first market system with three permanent roles:

- **Mineflayer observers** read DonutSMP order menus and submit evidence. They cannot trade.
- **Go backend** reads the official auction API, coordinates observers, retains SQLite evidence, and scores order/auction routes.
- **Fabric client** receives scored candidates, keeps each player's balance and slot usage local, allocates 20 order and 18 auction slots, creates explicitly authorized orders, and can claim/package/list completed tracked orders through verified server screens.

This is an unofficial research and client-automation project. Use it only where the server operator and account owner permit it. Mineflayer is structurally read-only; Fabric economic actions remain disabled until the player explicitly authorizes the current session and all live checks pass.

The stable API auction-only release remains at branch `codex/auction-only` and tag `auction-only-v1.0.0`. `main` is the combined order and auction research/production path. No fake rows are generated. See [architecture](docs/architecture.md), [API](docs/api.md), [security](SECURITY.md), and [publication scope](docs/publication.md).

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

Auction transactions remain in `data/history.json.gz`. Orders, fills, observer health, watches, diagnostics, and optimizer evidence use SQLite WAL at `data/market.db`. Raw order rows retain 24 hours while durable summaries and confirmed fills remain available for research. Retention runs in small yielding batches so it does not pause live collection. Automatic backups retain one compact snapshot per UTC day for seven days; manually named safety copies are never pruned.

## Mineflayer observers

Mineflayer is pinned to Minecraft 1.21.11. It collects **orders only**; the official API remains authoritative for auctions.

```powershell
cd collector
Copy-Item accounts.example.json accounts.json
npm ci
npm run build
npm run auth -- --account observer-1
npm start
```

Configure one entry per Microsoft account in `collector/accounts.json`. Every account has an isolated token cache and authenticated SOCKS5 or HTTP CONNECT proxy. The configured egress IP must match before the account joins; a mismatch disables that account instead of entering a restart loop. On Linux, use `chmod 600 collector/accounts.json`.

The checked-in schema was verified against the real 1.21.11 Donut order menu. It permits only the exact `Orders` refresh book and `Next page` arrow fingerprints. A changed or unknown layout is captured under `collector/captures/`, reported as `schema_hold`, and never clicked. The parser marks only a conservative allowlist of unmodified base commodities as signature-complete. Enchanted, damaged, named, container-bearing, potion, map, music-disc, tool, armor, and otherwise variant-sensitive rows remain `RESEARCH` and cannot receive a priority rank. Purchase, fulfillment, creation, confirmation, cancellation, claim, listing, and inventory-transfer operations are absent from the collector interface.

The collector paces server-confirmed discovery clicks at 750ms. Focused watches use a separate 500ms minimum lane plus the backend's requested freshness delay. Discovery continuously refreshes page one after proving the global `Most Per Item` sort; the official auction API prioritizes canonical items for exact `/order <item_id>` focused searches. Every filtered result must prove page one, canonical identity, completeness, and descending reward. The collector does not crawl arbitrary deep pages in the normal path.

Navigation accepts a page only after the server sends a matching window update; Mineflayer's optimistic local click state is never submitted as a new page. After login transfers settle, the stationary observer also suspends Mineflayer's idle movement ticks. This avoids Donut's 1.21.x `Invalid sequence` disconnect while preserving keepalives, teleport handling, chat, and window packets. Transient disconnects renew the same leased task and retry; only a schema hold permanently stops discovery.

One manager process launches and restarts every configured observer. The backend leases discovery, focused-watch, verification, and schema-probe tasks over HTTP long polling.

The order-auction dashboard ranks trustworthy order-to-auction research and ready candidates first. It prefers the item's exact full-stack resale cohort, scores conservative profit per day across attainable batches, and exposes the evidence, liquidity, price stability, and focused-watch controls behind every rank. Historical fill guesses from long observation gaps are retained for audit but quarantined from scoring.

## Fabric client

Requirements: Minecraft 1.21.11, Java 21, Fabric Loader 0.19.2+, and Fabric API. Build with Gradle 9.5+:

```powershell
gradle -p minecraft/client-mod clean test build
```

Copy the remapped JAR from `minecraft/client-mod/build/libs/` to the instance's `mods` directory. The verified build is checked in under `outputs/`. Press `N` or run `/dn`.

The generated `config/donut-network.properties` contains:

```properties
backend_url=https://your-backend.example
client_token=your-fabric-token
poll_millis=250
chat_alerts=true
balance=0
used_order_slots=0
used_auction_slots=0
diagnostics=true
order_server_hosts=play.donutsmp.net,donutsmp.net,java.donutsmp.net
```

Balance, used slots, reserve, and the final portfolio stay local. Once per second Fabric follows the same team-colored sidebar objective as Minecraft's HUD and reads Donut's visible balance, including compact values such as `$119M` and `$2.5M`; clearly labeled balance chat remains a fallback, and the screen provides a manual adjustment when no sidebar is available. The backend never applies a reference balance or returns a balance-sized portfolio. Position/outcome inference remains disabled in shadow mode until sanitized real transaction-message fixtures exist—the mod does not guess that opening a menu means a trade occurred. The mod applies a dynamic 15–35% reserve, fills as many distinct profitable offer slots as the local balance permits (up to 20), then sizes each order within measured volume and a 25% per-item exposure cap. A large order exits through sequential exact-stack listings of at most 64 units that reuse the 18 auction slots; future exit batches do not falsely consume simultaneous slots during acquisition planning.

The `/dn` screen is a compact blue/black order console: live balance, deployable cash, slot use, portfolio economics, and current orders are visible without tooltips. Adjustment and diagnostic controls live under `Local State`. `Review` retains one-order consent. `Auto Flip` authorizes one continuous in-memory order-to-auction session: it can start empty, automatically enables guarded auction exits, waits for later READY allocations, and keeps processing distinct items until the player stops it. Each queued allocation freezes its own maximum escrow. Fabric performs a fresh focused check, creates one order per canonical item, proves `Most Per Item` rank, and reprices only within the frozen profitable cap. A verified cancellation restores the already-known pre-submit local balance even when Donut's compact sidebar rounds away the refund. Transient feed, balance, and connection gaps wait and retry. Candidate-specific failures quarantine that item; uncertain post-submit or exit outcomes keep the affected durable item locked or in `HOLD` while unrelated items continue.

`Exits` opens the durable position view; it can still be authorized independently. The continuous session refreshes tracked fill progress from `Your Orders`, accepts either one-batch or full-order `Collect` delivery, obtains a fresh official-API exact-quantity exit quote before every listing, and reconciles `/ah -> Your Items` whenever all 18 local slots appear occupied. Positions occupying 27 or more physical inventory slots use exact 27-stack shulker packages; smaller positions retain the API-proven unchanged exit quantity. Unknown or irreversible outcomes hold only that item for manual reconciliation. Modified items remain manual.

Empty-box quotes use a separate backend search every five seconds rather than a cached broad-scan row. Both server and mod reject a quote older than 20 seconds. If the official API is unavailable, automatic exits wait; the age check is not relaxed.

Orders created before alpha28 have only a duplicate-prevention lock, not frozen acquisition/exit economics, so they are shown as legacy locks and must be claimed/listed manually. The client deliberately does not invent a resale target while importing old state.

Sanitized diagnostics are enabled by default and can be disabled in `/dn`. They contain only documented state, version, latency, route, decision, and error-code fields—never chat, usernames, credentials, server URLs, NBT, or inventories.

## Remote deployment

Copy `collector/accounts.container.example.json` to `collector/accounts.json`, configure `collector/order-schemas.json`, set the required environment variables, then run:

```powershell
docker compose up -d --build
```

The composition runs the backend, multi-account collector, and Caddy HTTPS termination together. Set `DN_DOMAIN` to the public hostname and point Fabric clients at `https://<DN_DOMAIN>`. Do not expose the backend container directly. Collectors reject non-loopback HTTP backend URLs, and all collector backend calls have bounded timeouts.

For the trusted second-PC deployment where Tailscale HTTPS certificates are not enabled, run only the backend and collector with the loopback-only override:

```bash
./scripts/build-second-pc.sh
docker compose -f compose.yaml -f compose.second-pc.yaml up -d auction-server order-collector
```

For a collector-only or backend-only update, pass `collector` or `backend` to the build script. The script keeps dependency caches under the ignored `.second-pc-cache/` directory, so repeated validated deployments do not redownload the complete Go and npm dependency sets.

From the player PC, run `scripts/install-second-pc-tunnel-task.ps1 -RemoteHost <host> -RemoteUser <user> -IdentityFile <private-key>` once. It installs a hidden per-user task that starts at logon, is watchdog-triggered every five minutes if it ever exits, supervises `scripts/second-pc-tunnel.ps1`, checks the real `/healthz` response, and reconnects with bounded backoff whenever the SSH forward or second PC is temporarily unavailable. Overlapping watchdog starts are ignored while the task is already running. The existing Fabric and browser URL remains `http://127.0.0.1:8080`; SSH encrypts and authenticates the hop, and the backend is never bound to LAN. Remove it with `Unregister-ScheduledTask -TaskName DonutMarketBackendTunnel -Confirm:$false`. Enabling tailnet HTTPS later allows replacing this tunnel with the normal Caddy deployment.

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
| `DN_SHULKER_INTERVAL` | `5s` | Targeted lowest-price empty-shulker refresh interval |

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

See [architecture](docs/architecture.md), [API](docs/api.md), [decisions](DECISIONS.md), and the complete [progress/review log](PROGRESS.md).
