# Donut Auction + Order Flipper

A functionality-first market system with three permanent roles:

- **Mineflayer observers** read DonutSMP order menus and submit evidence. They cannot trade.
- **Go backend** reads the official auction API, coordinates observers, retains SQLite evidence, and scores order/auction routes.
- **Fabric client** receives scored candidates, keeps each player's balance and slot usage local, allocates 20 order and 18 auction slots, creates explicitly authorized orders, and can claim/package/list completed tracked orders through verified server screens.

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

The collector paces server-confirmed discovery clicks at 750ms. Focused watches use a separate 500ms minimum lane, plus the backend's requested freshness delay, and locate an assigned item only by traversing the verified next-page control. Each discovery connection performs one pass through the server-reported last page, then rotates cleanly instead of reusing stale server state. A 1,000-page runaway guard fails closed if pagination is ever cyclic or malformed. A pass opens `/orders` once and paginates only through verified controls.

Navigation accepts a page only after the server sends a matching window update; Mineflayer's optimistic local click state is never submitted as a new page. After login transfers settle, the stationary observer also suspends Mineflayer's idle movement ticks. This avoids Donut's 1.21.x `Invalid sequence` disconnect while preserving keepalives, teleport handling, chat, and window packets. Transient disconnects renew the same leased task and retry; only a schema hold permanently stops discovery.

One manager process launches and restarts every configured observer. The backend leases discovery, focused-watch, verification, and schema-probe tasks over HTTP long polling.

The order-auction dashboard ranks trustworthy order-to-auction research and ready candidates first. It prefers the item's exact full-stack resale cohort, scores conservative profit per day across attainable batches, and exposes the evidence, liquidity, price stability, and focused-watch controls behind every rank. Historical fill guesses from long observation gaps are retained for audit but quarantined from scoring.

## Fabric client

Requirements: Minecraft 1.21.11, Java 21, Fabric Loader 0.19.2+, and Fabric API. Build with Gradle 9.5+:

```powershell
gradle -p minecraft/client-mod clean test build
```

Copy the remapped JAR from `minecraft/client-mod/build/libs/` to the instance's `mods` directory. The verified build is checked in at `outputs/donut-market-flips-2.1.0-alpha.26.jar`. Press `N` or run `/dn`.

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
order_server_hosts=play.donutsmp.net,donutsmp.net
```

Balance, used slots, reserve, and the final portfolio stay local. Once per second Fabric follows the same team-colored sidebar objective as Minecraft's HUD and reads Donut's visible balance, including compact values such as `$119M` and `$2.5M`; clearly labeled balance chat remains a fallback, and the screen provides a manual adjustment when no sidebar is available. The backend never applies a reference balance or returns a balance-sized portfolio. Position/outcome inference remains disabled in shadow mode until sanitized real transaction-message fixtures exist—the mod does not guess that opening a menu means a trade occurred. The mod applies a dynamic 15–35% reserve, fills as many distinct profitable offer slots as the local balance permits (up to 20), then sizes each order within measured volume and a 25% per-item exposure cap. A large order exits through sequential exact-stack listings of at most 64 units that reuse the 18 auction slots; future exit batches do not falsely consume simultaneous slots during acquisition planning.

The `/dn` screen is a compact blue/black order console: live balance, deployable cash, slot use, portfolio economics, and the four current orders are visible without tooltips. Adjustment and diagnostic controls live under `Local State` instead of competing with the portfolio. Every unavailable action displays its exact blocker; the automatic-consent action remains clickable when blocked so it can explain the missing balance, connection, or slot state. `Review` retains one-order consent. `Auto Orders` opens a second confirmation screen for a continuous in-memory session: it can start with no READY orders, waits for later allocations, and keeps reconciling new distinct items until the player stops it, disconnects, or a safety check fails closed. Each queued allocation freezes its own maximum escrow. Fabric processes one item at a time and performs a new focused recheck. It starts one cent above the observed price bucket, proves the global menu is sorted by `Most Per Item`, searches the exact underscore-delimited item, and requires a unique public rank. If the order is not first, Fabric cancels only an exact unfilled personal order through the verified `Orders -> Edit Order` screen, proves it disappeared, waits for the scoreboard refund, and retries at the consent-time competitive bucket boundary. It drops the item if that higher bid exceeds the reviewed escrow or profit floor. Unknown screens, partial fills, ambiguous identity, missing refunds, or changed economics stop fail-closed. Submitted items and their delivered/claimed/packaged/listed progress are persisted across restarts.

`Exits` opens the durable position view. `Auto Exits` has separate session consent because it can collect inventory, purchase empty generic shulkers, place and pack them, confirm listings, and verify the result in `Your Items`. Positions occupying 27 or more physical inventory slots use exact 27-stack shulker packages; smaller positions retain the API-proven unchanged exit quantity. The live client rechecks generic empty-shulker contents and lowest price before purchase, applies a balance-scaled spend cap, and stops on any ambiguous item, quantity, modifier, screen, receipt, or price. Modified items remain manual. When no completed position is ready, the enabled session simply waits.

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

From the player PC, run `scripts/second-pc-tunnel.ps1`. The existing Fabric and browser URL remains `http://127.0.0.1:8080`; SSH encrypts and authenticates the hop, and the backend is never bound to LAN. Enabling tailnet HTTPS later allows replacing this tunnel with the normal Caddy deployment.

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
