# Progress

Last updated: 2026-08-24

## Implemented on `codex/auction-orders`

- The auction-only release remains frozen at `auction-only-v1.0.0` / `codex/auction-only`.
- The Go backend still collects auctions from the official API and now persists observer registration, leased tasks, idempotent order snapshots, fill evidence, watches, diagnostics, and backups in SQLite WAL.
- Order evidence graduates through captured, research, actionable, and conflict/hold states. Disappeared orders are ambiguous; only observed quantity reductions create fills.
- Combined candidates model exact batch quantity, fees, capital, order/auction slots, queue position, completion probability, cycle time, conservative volume, and risk-adjusted profit/day. There is no fixed `$100k` threshold.
- The `/order-auction-flipper` page exposes real observer health, freshness, scan coverage, disagreements, evidence history, candidates, rejection reasons, and a backend-only `$10M` reference portfolio. It generates no fake market rows.
- The Mineflayer collector manager launches an isolated child per account, validates dedicated proxy egress, uses separate Microsoft token caches, registers with the backend, long-polls leased tasks, reconnects independently, and submits order snapshots.
- Mineflayer's interaction adapter permits only `/orders` and controls explicitly verified by a captured schema. Empty/unknown schemas are capture-only; all economic actions and inventory transfers are denied.
- The distinct `2.0.0-alpha.1` Fabric client polls the combined candidate feed, parses or lets the player override balance, applies a dynamic reserve, allocates within 20 order and 18 auction slots, starts focused watches, displays candidate explanations, and provides validated manual navigation. No purchase is automatic.
- Fabric diagnostics are allowlisted, batched, rate-limited, retained for 14 days, enabled with a visible opt-out, and exclude personal/secret market context.
- Compose runs backend, collector manager, persistent data, and Caddy HTTPS termination. Loopback HTTP remains the development path.

## Verified locally

- `go test ./...` and `go vet ./...` pass. Tests cover authenticated leases, idempotent scans, capture-only exclusion, fill/disappearance semantics, current-price freshness, shared-watch lifecycle, exact-quantity valuation, candidate executable volume, backups, scoped auth, API handlers, and diagnostic redaction.
- Node TypeScript build and six parser/navigation tests pass; `npm audit --omit=dev` reports zero vulnerabilities.
- Fabric JUnit/build passes on Minecraft 1.21.11, Java 21, Loader 0.19.2, Fabric API 0.141.6, Loom 1.17.19, and Gradle 9.6.1.
- Candidate-frontier benchmark: approximately 118µs/op for 100 evidence rows on the local i5-11600K. Existing market benchmarks remain near or under roughly 2ms/op for their checked-in workloads.
- `docker compose config --quiet` validates with placeholder environment credentials. The Docker Desktop Linux daemon is not running, so containers could not be executed locally.
- Windows `go test -race` is unavailable because the local Go race build requires CGO. The normal suite is pure Go; race instrumentation remains a Linux CI/deployment-host gate.

## Senior review passes

1. Added unguessable renewable lease tokens, ownership/expiry validation on heartbeats/scans/results, and tests that reject a forged lease.
2. Prevented unknown or incomplete schemas from entering price/fill evidence, required verified canonical modifier signatures and queue position, and reused the auction engine's singular-plus-exact-batch valuation path.
3. Replaced invented one-batch volume with strict two-sided executable limits, separated current order capacity from historical fill velocity, and stopped retained historical high rewards/queue ranks from leaking into current candidates.
4. Deduplicated shared focused-watch tasks, prevented one client watch deletion from stopping another, added current scan/fill/watch/reference-portfolio debug surfaces, scoped credentials, diagnostic rate limiting, and bounded retention/backups.
5. Hardened collector secret permissions, focused-watch refresh cadence, reconnect backoff reset, exact command/control denial, and assigned the combined Fabric artifact a distinct `2.0.0-alpha.1` identity.

A final fresh pass found no further code-only improvement that outweighed the risk of inventing behavior without real Donut order-menu and transaction-message fixtures.

## Shadow-mode limitations

- No real order-menu schema is enabled. `collector/order-schemas.json` is intentionally empty, so collectors capture unknown layouts and stop instead of clicking.
- Microsoft login reuse, each proxy type, signed `/orders`, real item components/lore, pagination, refresh cadence, scoreboard messages, and reconnect behavior cannot be certified without operator accounts and sanitized live fixtures.
- Therefore combined candidates should be treated as research-only until repeated real scans and fill-rate calibration graduate an item to actionable evidence.
- Local position inference is fail-closed until real success/failure message fixtures exist; used slots remain manually adjustable and no transaction or complete inventory data is uploaded.

## Next rollout gates

1. Copy the collector account example to a permission-restricted host secret and bootstrap each Microsoft account.
2. Run one observer through each configured proxy and collect unknown-menu diagnostics without navigation.
3. Review and version a real order-menu fixture, then enable only the proven non-transactional controls.
4. Run multiple observers in shadow mode and measure coverage, disagreement, attainable refresh cadence, and prediction error.
5. Enable Fabric `READY` notifications only after fill velocity, exact-quantity exits, and stability calibration pass.
