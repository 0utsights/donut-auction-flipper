# Progress

Last updated: 2026-08-25

## Implemented on `codex/auction-orders`

- The auction-only release remains frozen at `auction-only-v1.0.0` / `codex/auction-only`.
- The Go backend still collects auctions from the official API and now persists observer registration, leased tasks, idempotent order snapshots, fill evidence, watches, diagnostics, and backups in SQLite WAL.
- Order evidence graduates through captured, research, actionable, and conflict/hold states. Disappeared orders are ambiguous; only observed quantity reductions create fills.
- Live-menu evidence now counts independent sessions rather than pagination pages, compares top-of-book prices per session, and accepts only bounded same-page focused reductions when Donut omits owner/order IDs. The fast path can graduate an actively filling, stable market after 30 seconds of focused evidence; auction liquidity gates remain unchanged.
- Combined candidates model exact batch quantity, fees, capital, order/auction slots, queue position, completion probability, cycle time, conservative volume, and risk-adjusted profit/day. There is no fixed `$100k` threshold.
- The `/order-auction-flipper` page exposes real observer health, freshness, scan coverage, disagreements, evidence history, candidates, rejection reasons, and a backend-only `$10M` reference portfolio. It generates no fake market rows.
- The Mineflayer collector manager launches an isolated child per account, validates dedicated proxy egress, uses separate Microsoft token caches, registers with the backend, long-polls leased tasks, reconnects independently, and submits order snapshots.
- Mineflayer's interaction adapter permits only `/orders` and controls explicitly verified by a captured schema. Empty/unknown schemas are capture-only; all economic actions and inventory transfers are denied.
- The live Fabric/player-client order wizard has been mapped through its final review screen without spending currency. `docs/order-creation-workflow.md` records the exact navigation, inputs, review invariants, fail-closed Fabric state machine, and remaining low-value acceptance test.
- The distinct `2.1.0-alpha.1` Fabric client polls the combined candidate feed, parses or lets the player override balance, applies a dynamic reserve, allocates within 20 order and 18 auction slots, and starts focused watches. Its Fabric-only executor presents a separate one-order arm screen, preserves cent-precise stack economics, verifies each chest/dialog transition, repeats freshness/budget/slot checks immediately before `Create Order`, and stops on any ambiguity. DonutSMP staff authorization for observer collection and locally armed Fabric order creation was explicitly confirmed by the operator on 2026-08-26.
- Fabric diagnostics are allowlisted, batched, rate-limited, retained for 14 days, enabled with a visible opt-out, and exclude personal/secret market context.
- Compose runs backend, collector manager, persistent data, and Caddy HTTPS termination. Loopback HTTP remains the development path.
- The second-PC overlay bounds backend and collector CPU, memory, and process counts, uses read-only container filesystems with explicit writable mounts/tmpfs, prevents privilege escalation, and rotates container logs. The backend receives a bounded 512 MiB temporary filesystem because SQLite's initial evidence-session backfill can exceed a small default temp area; its total memory ceiling is 1.5 GiB.

## Verified locally

- `go test ./...` and `go vet ./...` pass. Tests cover authenticated leases, idempotent scans, capture-only exclusion, fill/disappearance semantics, current-price freshness, shared-watch lifecycle, exact-quantity valuation, candidate executable volume, backups, scoped auth, API handlers, and diagnostic redaction.
- Node TypeScript build and 20 parser, navigation, proxy, configuration, and redaction tests pass; `npm audit --omit=dev` reports zero vulnerabilities.
- Fabric JUnit/build passes on Minecraft 1.21.11, Java 21, Loader 0.19.2, Fabric API 0.141.6, Loom 1.17.19, and Gradle 9.6.1.
- Candidate-frontier benchmark: approximately 135–204µs/op for 100 evidence rows on the local i5-11600K, with the upper result measured while the live backend and observer were running. Existing market benchmarks remain near or under roughly 2ms/op for their checked-in workloads.
- `docker compose config --quiet` validates with placeholder environment credentials. The Docker Desktop Linux daemon is not running, so containers could not be executed locally.
- Windows `go test -race` is unavailable because the local Go race build requires CGO. The normal suite is pure Go; race instrumentation remains a Linux CI/deployment-host gate.

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

A fresh post-executor pass found no further code-only improvement that outweighed the risk of inventing button or server-outcome behavior without a low-value live acceptance fixture.

## Shadow-mode limitations

- The verified schema enables only `/orders`, the exact refresh book, and the exact next-page arrow. Changed layouts remain capture-only.
- The current generic signature is item-ID based and deliberately marked modifier-incomplete. Real rows therefore remain research-only until stable order identity, modifier equivalence, queue position, and repeated fill evidence are proven.
- Only the configured HTTP CONNECT proxy has completed the live acceptance path; every additional proxy type/account still requires its own egress and login check.
- Local position inference is fail-closed until real success/failure message fixtures exist; used slots remain manually adjustable and no transaction or complete inventory data is uploaded.

## Next rollout gates

1. Add and bootstrap any additional observer accounts in separate permission-restricted profiles and verify each configured proxy egress.
2. Run multiple observers in shadow mode and measure overlap, disagreement, full-pass duration, and attainable focused-watch cadence.
3. Capture stable order identity/modifier/queue fields if Donut exposes them; keep actionability disabled if equivalence cannot be proven.
4. Calibrate observed quantity reductions against real fills and exact-quantity auction exits.
5. Enable Fabric `READY` notifications only after fill velocity, exact-quantity exits, and stability calibration pass.
