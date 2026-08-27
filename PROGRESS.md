# Progress

Last updated: 2026-08-27

## Implemented on `codex/auction-orders`

- The auction-only release remains frozen at `auction-only-v1.0.0` / `codex/auction-only`.
- The Go backend still collects auctions from the official API and now persists observer registration, leased tasks, idempotent order snapshots, fill evidence, watches, diagnostics, and backups in SQLite WAL.
- Order evidence graduates through captured, research, actionable, and conflict/hold states. Disappeared orders are ambiguous; only observed quantity reductions create fills.
- Live-menu evidence now counts independent sessions rather than pagination pages, compares top-of-book prices per session, and accepts only bounded same-page focused reductions when Donut omits owner/order IDs. The fast path can graduate an actively filling, stable market after 30 seconds of focused evidence; auction liquidity gates remain unchanged.
- Combined candidates model exact auction-exit batch quantity, fees, queue position, completion probability, cycle time, conservative 24-hour volume, and risk-adjusted profit/day. The backend publishes full market capacity without a player balance cap; Fabric alone sizes acquisition orders from local balance, reserve, session budget, and exposure limits.
- Exact-stack auction volume, seller diversity, and freshness are recomputed at the final proposed resale price. Singular history is only a conservative unit-price ceiling. The calibrated 35% confidence floor still requires five nearby exact-stack sales from three sellers, while target freshness scales with observed sale velocity instead of an incompatible fixed two-hour cutoff.
- The bare `/order-auction-flipper` page shows only current `READY` decisions plus a collapsed research queue and updates automatically. Collector health, freshness, scan coverage, disagreements, evidence history, rejection reasons, and the backend-only `$10M` reference portfolio remain on `/order-auction-flipper/debug`. Neither surface generates fake market rows.
- The Mineflayer collector manager launches an isolated child per account, validates dedicated proxy egress, uses separate Microsoft token caches, registers with the backend, long-polls leased tasks, reconnects independently, and submits order snapshots.
- Collector lifecycle logs distinguish verified proxy egress, Minecraft connect, authenticated login, and spawn without exposing account, token, proxy credential, or server packet contents, making pre-spawn failures diagnosable from the live debug process.
- Mineflayer's interaction adapter permits only `/orders` and controls explicitly verified by a captured schema. Empty/unknown schemas are capture-only; all economic actions and inventory transfers are denied.
- The live Fabric/player-client order wizard has been mapped through its final review screen without spending currency. `docs/order-creation-workflow.md` records the exact navigation, inputs, review invariants, fail-closed Fabric state machine, and remaining low-value acceptance test.
- The distinct `2.1.0-alpha.5` Fabric client polls the combined candidate feed, parses or lets the player override balance, applies a dynamic reserve, and first maximizes distinct profitable offers up to the 20-order limit before assigning additional measured volume by profit/day per dollar. One acquisition order may contain millions of units; it exits through sequential exact-stack listings of at most 64 that reuse the 18 auction slots. It permits only one active order per item, persists local duplicate locks, reconciles the minimum used-order count from those locks, verifies the server's Your Orders screen before every creation, and stops on direct duplicate-order server messages. Its compact paginated UI explains all planned orders, local cash/reserve/slots, total escrow, exit-listing count, margin, confidence, completion, and cycle time. DonutSMP staff authorization for observer collection and locally armed Fabric order creation was explicitly confirmed by the operator on 2026-08-26.
- Confirmed long-lived item profiles are maintained incrementally in compact SQLite tables. Proven markets receive a 20-second focused revalidation lane that can preempt broad discovery, while every economic action still requires current stable evidence and Fabric's six-second final price check.
- Fabric diagnostics are allowlisted, batched, rate-limited, retained for 14 days, enabled with a visible opt-out, and exclude personal/secret market context.
- Compose runs backend, collector manager, persistent data, and Caddy HTTPS termination. Loopback HTTP remains the development path.
- The second-PC overlay bounds backend and collector CPU, memory, and process counts, uses read-only container filesystems with explicit writable mounts/tmpfs, prevents privilege escalation, and rotates container logs. The backend receives a bounded 512 MiB temporary filesystem because SQLite's initial evidence-session backfill can exceed a small default temp area; its total memory ceiling is 1.5 GiB.
- The constrained second-PC profile polls the official newest auction page every 500ms, retains raw order observations for 24 hours, batches retention work, and keeps one automatic SQLite backup per UTC day for seven days. Live pruning reduced duplicate backup storage from 5.5 GiB to about 1.0 GiB while preserving the manually named safety copy.

## Verified locally

- `go test ./...` and `go vet ./...` pass. Tests cover authenticated leases, idempotent scans, capture-only exclusion, fill/disappearance semantics, current-price freshness, shared-watch lifecycle, exact-quantity valuation, candidate executable volume, backups, scoped auth, API handlers, and diagnostic redaction.
- Node TypeScript build and 27 parser, packet-settlement, navigation, authentication-proxy, configuration, and redaction tests pass; `npm audit --omit=dev` reports zero vulnerabilities.
- Fabric JUnit/build passes on Minecraft 1.21.11, Java 21, Loader 0.19.2, Fabric API 0.141.6, Loom 1.17.19, and Gradle 9.6.1.
- Candidate-frontier benchmark is approximately 0.20–0.24ms/op for 100 evidence rows on the local i5-11600K. Quantity-aware opportunity analysis is approximately 1.46ms/op after caching the completed quantity pair; the other checked-in market benchmarks are approximately 0.17–2.56ms/op in the final pass.
- The Go race suite passes in CI and in a fresh Go 1.26 Linux container on the second PC. The production backend is healthy, and LividClick is currently authenticated, spawned, confirming `Most Per Item`, and submitting complete pages. A live 15-minute debug sample reached page 388 with 44 current base-safe item signatures, 711 confirmed reductions, zero incomplete scans, and zero unknown-schema scans; menu closures rotate and recover automatically.

## Senior review passes

1. Added unguessable renewable lease tokens, ownership/expiry validation on heartbeats/scans/results, and tests that reject a forged lease.
2. Prevented unknown or incomplete schemas from entering price/fill evidence, required verified canonical modifier signatures and queue position, and reused the auction engine's singular-plus-exact-batch valuation path.
3. Replaced invented one-batch volume with strict two-sided executable limits, separated current order capacity from historical fill velocity, and stopped retained historical high rewards/queue ranks from leaking into current candidates.
4. Deduplicated shared focused-watch tasks, prevented one client watch deletion from stopping another, added current scan/fill/watch/reference-portfolio debug surfaces, scoped credentials, diagnostic rate limiting, and bounded retention/backups.
5. Hardened collector secret permissions, focused-watch refresh cadence, reconnect backoff reset, exact command/control denial, and assigned the combined Fabric artifact a distinct `2.0.0-alpha.1` identity.
6. Verified the real 1.21.11 Donut order layout, exact cent parsing, server-acknowledged pagination, proxy/SRV login, credential redaction, and fail-closed schema behavior. A single live pass completed 364 pages / 16,380 rows without a sequence kick.
7. Fixed optimistic window-state submissions, partial-page connection reuse, watch deletion versus active-lease races, insecure remote HTTP configuration, unbounded backend calls, lifetime-based reconnect delays, and proxy-egress restart loops.
8. Added the Fabric-only one-order execution boundary: a separate explicit arm, exact cent/stack escrow checks, a DonutSMP hostname allowlist, live feed/order/auction freshness, local reserve/slot/session-budget enforcement, verified chest/dialog transitions, exact registry-item review matching, and a single final action.
9. Hardened that executor against disconnects, stalled screens, changed candidates, duplicate pending signatures, quantity-prefix and money-suffix parser collisions, raw diagnostic leakage, and active-order rows. Server outcomes remain conservatively pending until real success/failure fixtures are captured.
10. Fixed Microsoft device authentication repeatedly invalidating its own login code: the collector now keeps one device-code attempt alive for ten minutes while cached-token logins still return immediately.
11. Settled complete `window_items`/`set_slot` packet bursts, tracked replacement window IDs, rejected unrelated/closed windows, and rotated stale menu sessions. Live discovery reached page 35 and focused watches traversed to pages 23–24 without the former false page-10 ending.
12. Added indexed, yielding SQLite retention; coalesced automatic backups; bounded raw/fill/diagnostic history; and moved the second-PC fast lane to 500ms. Startup maintenance no longer monopolizes observer requests.
13. Restricted subsecond scoring to the fetched newest page, reused unchanged immutable results, indexed freshness windows, skipped stale order valuations, and coalesced non-moving active asks while keeping low asks immediate. Live backend CPU samples fell from the original sustained 60–80% range to roughly 20–42%, with the lower readings outside broad-pass spikes.
14. Replaced the greedy reference portfolio with a deterministic Pareto allocator, combined exact resale batches into one acquisition order per canonical item, persisted and reconciled duplicate locks against local slot state, and required a fresh focused order price immediately before execution.
15. Recomputed auction liquidity at the final exact-stack resale target, prevented singular observations from inflating stack volume, calibrated target freshness to observed sale velocity, rejected future transactions, and cached completed quantity pairs to improve the quantity-aware benchmark from roughly 11.6ms to 1.46ms/op.
16. Routed the complete Microsoft/Xbox/Minecraft authentication chain through each account's configured proxy, restored direct backend traffic after session acquisition, and added sanitized connection-stage diagnostics. This isolated the remaining live outage to Donut's pre-login edge rather than authentication, API health, proxy egress, or parsing.
17. Removed the artificial 18-stack acquisition cap, separated total order quantity from concurrent auction listings, and moved every balance/reserve/exposure decision into Fabric. Exact exits remain at most 64 units and recycle auction slots over time.
18. Added permanent compact item profiles and an expedited revalidation lane. A senior pass replaced the initial 90-day regrouping query with incremental profile/order aggregates so 750ms candidate refreshes remain bounded as data grows.
19. Reworked Fabric allocation to maximize distinct safe offers before bulk sizing, added deterministic randomized invariant coverage, and enforced strict route-slot and exact-max-stack semantics on every decoded candidate.
20. Replaced the clipped expanded mod screen with a 440×240 barebones order console. Four-row pagination exposes all 20 planned offers while detailed tooltips and the arm screen explain bulk quantity, escrow, sequential exits, model quality, and duplicate behavior.

A fresh post-deployment pass found no further code-only improvement that outweighed the risk of weakening conservative evidence gates or inventing button/server-outcome behavior without another real fixture.

## Shadow-mode limitations

- The verified schema enables only `/orders`, the exact refresh book, and the exact next-page arrow. Changed layouts remain capture-only.
- A conservative base-commodity allowlist may be signature-complete. Modifier-bearing and variant-sensitive rows remain research-only until stable order identity and component equivalence can be proven.
- HTTP CONNECT egress, Microsoft token reuse, Donut login/spawn, verified menu navigation, and automatic recovery are live on the configured observer. Additional accounts and proxy types still require their own acceptance checks.
- Local position inference is fail-closed until real success/failure message fixtures exist; used slots remain manually adjustable and no transaction or complete inventory data is uploaded.

## Next rollout gates

1. Add and bootstrap any additional observer accounts in separate permission-restricted profiles and verify each configured proxy egress.
2. Run multiple observers in shadow mode and measure overlap, disagreement, full-pass duration, and attainable focused-watch cadence.
3. Capture stable order identity/modifier/queue fields if Donut exposes them; keep actionability disabled if equivalence cannot be proven.
4. Calibrate observed quantity reductions against real fills and exact-quantity auction exits.
5. Enable Fabric `READY` notifications only after fill velocity, exact-quantity exits, and stability calibration pass.
