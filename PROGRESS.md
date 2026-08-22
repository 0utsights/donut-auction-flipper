# Progress

Last updated: 2026-08-20

## Completed systems

- Current official DonutSMP auction/transaction API client with bearer auth, all ten transaction pages, up to 220 listing pages, response/schema validation, bounded container conversion, 240 rpm pacing, retry statistics and mock-server tests.
- `robust-v2` seller-resistant valuation with short/long horizons, MAD filtering, distinct-seller active references, capped volume, market regimes, reference age, expected sale time, exact/base fallback, modifier-fidelity warnings and evidence APIs.
- Versioned compact Fabric snapshots with ETags, chunked 64 KiB WebSocket recovery snapshots, incremental family price updates, atomic restart-safe caches and two-minute stale fail-closed behavior.
- Authenticated HTTP ingestion and binary WebSocket gateway with P0/P1/P2 backpressure, origin checks, bounded rate identities, targeted assignments, presence cleanup, chat isolation and drop metrics.
- Dynamic worker scheduler, search-sharding comparison, eight-worker simulator, changing market regimes, underpricing/outliers, batched market feed and deterministic competing-purchase outcomes.
- PostgreSQL pgx batch repository plus automatic advisory-locked migrations, normalized source records, valuation history and collector-cycle history.
- Barebones DonutSMP/Minecraft operator console with no fabricated data or promotional imagery; the Debug view exposes collector health, model decisions, risk flags and raw exact/base evidence behind the admin proxy.
- Live-only default startup: the dashboard begins empty, reports its source mode, the authenticated collector is opt-in, and simulation is isolated behind a test profile.
- Fabric 1.21.11 client core and tests, Compose runtime, Prometheus profile, CI, developer documentation and reproducible load/benchmark tools.
- Fabric 0.4 auction client with conservative changed-slot parsing, compact backend valuation and opportunity polling, clickable chat search alerts, a minimal `N`/`/dn` control screen, bounded deduplication, stale-feed shutdown and manual-only purchase selection.
- Fabric client 0.4.0 compiled and packaged against Loader 0.19.2 for Minecraft 1.21.11 at `outputs/donut-network-client-0.4.0-loader-0.19.2.jar`.

## Verification performed

- `go test ./...` — pass.
- `go vet ./...` — pass.
- Dashboard production build/render tests — pass.
- Dashboard ESLint — pass.
- Full npm audit — 0 vulnerabilities after upgrading the dashboard toolchain and removing unused hosting/database starter dependencies.
- Fabric Gradle 9.7 / JDK 21 build — pass, including JUnit tests.
- Fresh Fabric 0.4 clean test/build and 200,000-iteration auction pipeline benchmark — pass at 4.698 µs/op (about 212,857 ops/sec).
- `docker compose config --quiet` — pass.
- Fresh E2E at 100 listings/sec and eight workers — 600 transactions, eight valuation families, 2,138 observations, 30 detected flips, 22 purchases, working evidence debug, 0 HTTP errors and 0 dropped frames; workers and assignments returned to zero on disconnect.
- Unauthorized operator projection returned 401; same-origin dashboard proxy returned 200 with the server-held admin token.
- Race test is configured and runs in Linux CI. Local Windows execution could not run it because the installed Go toolchain has no CGO compiler.
- Docker/PostgreSQL runtime integration was not executed because the local Docker daemon was unavailable.

## Measured performance

- Robust-v2 valuation over 500 sales: 624 µs/op, 1.28 MB/op, 1,080 allocations; this is a backend recomputation path, not the listing hot path.
- New-engine seed of 500 sales plus a 44-listing batch: 1.93 ms/op.
- 100 clients × 100 HTTP observations after active-book review: 43,920 events/sec, 0 failed, p50 1.96 ms, p95 4.81 ms, p99 7.75 ms, max 18.52 ms.
- Fabric changed-listing parse, canonicalize, index, evaluate, and enqueue pipeline: 4.698 µs/op (about 212,857 ops/sec over 200,000 iterations); unchanged slots perform only a structured fingerprint comparison.
- Backend existing-listing observation benchmark: 246.8 µs/op; worker cached listing evaluation: 78.1 ns/op with zero allocations. Opportunity rankings are cached once per market version for all polling clients.

## Senior review passes and fixes

1. Hardened the market model against repeated sellers, bait listings, seller walls, falling markets, stale evidence, API modifier blindness and arithmetic overflow; bounded recursive containers and retained only 31 days in memory.
2. Expanded collection to a rate-limited full scan, added row/payload validation, stable best-effort API identities, expiry handling, collector telemetry, batch persistence, valuation history and startup migrations.
3. Added exact/base evidence debugging, compact ETag polling, restart-safe Fabric caches, family update broadcasts and chunked WebSocket recovery; eliminated silent oversize frames and full-map incremental copies.
4. Load telemetry exposed quadratic active-book recomputation. Evidence-aware repricing removed it and improved the measured 100×100 workload from 1,296 to 43,920 events/sec.
5. Added backend-ranked opportunity delivery and reviewed it twice: compacted the client contract, failed closed after two stale minutes, blocked modifier-blind/stale-reference valuations, diversified duplicate item alerts, preserved stale state across ETags, sanitized commands and capped chat bursts.
6. Re-ran all Go tests plus the Fabric clean build, tests and benchmark. A live authenticated cycle ingested 9,680 listings and 1,000 transactions with zero API errors; the hardened opportunity endpoint returned a 4.1 KB feed before duplicate-signature compaction.

## Known limitations

- The official collector and opportunity endpoint have been validated with a live key. The Fabric screen recognition and clickable in-game rendering still need a sanitized live DonutSMP client fixture; no purchase action is automated.
- PostgreSQL uses protocol batches but not `COPY`; observation identity rows, retention jobs and Testcontainers query-plan tests remain production work.
- Development credentials are pre-shared. Production needs expiring signed sessions bound to verified Minecraft UUID ownership, rotation and revocation.
- Single-node in-process fanout is intentional. A 1,000 slow/fast WebSocket benchmark should precede a NATS/multi-node split.
- Base-family fallback is deliberately conservative rather than statistically calibrated against eventual-sale error.

## Next highest-value tasks

1. Capture sanitized live Auction House screens and validate Fabric parsing, chat click rendering and seller/price matching in notify-only mode.
2. Add PostgreSQL `COPY` batching, full observation identity persistence, migration integration tests and representative `EXPLAIN (ANALYZE, BUFFERS)` results.
3. Calibrate fallback confidence and measure prediction error by signature family/liquidity band.
4. Replace development tokens with rotating signed UUID-bound sessions and operator RBAC.
5. Benchmark 100k-signature snapshot encoding plus 1,000 slow/fast WebSocket consumers, then add diffs/checksums and multi-node fanout only if measurements justify it.
