# Donut Auction Flips

An API-first DonutSMP auction flipper with two moving parts:

- a single Go backend that reads the official API, retains bounded sale history, values items, and ranks live listings;
- a thin Fabric 1.21.11 mod that polls the ranked feed, sends clickable chat alerts, and opens a manual `/ah <item>` search.

There is no screen parser, worker network, sharding, WebSocket, PostgreSQL, Node dashboard, telemetry, or automatic purchasing in the normal path. The older full system and parser remain preserved at Git tag `legacy-full-system-v0.4.0` and branch `archive/full-system-v0.4.0`.

## Run locally

Requirements: Go 1.26. The API key stays in the backend process and must never be placed in the mod config.

On Windows, use the local launcher. The first run asks for the API key and stores it using Windows user-scoped encryption; later runs need no setup. Keep its terminal open while playing.

```powershell
.\scripts\start-local.ps1
```

Or start the backend directly with an environment variable:

```powershell
$env:DONUT_API_KEY='your-key'
go run ./cmd/server
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080) for the live debug page. It starts with no fake data, reports collection progress/errors, and shows every flip that passes the thresholds. Health is at `/healthz`; the compact mod feed is `/api/v1/flips`.

The backend has two API lanes. The fast lane refreshes the newest 44 listings about every 0.5–0.8 seconds under load and publishes immediately. A background lane uses the remaining rate-limit budget to refresh completed-sale history and the newest 9,680 rows (220 × 44) for broad valuation and active-market depth. Completed-sale history is retained in `data/history.json.gz`, capped at 100,000 rows and 31 days.

Stacked listings are quantity-safe: the backend requires both quantity-one sales and completed sales at the exact listed quantity, uses the lower per-item quick-sell estimate, then multiplies by the unchanged resale quantity. It will not recommend a stack based on profit that only exists after splitting it.

Liquidity is price-local: the alert gate, confidence, and sell-time estimate count only completed sales from the last 24 hours within ±10% of the proposed resale price, and one seller cannot supply all qualifying volume. The debug page shows near-target volume and distinct sellers beside total item volume so split-price markets are visible instead of being blended into a misleading sales count.

## Install the mod

Use Minecraft 1.21.11, Fabric Loader 0.19.2 or newer, Fabric API, and Java 21. Copy `outputs/donut-auction-flips-1.0.0-loader-0.19.2.jar` into the Prism instance's `mods` directory.

The first launch creates `config/donut-network.properties`:

```properties
backend_url=http://127.0.0.1:8080
client_token=
poll_millis=250
chat_alerts=true
```

Press `N` or run `/dn` for the barebones control screen. Alerts provide a primary seller search and a canonical item-ID fallback such as `/ah redstone_block`; screen rows use the seller search. Confirm the item and price yourself—the public API does not expose a command-addressable listing ID, and the mod never clicks or buys an item.

The former double-blur crash is removed: the new screen draws an opaque background and never calls Minecraft's blur renderer.

## Optional client authentication

Local loopback use can omit a token. For any non-loopback backend bind, both variables are required:

```powershell
$env:DN_ADDRESS='0.0.0.0:8080'
$env:DN_CLIENT_TOKEN='replace-with-a-long-random-value'
```

Put that same 16-512 character printable token in the mod's `client_token`. It is our backend access token, not the Donut API key.

## Configuration

| Variable | Default | Purpose |
|---|---:|---|
| `DONUT_API_KEY` | required | Official API bearer key; backend only |
| `DONUT_API_BASE` | `https://api.donutsmp.net` | Official API base URL |
| `DN_ADDRESS` | `127.0.0.1:8080` | HTTP/debug bind |
| `DN_CLIENT_TOKEN` | empty on loopback | Protects the mod feed |
| `DN_LISTING_PAGES` | `220` | Newest 44-row auction pages per scan (max 220) |
| `DN_FAST_INTERVAL` | `250ms` | Pause between newest-page requests; effective cadence also respects the shared API limiter |
| `DN_COLLECTION_PAUSE` | `5s` | Pause after a completed scan |
| `DN_HISTORY_FILE` | `data/history.json.gz` | Bounded compressed sale history |
| `DN_MIN_PROFIT` | `100000` | Minimum expected profit |
| `DN_MIN_MARGIN_BPS` | `1000` | Minimum margin, 1000 = 10% |
| `DN_MIN_CONFIDENCE_BPS` | `5000` | Minimum model confidence, 5000 = 50% |
| `DN_MIN_VOLUME_24H` | `2` | Minimum completed sales in 24 hours |
| `DN_MAX_PURCHASE_PRICE` | `0` | Optional budget cap; 0 means unlimited |

## Build and test

```powershell
go test ./...
go vet ./...
gradle -p minecraft/client-mod clean test build
```

The Fabric build requires Gradle 9.5+ because the selected Fabric Loom version targets that plugin API. See [docs/architecture.md](docs/architecture.md), [docs/api.md](docs/api.md), and [DECISIONS.md](DECISIONS.md).
