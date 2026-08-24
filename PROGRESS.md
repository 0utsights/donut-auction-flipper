# Progress

Last updated: 2026-08-23

## Current system

- One rate-limited/retrying official API client runs a sub-second newest-page detection lane alongside a background ten-transaction-page/220-active-page valuation lane.
- `robust-v4-price-volume-quantity` ranks opportunities using the lower of quantity-one and exact-resale-quantity per-item valuations, and qualifies liquidity only from 24-hour sales within ±10% of the intended resale price.
- Recent transactions persist in a deduplicated, versioned, compressed, safely rotated, 31-day/100,000-row local archive.
- One HTTP process exposes health, an ETag-enabled compact flip feed, JSON debug state, and a zero-dependency live debug page.
- Fabric 1.0.0 polls only that feed, caps/deduplicates chat alerts, validates commands, opens manual auction searches, and provides a plain `N`/`/dn` screen without blur.
- The upstream API key remains backend-only. Public backend binds require a separate client token.
- The complete parser/distributed implementation is preserved at tag `legacy-full-system-v0.4.0` and branch `archive/full-system-v0.4.0`.
- Auction-only v1.0.0 is frozen at tag/release `auction-only-v1.0.0` and branch `codex/auction-only`; active auction-plus-orders work lives on `codex/auction-orders`.
- `/order-auction-flipper` is an intentionally empty, no-fake-data foundation until an order-only Fabric menu reader is designed from live menu evidence.

## Verification

- `go test ./...` passes.
- `go vet ./...` passes.
- Fabric JUnit/build passes with JDK 21, Gradle 9.6.1, Loader 0.19.2, Minecraft 1.21.11, and Fabric API 0.141.6.
- API handler tests cover auth, ETags/304, short-page completion, failure state, safe public binding, and command sanitization.
- History tests cover round-trip compression, retention, deduplication, ordering, and caps.
- Mod tests cover a valid feed, injected-command rejection, and feed-size bounds.
- Final live authenticated fast-lane soak: 99 snapshot versions across a complete broad scan published 741ms apart on average and 984ms maximum; upstream-to-feed duration was 733ms maximum, with zero upstream errors, retries, or rate-limit responses.
- Concurrent broad scan: 1,000 transaction rows and 9,680 newest listings completed in 87.8 seconds without blocking fast publication. At that moment the retained archive contained 77,216 unique sales and the model contained 1,099 valuation families; market-dependent flip counts change continuously.
- Final live Lava Chicken regression: after the latest sales arrived, fair value moved to $250,000 and the conservative target to $205,000. Only 4 of 12 daily trades from 2 sellers fall in its $184,500-$225,500 target band; high volatility/falling-market evidence lowers confidence to 15.25%, so it remains rejected rather than inheriting all item-wide volume.
- Docker Compose configuration validates. The local Docker daemon is not running, so the final image could not be executed here.
- Windows race instrumentation requires CGO and is unavailable locally; Linux CI runs `go test -race ./...`.

## Senior review passes

1. Fixed five high-value correctness/compatibility issues: Loader 0.19.2 metadata, the double-blur GUI crash, zero-as-unlimited budget semantics, first-scan health reporting, and removal of fake/legacy runtime paths.
2. Fixed stale conditional-client state by including collection/error state in ETags, added corrupt-primary history recovery from backup, protected debug routes with downstream auth when configured, made command truncation Unicode-safe, and added regression tests.
3. Added the missing decision/rejection funnel and per-signature evidence endpoint, removed unused simulator/client snapshot types, validated and bounded downstream tokens, returned real 404s for unknown paths, and re-ran tests/benchmarks.
4. Live evidence disproved the assumed 220-page full-book boundary: valid rows exist past page 2,000. The system now truthfully defines 220 pages as a recent-listing latency window and uses accumulating completed sales for broad-market value.
5. Rejected stack false positives by making quantity-one evidence mandatory, requiring an exact-quantity completed-sale cohort, conservatively combining both models, and exposing unit/total references in debug output.
6. The follow-up quantity review added invariant tests for total-to-unit normalization, exact-quantity active competition, missing-cohort rejection, dual-reference audit fields, and a dedicated ranking benchmark. No further quantity-path issue in the current scope justified additional complexity.
7. Replaced ambiguous display-name searches with seller-first navigation plus canonical underscore item-ID fallbacks, added independently validated dual chat actions, and documented why synthetic API fingerprints cannot directly open a listing.
8. Split newest-listing detection from broad collection, seeded the engine from retained history at startup, reduced local mod polling to 250ms, preserved last-good feeds on fast failures, and exposed measured fast-lane latency in debug output.
9. The fresh concurrency/operations review prevented a finishing broad scan from replacing a newer fast feed, added regression coverage for ordering and model isolation, moved the 9,680-row active-book merge off the live fast engine after observing a 1.36-second contention spike, reduced debug-page refresh from five seconds to one, and verified shared-rate-limit behavior live.
10. Reproduced the Lava Chicken split-price market, separated target-price volume from broad item volume, made confidence/volume/sell-time gates use only the ±10% target cohort, recognized two-seller active competition, and exposed both volume measures in debug output.
11. Released the stable auction-only system with reproducible JAR/backend assets, split future order development onto its own branch, confirmed the official API lacks orders, and added a source-status/ranking-rules page that cannot publish fabricated order opportunities.

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
