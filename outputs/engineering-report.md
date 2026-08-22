# Donut Network engineering report

Date: 2026-08-16

## 1. What was built

A production-minded implementation of a distributed DonutSMP auction-flipping network:

- Go backend with authenticated HTTP ingestion, prioritized binary WebSockets, observations, presence, chat, telemetry, worker scheduling, health and Prometheus metrics.
- Official DonutSMP API collector with bearer authentication, pagination, 240 rpm pacing, retries, bounds and mock-contract tests.
- Deterministic item signatures/fingerprints and a robust valuation engine using MAD filtering, recency-weighted medians, active depth/best ask, liquidity/confidence and exact-to-base fallback.
- Atomic client price snapshots, incrementals/invalidation and a local integer-only flip decision path.
- Opt-in test simulator with market regimes, outliers, variable stacks, flips, competing purchases, 30-second listing lifetimes and batched ingest.
- PostgreSQL repository and normalized/indexed/partitioned migration.
- Fabric 1.21.11/Java 21 notify-only auction observer with conservative screen recognition, changed-slot parsing, backend-compatible signatures, bounded local order book, official-snapshot comparison fields, action-bar opportunities, and purchase revalidation boundaries.
- Barebones table-first Minecraft/DonutSMP operator console. The earlier decorative theme, generated promotional image and fabricated offline market data were removed.
- Live-first Compose runtime, optional Prometheus profile, load generator, CI and full developer/architecture/security documentation.

## 2. Final architecture

`Donut API → authenticated collector ingest → PostgreSQL + in-memory valuation engine → versioned snapshot/incrementals → prioritized WebSocket → atomic worker cache → local flip decision → async observation/purchase telemetry → operator projection`.

The browser dashboard uses a same-origin server proxy. The admin token remains server-side; official batch ingest and native workers use separate tokens. Live mode rejects simulator-tagged records and simulation-mode WebSockets. Slow clients shed chat first and are disconnected if they cannot accept market-critical frames, then recover from a full snapshot.

## 3. Run locally with live data

```bash
$env:DONUT_API_KEY='your-key'
docker compose --profile live up --build
```

Create the API key in-game with `/api`. Open `http://localhost:3000`; the official-feed status remains `waiting` until the first authenticated batch succeeds. Add `--profile metrics` to include Prometheus on port 9090. Running `docker compose up --build` without the live profile starts an intentionally empty backend and dashboard.

Without Docker, use three terminals:

```bash
$env:DONUT_API_KEY='your-key'
go run ./cmd/backend
go run ./cmd/collector
cd apps/dashboard && npm ci && npm run dev
```

## 4. Run tests

```bash
go test -race ./...
go vet ./...
go build ./cmd/...
cd apps/dashboard && npm ci && npm run lint && npm test && npm audit
gradle -p minecraft/client-mod build --no-daemon
docker compose config
```

All applicable local checks passed. Linux CI runs the Go race detector. Local Windows race execution lacked a CGO compiler. The Docker daemon was unavailable locally, so PostgreSQL/container runtime integration remains CI/next-environment work; Compose validation passed.

## 5. Run opt-in test simulation and load tools

```bash
$env:DN_DATA_MODE='simulation'
go run ./cmd/backend
go run ./cmd/simulator -rate 100 -workers 8 -flip-percent 12 -history 80
go run ./cmd/loadtest -clients 100 -events 100 -ramp 1s
go run ./cmd/loadtest -clients 1000 -events 10 -ramp 2s
```

This workload is test-only and cannot connect to a live-mode backend. A fresh 100 listings/sec E2E run produced 480 test transactions, eight valuation entries including two base fallbacks, eight test workers, dynamic assignments, flips and simulated purchases with zero HTTP errors/dropped frames. Worker and assignment counts returned to zero on disconnect.

## 6. Benchmarks

Measured on Windows/amd64, Intel i5-11600K:

| Measurement | Result |
| --- | ---: |
| Cached listing evaluation | 55.14 ns/op, 0 allocations |
| Existing-market observation | 2.100 µs/op, 1,148 B, 14 allocations |
| 500-sale valuation recomputation | 160.5 µs/op, 190,863 B, 28 allocations |
| 100 × 100 observations | 9,979.8 events/sec, 0 failed, p95 522.1 µs, p99 581.8 µs |
| 1,000 × 10 observations | 4,992.9 events/sec, 0 failed, p95 530.2 µs, p99 645.6 µs |
| Fabric changed-listing parse/evaluate pipeline | 5.36 µs/op, approximately 186,600 ops/sec |

The Windows timer printed p50 as `0s` for the sub-millisecond loopback workload; this means below timer resolution, not literal zero latency. Maximums were 9.21 ms and 13.69 ms.

## 7. Measured bottlenecks

- Robust 500-sale valuation recomputation allocates about 191 KB, but it is a background path and does not gate client decisions.
- Observation ingestion was optimized from roughly 25 µs to 2.10 µs by maintaining active aggregates instead of recalculating robust history on every listing.
- PostgreSQL still writes row-by-row inside transactions; batching/COPY is the first expected production throughput limit.
- Snapshot encoding and single-node fanout still need representative 100k-signature/1,000-slow-consumer profiling.

## 8. Known limitations

- No live Donut API key or sanitized Auction House UI fixture was available.
- The official collector was validated with an operator-supplied runtime key: one live cycle ingested 9,680 listings and 1,000 transactions without API errors. The key remains outside the repository and mod.
- Fabric 0.4 adds backend-ranked clickable chat alerts and a minimal `N`/`/dn` GUI. Clicks only run a sanitized `/ah <item>` search; Fabric recognition and lore variants still require sanitized notify-only validation against the live Donut server.
- Development credentials are pre-shared; production needs expiring, rotating, UUID-bound sessions and operator RBAC.
- PostgreSQL observation identities, valuation history, retention/partition jobs and migration query-plan integration tests remain.
- Base fallback is conservative but not yet calibrated against eventual-sale error.
- The dashboard is intentionally local-only and runs alongside the backend.

## 9. Current Donut integration assumptions

The implementation follows the official Swagger document retrieved on 2026-08-16: base `https://api.donutsmp.net`, bearer keys created with `/api`, listing pages at `GET /v1/auction/list/{page}` with optional search/sort body, transaction pages at `GET /v1/auction/transactions/{page}`, maximum ten transaction pages, and a published 250 requests/minute/key ceiling. The collector deliberately runs at 240 rpm and converts JSON money to integer currency at the boundary.

## 10. Exact next priorities

1. Capture sanitized live Auction House title/lore fixtures and validate the Fabric 0.2 adapter in notify-only mode.
2. Connect the existing Java snapshot cache to the authenticated backend WebSocket and persist screen-vs-official spread telemetry.
3. Add PostgreSQL COPY batching, observation identity persistence, Testcontainers migrations and representative query plans.
4. Calibrate base/modifier fallback confidence against eventual-sale error.
5. Replace development tokens with rotating signed UUID-bound sessions and operator RBAC, then benchmark 100k-signature snapshot encoding and mixed-speed WebSockets.
