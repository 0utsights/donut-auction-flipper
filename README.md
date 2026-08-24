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

The default recent-listing window takes roughly a minute to scan because the client stays below Donut's published request limit. It covers the newest 9,680 rows (220 × 44), not the entire deep auction book. Completed-sale history is the broad-market authority and is retained in `data/history.json.gz`, capped at 100,000 rows and 31 days.

Stacked listings are quantity-safe: the backend requires both quantity-one sales and completed sales at the exact listed quantity, uses the lower per-item quick-sell estimate, then multiplies by the unchanged resale quantity. It will not recommend a stack based on profit that only exists after splitting it.

## Install the mod

Use Minecraft 1.21.11, Fabric Loader 0.19.2 or newer, Fabric API, and Java 21. Copy `outputs/donut-auction-flips-1.0.0-loader-0.19.2.jar` into the Prism instance's `mods` directory.

The first launch creates `config/donut-network.properties`:

```properties
backend_url=http://127.0.0.1:8080
client_token=
poll_seconds=2
chat_alerts=true
```

Press `N` or run `/dn` for the barebones control screen. Clicking an alert or row runs a sanitized auction search. Confirm the seller and price yourself; the mod never clicks or buys an item.

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
