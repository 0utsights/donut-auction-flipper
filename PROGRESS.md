# Progress

Last updated: 2026-08-27

## Implemented on `codex/auction-orders`

- The auction-only release remains frozen at `auction-only-v1.0.0` / `codex/auction-only`.
- The Go backend still collects auctions from the official API and now persists observer registration, leased tasks, idempotent order snapshots, fill evidence, watches, diagnostics, and backups in SQLite WAL.
- Order evidence graduates through captured, research, actionable, and conflict/hold states. Disappeared orders are ambiguous; only observed quantity reductions create fills.
- Live-menu evidence now counts independent sessions rather than pagination pages, compares top-of-book prices per session, and accepts only bounded same-page focused reductions when Donut omits owner/order IDs. The fast path can graduate an actively filling, stable market after 30 seconds of focused evidence; auction liquidity gates remain unchanged.
- Combined candidates model exact batch quantity, fees, capital, order/auction slots, queue position, completion probability, cycle time, conservative volume, and risk-adjusted profit/day. There is no fixed `$100k` threshold.
- Exact-stack auction volume, seller diversity, and freshness are recomputed at the final proposed resale price. Singular history is only a conservative unit-price ceiling. The calibrated 35% confidence floor still requires five nearby exact-stack sales from three sellers, while target freshness scales with observed sale velocity instead of an incompatible fixed two-hour cutoff.
- The bare `/order-auction-flipper` page shows only current `READY` decisions plus a collapsed research queue and updates automatically. Collector health, freshness, scan coverage, disagreements, evidence history, rejection reasons, and the backend-only `$10M` reference portfolio remain on `/order-auction-flipper/debug`. Neither surface generates fake market rows.
- The Mineflayer collector manager launches an isolated child per account, validates dedicated proxy egress, uses separate Microsoft token caches, registers with the backend, long-polls leased tasks, reconnects independently, and submits order snapshots.
- Collector lifecycle logs distinguish verified proxy egress, Minecraft connect, authenticated login, and spawn without exposing account, token, proxy credential, or server packet contents, making pre-spawn failures diagnosable from the live debug process.
- Mineflayer's interaction adapter permits only `/orders` and controls explicitly verified by a captured schema. Empty/unknown schemas are capture-only; all economic actions and inventory transfers are denied.
- The live Fabric/player-client order wizard has been mapped through its final review screen without spending currency. `docs/order-creation-workflow.md` records the exact navigation, inputs, review invariants, fail-closed Fabric state machine, and remaining low-value acceptance test.
- The distinct `2.1.0-alpha.4` Fabric client polls the combined candidate feed, parses or lets the player override balance, applies a dynamic reserve, and uses a deterministic Pareto optimizer to allocate bulk acquisition orders within 20 order and 18 auction slots. It permits only one active order per item, persists local duplicate locks, reconciles the minimum used-order count from those locks, verifies the server's Your Orders screen before every creation, and stops on direct duplicate-order server messages. Its Fabric-only executor preserves cent-precise stack economics, verifies each chest/dialog transition, repeats focused freshness/budget/slot checks immediately before `Create Order`, and stops on any ambiguity. DonutSMP staff authorization for observer collection and locally armed Fabric order creation was explicitly confirmed by the operator on 2026-08-26.
- Fabric diagnostics are allowlisted, batched, rate-limited, retained for 14 days, enabled with a visible opt-out, and exclude personal/secret market context.
- Compose runs backend, collector manager, persistent data, and Caddy HTTPS termination. Loopback HTTP remains the development path.
- The second-PC overlay bounds backend and collector CPU, memory, and process counts, uses read-only container filesystems with explicit writable mounts/tmpfs, prevents privilege escalation, and rotates container logs. The backend receives a bounded 512 MiB temporary filesystem because SQLite's initial evidence-session backfill can exceed a small default temp area; its total memory ceiling is 1.5 GiB.
- The constrained second-PC profile polls the official newest auction page every 500ms, retains raw order observations for 24 hours, batches retention work, and keeps one automatic SQLite backup per UTC day for seven days. Live pruning reduced duplicate backup storage from 5.5 GiB to about 1.0 GiB while preserving the manually named safety copy.

## Verified locally

- `go test ./...` and `go vet ./...` pass. Tests cover authenticated leases, idempotent scans, capture-only exclusion, fill/disappearance semantics, current-price freshness, shared-watch lifecycle, exact-quantity valuation, candidate executable volume, backups, scoped auth, API handlers, and diagnostic redaction.
- Node TypeScript build and 27 parser, packet-settlement, navigation, authentication-proxy, configuration, and redaction tests pass; `npm audit --omit=dev` reports zero vulnerabilities.
- Fabric JUnit/build passes on Minecraft 1.21.11, Java 21, Loader 0.19.2, Fabric API 0.141.6, Loom 1.17.19, and Gradle 9.6.1.
- Candidate-frontier benchmark is approximately 142µs/op for 100 evidence rows on the local i5-11600K. Quantity-aware opportunity analysis is approximately 1.46ms/op after caching the completed quantity pair; the other checked-in market benchmarks are approximately 0.17–2.56ms/op in the final pass.
- The Go race suite passes in a fresh Go 1.26 Linux container on the second PC. The production backend is healthy on the second PC at commit `f458e85`. The collector manager is running and retrying safely, but its current observer is offline: both tested direct Webshare exits pass egress verification and Microsoft authentication, after which Donut closes the raw Minecraft socket before login/spawn without sending a protocol disconnect reason. Existing order evidence therefore ages normally to `STALE`; it is never presented as fresh.

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

A fresh post-deployment pass found no further code-only improvement that outweighed the risk of weakening conservative evidence gates or inventing button/server-outcome behavior without another real fixture.

## Shadow-mode limitations

- The verified schema enables only `/orders`, the exact refresh book, and the exact next-page arrow. Changed layouts remain capture-only.
- A conservative base-commodity allowlist may be signature-complete. Modifier-bearing and variant-sensitive rows remain research-only until stable order identity and component equivalence can be proven.
- HTTP CONNECT egress, Microsoft token reuse, and a proxied Donut status handshake are verified. The current account is still closed by Donut between authenticated session acquisition and the Minecraft login packet on two direct Webshare exits; another account or a Donut-approved/residential exit must complete the remaining live acceptance path.
- Local position inference is fail-closed until real success/failure message fixtures exist; used slots remain manually adjustable and no transaction or complete inventory data is uploaded.

## Next rollout gates

1. Add and bootstrap any additional observer accounts in separate permission-restricted profiles and verify each configured proxy egress.
2. Run multiple observers in shadow mode and measure overlap, disagreement, full-pass duration, and attainable focused-watch cadence.
3. Capture stable order identity/modifier/queue fields if Donut exposes them; keep actionability disabled if equivalence cannot be proven.
4. Calibrate observed quantity reductions against real fills and exact-quantity auction exits.
5. Enable Fabric `READY` notifications only after fill velocity, exact-quantity exits, and stability calibration pass.
