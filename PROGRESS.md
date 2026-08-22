# Progress

Last updated: 2026-08-22

## Current system

- One rate-limited/retrying official API client reads up to ten transaction pages and the newest 220 44-row active-auction pages per cycle.
- `robust-v2` calculates manipulation-resistant exact/base valuations and ranks opportunities behind profit, margin, confidence, liquidity, expiry, and risk gates.
- Recent transactions persist in a deduplicated, versioned, compressed, safely rotated, 31-day/100,000-row local archive.
- One HTTP process exposes health, an ETag-enabled compact flip feed, JSON debug state, and a zero-dependency live debug page.
- Fabric 1.0.0 polls only that feed, caps/deduplicates chat alerts, validates commands, opens manual auction searches, and provides a plain `N`/`/dn` screen without blur.
- The upstream API key remains backend-only. Public backend binds require a separate client token.
- The complete parser/distributed implementation is preserved at tag `legacy-full-system-v0.4.0` and branch `archive/full-system-v0.4.0`.

## Verification

- `go test ./...` passes.
- `go vet ./...` passes.
- Fabric clean JUnit/build passes with JDK 21, Gradle 9.7, Loader 0.19.2, Minecraft 1.21.11, and Fabric API 0.141.6.
- API handler tests cover auth, ETags/304, short-page completion, failure state, safe public binding, and command sanitization.
- History tests cover round-trip compression, retention, deduplication, ordering, and caps.
- Mod tests cover a valid feed, injected-command rejection, and feed-size bounds.
- Final live authenticated scan: 1,000 transaction rows, 9,680 newest listings, 9,749 retained unique sales, 513 valuation families, two qualified flips, and zero upstream errors/retries. The exact counts change with the market.
- Docker Compose configuration validates. The local Docker daemon is not running, so the final image could not be executed here.
- Windows race instrumentation requires CGO and is unavailable locally; Linux CI runs `go test -race ./...`.

## Senior review passes

1. Fixed five high-value correctness/compatibility issues: Loader 0.19.2 metadata, the double-blur GUI crash, zero-as-unlimited budget semantics, first-scan health reporting, and removal of fake/legacy runtime paths.
2. Fixed stale conditional-client state by including collection/error state in ETags, added corrupt-primary history recovery from backup, protected debug routes with downstream auth when configured, made command truncation Unicode-safe, and added regression tests.
3. Added the missing decision/rejection funnel and per-signature evidence endpoint, removed unused simulator/client snapshot types, validated and bounded downstream tokens, returned real 404s for unknown paths, and re-ran tests/benchmarks.
4. Live evidence disproved the assumed 220-page full-book boundary: valid rows exist past page 2,000. The system now truthfully defines 220 pages as a recent-listing latency window and uses accumulating completed sales for broad-market value.

## Intentional omissions

- No screen parsing in the distributed mod.
- No automatic purchase, slot click, auction refresh, or anti-cheat interaction.
- No fake/simulated market records in normal or debug views.
- No database, WebSocket, worker, sharding, telemetry, Node dashboard, or hosted site.

## Highest-value future work

1. Run long enough to accumulate representative live completed-sale history and measure prediction error against later sales.
2. Add replay/backtesting reports before tuning model thresholds.
3. Add signed, expiring, UUID-bound client sessions before distributing access beyond trusted users.
4. Validate chat click/search/seller matching in a live sanitized DonutSMP session.
5. Revisit the archived parser only after the API-only path is measured and a real latency gap justifies the maintenance cost.
