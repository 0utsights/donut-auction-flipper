# Architecture decisions

## 2026-08-22 — Preserve the old system before redesign

The complete pre-redesign repository was committed as `be6a624`, tagged `legacy-full-system-v0.4.0`, pushed to branch `archive/full-system-v0.4.0`, and stored in the private GitHub repository `0utsights/donut-auction-flipper`. The parser is not shipped now, but remains recoverable from that immutable tag and archive branch.

## 2026-08-23 — Permanent Mineflayer/Fabric boundary

Mineflayer is the permanent order-market observation layer. It parses order menus, accepts only research assignments, and submits observations; it never buys, fulfills, creates, confirms, cancels, claims, collects, lists, or transfers inventory. Auctions remain sourced from the official Donut API. Fabric is the player-facing flipping client: it receives scored candidates, keeps personal state locally, and assists with manual actions. Adapting Fabric code to run in Mineflayer is deferred.

Live player-client exploration established the order-creation flow documented in `docs/order-creation-workflow.md`. Any execution assistance belongs exclusively in Fabric. The initial executor is limited to one locally reviewed and explicitly armed order, revalidates the backend candidate and every server-driven screen, and may press the final `Create Order` action once. It cannot loop or arm another candidate. Mineflayer's permanent transaction prohibition is unchanged.

Collector coordination uses authenticated HTTP long polling rather than WebSockets. Any number of isolated account processes may register with the Go backend, receive renewable observation leases, and deliberately overlap work for verification. The backend cannot issue transaction instructions.

## 2026-08-22 — Backend-only upstream credential

`DONUT_API_KEY` exists only in the backend environment. It is excluded from Git, state files, logs, HTTP responses, debug HTML, Fabric resources, and mod configuration. The distributed mod only receives ranked flip records. A separate optional `DN_CLIENT_TOKEN` protects that downstream feed.

For Windows local development, `scripts/start-local.ps1` may cache the key as a user-scoped DPAPI ciphertext under the Git-ignored `data/` directory. This keeps the normal launch one-command without placing the plaintext key in source, mod files, command arguments, or shell history.

## 2026-08-23 — SQLite WAL for research state

Auction transaction history retains its bounded compressed archive, while order observations, fill evidence, observer health, leases, watches, diagnostics, and optimizer runs use embedded SQLite WAL storage. Submissions are idempotent, diagnostics expire after 14 days, and automated daily backups use SQLite's consistent `VACUUM INTO` path. This avoids premature external-database operations while giving multi-observer research durable transactional state.

## 2026-08-22 — Fresh immutable model per scan

Each successful cycle merges bounded transaction history, scans the active book, builds a fresh `robust-v2` engine, and atomically publishes a new snapshot. Readers never observe a half-updated market. If a scan fails, the last flip list remains visible but its status becomes `error`, and the Fabric client does not emit new alerts from it.

## 2026-08-22 — Conservative recommendations

Alerts use completed-sale evidence plus live asks, exact modifier-aware signatures, seller/day deduplication, MAD outlier filtering, short/long windows, liquidity, confidence, volatility, sell-time estimates, and manipulation/staleness flags. `api_modifier_blindspot` and `stale_references` block an alert. Active asks alone cannot establish value.

## 2026-08-23 — Quantity-preserving resale valuation

Every opportunity is anchored to completed quantity-one sales. Listings with quantity greater than one must also have enough completed sales at that exact quantity. The executable per-item reference is the lower of those two conservative models, multiplied by the unchanged listing quantity. Missing either cohort rejects the stack; the engine never counts profit that requires splitting a stack before resale.

## 2026-08-22 — Manual navigation only

The server supplies only a sanitized `/ah <item>` search command. The mod validates it again before rendering a click action or sending chat. It does not select slots, refresh the auction house, confirm purchases, automate buying, bypass anti-cheat, or attempt direct listing navigation that the public command surface cannot guarantee.

## 2026-08-23 — Seller-first auction navigation

The API provides a seller but no server-addressable listing ID; the apparent `auction_id` is our stable fingerprint and cannot support a Hypixel-style direct-open command. Alerts therefore expose `/ah <seller>` as the fastest primary route and `/ah <canonical_item_id>` as a fallback. Item searches use identifier paths such as `redstone_block`, never display-name spaces. Both commands remain locally validated and purchasing remains manual.

## 2026-08-23 — Split fast detection from broad valuation

A one-second 220-page scan is impossible under the upstream 250-request/minute limit. The API path is therefore split without adding workers or WebSockets: a fast loop polls recently-listed page 1 and publishes against the retained sale model, while the broad transaction/depth collector runs concurrently through the same 240-request/minute limiter. Local mod polling is 250ms. This prioritizes approximately sub-second new-listing detection while preserving broad evidence in one process.

## 2026-08-23 — Gate liquidity at the intended resale price

Total item sales can combine incompatible price regimes and do not prove that an item will clear at our proposed resale price. `robust-v4-price-volume` therefore counts completed 24-hour volume only inside a ±10% band around the quick-sell target for confidence, minimum-volume gating, and sell-time estimates. It also requires that qualifying target-price volume is not supplied by only one seller. Total 24-hour item volume remains visible as context but cannot qualify an alert. The active price cap uses the second-cheapest distinct seller, so two independent competing listings are treated as real market evidence while one bait listing cannot set the target alone.

## 2026-08-23 — Freeze auction-only; develop combined research independently

The verified API-only product is frozen at branch `codex/auction-only`, tag/release `auction-only-v1.0.0`. Auction-plus-orders development continues on `codex/auction-orders`. The official API has no order endpoints, so order evidence comes only from capture-only Mineflayer observers. No third-party tracker, simulated row, automatic purchase, or Fabric market upload is accepted as a substitute.

Combined ranking has no fixed dollar-profit floor. The backend publishes a bounded conservative frontier using exact-quantity evidence, fees, capital, completion probability, cycle time, executable volume, queue position, stability, and scarce market slots. A trade's score is `net profit × completion probability ÷ cycle days`. Profit per inventory slot remains visible as a tie-breaker.

## 2026-08-23 — Player state and allocation remain local

Fabric owns the player's parsed or manually overridden balance (default zero until observed), inferred positions, available 20 order slots and 18 auction slots, dynamic 15–35% reserve, and final deterministic integer allocation. The allocator cannot exceed deployable cash, slot limits, conservative executable volume, or per-item exposure bounds. Personal portfolios and complete inventories are never uploaded.

Position/outcome inference is fail-closed during shadow rollout. Until real sanitized server-message fixtures are captured, Fabric exposes manual used-slot state and does not treat menu navigation as evidence that a purchase, fill, or listing occurred.

## 2026-08-23 — Evidence graduation and unsafe-state semantics

Order markets graduate through `captured`, `research`, and `actionable`. Actionability requires repeated complete scans, observed reductions of stable order identities across sessions, current focused observations, and stable exact-quantity auction exits. Disappearance alone is ambiguous. Observer disagreement, schema uncertainty, stale evidence, modifier blindness, or price shocks produce `HOLD`, `STALE`, or `RESEARCH`, never an optimistic recommendation.

## 2026-08-23 — Collector isolation, routing, and secrets

One Node manager launches one child process per configured Microsoft account. Every account has an isolated token cache, stable observer identity, parser version, reconnect state, and dedicated authenticated proxy. Egress must match the configured address before Minecraft login. Token caches and proxy credentials remain in permission-restricted collector-host files and are never sent to the backend or Fabric.

The interaction adapter is deny-by-default. Only `/orders` plus controls explicitly verified by a captured schema may be used; unknown screens are capture-only. Mineflayer and protocol dependencies are pinned for the 1.21.11 compatibility baseline, but live authentication, signed-command, menu-schema, and proxy verification still require operator accounts and real server fixtures before navigation can be enabled.

## 2026-08-23 — Scoped credentials and sanitized diagnostics

Administrator, observer, and Fabric credentials use separate permission scopes and are stored as hashes in the running backend. The upstream Donut API key remains backend-only. Fabric keeps full logs locally and uploads only allowlisted, batched operational diagnostics by default, with a visible opt-out. Raw chat, usernames, server addresses, complete inventories, raw NBT, credentials, and secret-bearing API responses are forbidden; accepted diagnostics expire after 14 days.

## 2026-08-23 — Remote topology and shadow rollout

Production uses one Compose topology for the Go backend, Mineflayer manager, persistent storage, backups, and Caddy HTTPS termination on the second PC or Oracle host. Loopback HTTP is development-only. Collectors launch in shadow/capture mode, and combined `READY` recommendations remain unavailable until real order schemas, fill-rate calibration, observer agreement, and stability checks pass.

The initial second-PC move uses host-networked containers bound only to `127.0.0.1` plus an authenticated SSH local-forward from the player PC. Tailscale HTTPS certificates are not enabled on the tailnet, so this provides encrypted remote access without exposing backend HTTP on LAN or changing tailnet-wide settings. The standard Caddy HTTPS topology remains the production/public path once certificates are enabled.

Second-PC builds support independent `backend` and `collector` targets and retain untracked Go/npm dependency caches under `.second-pc-cache/`. Runtime containers, mounted secrets, and production data are unchanged; this only removes redundant compilation and downloads during validated service-specific deployments.

The constrained second-PC profile runs the official API newest-page lane every 500ms. This remains sub-second at the polling boundary while the fast and broad lanes share a conservative 225-request/minute client budget below the published 250 limit. A 429 applies one shared cooldown (honoring `Retry-After`) so both lanes recover together; the prior short independent retries could churn during a rolling-window collision. Raw scan rows retain 24 hours because current pricing uses two minutes and confirmed fills are stored separately for 90 days. Automatic SQLite backups are coalesced to the newest snapshot per UTC day for seven days, while manually named safety backups are never pruned. This prevents development restarts from multiplying several-hundred-megabyte copies without weakening daily recovery coverage.

The fast lane scores only the newest page it just fetched; the background broad pass remains responsible for the wider active book. Repeated identical listings refresh their liveness without rebuilding valuations, and an unchanged page republishes freshness from the previous immutable result instead of rescoring thousands of older listings.

A new ask that reaches or beats the current active reference is market-moving and triggers valuation immediately. Higher, non-moving depth is coalesced per item for up to five seconds; same-auction economic edits and removals remain immediate. This preserves low-price flip latency while bounding repeated valuation work on a busy newest page.

Retention deletes are intentionally committed in small batches and yield between batches. The child-row foreign key has a supporting `order_rows(scan_id)` index so cascades do not repeatedly scan the complete observation table. Maintenance therefore shares the single SQLite connection with live observer submissions instead of holding it for a database-wide transaction.

The two-minute evidence window and 24-hour fill window have timestamp-leading indexes. Candidate refreshes must seek directly into fresh rows; they may not scan the retained multi-million-row observation table on every collector page or API poll.

Historical order identities remain available to the research/debug store, but rows without a price observation in the current ten-minute trust window do not enter auction quantity valuation. They cannot produce a candidate and evaluating them on every refresh only consumes CPU.

Order-to-auction opportunities represent persistent market spreads rather than one-off underpriced listings. Ranking therefore trusts the latest usable order observation for ten minutes instead of thirty seconds. Stability still uses repeated observations, the competitive price comes from the newest session rather than the highest price anywhere in that window, and the official auction exit continues refreshing subsecond. Fabric's explicitly armed executor retains its separate six-second order check and requests a focused refresh before committing funds.

## 2026-08-22 — Barebones client and debug UI

The in-game screen and backend debug page are intentionally plain, information-first interfaces. The Fabric screen uses an opaque fill and does not call `renderBackground`, fixing the Minecraft 1.21.11 `Can only blur once per frame` crash. Styling remains deferred.

The default `/order-auction-flipper` page is a player decision surface, not an engineering console. It displays only `READY` order-to-auction opportunities, exact order and relist inputs, and a collapsed research queue clearly marked as not buyable. Collector operations, evidence, rejected routes, and legacy quarantine totals remain available at `/order-auction-flipper/debug` without cluttering the normal workflow.

## 2026-08-25 — Exact order-price cents and live menu schema

Mineflayer stores order rewards as integer cents (`unit_reward_cents`). This preserves Donut values such as `$0.01`, `$230.10`, and compact `$19.1K` prices without floating-point comparisons or silent rounding. The backend keeps auction/candidate totals in whole dollars: order acquisition costs round up only after multiplying cents by quantity, while expected order proceeds round down. This is conservative and prevents cheap stacked commodities from being inflated or discarded.

The first real 1.21.11 order fixture verified `Orders (Page N)`, listing slots `0–44`, the non-transactional `Filter` hopper at slot `47`, the `Orders` refresh book at slot `49`, and the `Next page` arrow at slot `53`. Only those exact fingerprints are enabled. Before submitting any scan, the collector cycles the three-state hopper at most three times and proves `Most Per Item` behavior from a fully parsed page whose per-item rewards are descending. Missing, partial, or non-descending evidence stops the task in schema hold. Server-confirmed discovery uses a 750ms click interval; focused watches retain a 500ms minimum interaction delay while treating requested freshness as the duration of the complete refresh cycle. Each discovery connection performs one pass and rotates at the real last page, an expired menu, or a 1,000-page runaway guard; the former 200-page cap was removed after the live market exceeded it. `/orders` opens that pass once. Listing rows, `Your Orders`, search, shop, create, deliver, and every unknown or changed control remain non-clickable. Parsed signatures remain modifier-incomplete and therefore research-only until stable order identity and modifier equivalence are proven.

Donut expires or closes the order GUI while leaving the Minecraft connection apparently alive, and live recovery showed that the resulting session can silently ignore later `/orders` commands. A closed or non-opening order menu therefore rotates the Minecraft connection before retrying. The isolated prismarine-auth cache remains mounted, so this does not repeat Microsoft device authorization or change the assigned proxy. Discovery completion also rotates the connection to avoid reusing stale navigation state.

Focused watches locate their assigned item only by traversing the same verified pagination control; they never type into a sign, invoke an unverified search, or click a listing. Remote collector-to-backend traffic requires HTTPS, while loopback HTTP remains permitted for development. Backend calls use explicit timeouts, reconnect backoff is based on rapid failures rather than the lifetime reconnect diagnostic, and a configured egress mismatch disables only the affected account.

Mineflayer mutates a window optimistically when `clickWindow` is called, before the server confirms a custom-menu page. The collector therefore requires a matching server `window_items` or `open_window` packet and verifies that `Orders (Page N)` advances before submitting another page. Observers are stationary, so after login transfers settle the pinned Mineflayer 4.37.1 mount-state path suspends its otherwise unavoidable idle movement updates. A live acceptance scan completed 364 consecutively confirmed pages (16,380 rows), rotated cleanly at the server-reported end, and resumed from page 1 without the prior `Invalid sequence` kick. A live focused-watch test paginated to its assigned item, refreshed that page repeatedly, and now rotates before returning to discovery so a later full scan cannot inherit a partial page position.

Live scans later showed that Donut may send `window_items` followed by additional `set_slot` packets for one navigation. Treating the first packet as final produced inconsistent false endings at page 10 while equivalent passes reached more than 50 pages. Navigation now waits for the relevant packet burst to become quiet, ignores packets for other windows, and fails immediately if the active window closes. The collector still verifies the page number after the settled server update; this does not relax the deny-by-default click policy.

Legacy SQLite `unit_reward` values remain quarantined as whole-dollar development data. Exact observations write to a new `unit_reward_cents` column, and only positive values from that column enter evidence or candidate economics. The system does not guess a conversion for mixed historical rows.

## 2026-08-22 — Compatibility baseline

The mod targets Minecraft 1.21.11, Java 21, Fabric API 0.141.6, and Fabric Loader 0.19.2 or newer to match the current Prism instance. Fabric Loom 1.17.19 requires Gradle 9.5 or newer for builds; Gradle 9.7 is used in CI.

## 2026-08-29 — Fabric-only order claims and auction exits

Fabric may run a second, explicitly authorized session that claims completed tracked orders and creates their auction exits. Claim, packaging, purchase, confirmation, inventory transfer, block placement/breaking, and listing actions remain absent from Mineflayer. The collector boundary is permanent: if a future Fabric workflow is adapted to Mineflayer, any order that needs shulker acquisition or packaging must be rejected before acquisition because that physical inventory workflow cannot be made reliable in the observer. Mineflayer never buys such an order and remains observation-only.

The durable client position records delivered, claimed, packaged, and listed quantities independently. Progress is monotonic and persisted after each verified server-side result. A resumed session may continue only from those verified boundaries; ambiguous state transitions put the item in `HOLD` and require manual reconciliation. Claiming a full order frees its local order slot, and only a listing proven exactly once in `Your Items` consumes a local auction slot.

`CLAIM_PENDING`, `PACKAGE_PENDING`, and `LISTING_PENDING` are persisted before their corresponding irreversible action. If the process stops before the observable receipt advances that marker, restart cannot determine whether the action reached the server and therefore converts the position to `HOLD` instead of replaying it.

Exit packaging uses physical inventory slots, `ceil(total quantity / maximum stack size)`. Fewer than 27 physical slots produce sequential unchanged-quantity listings. At 27 or more slots, Fabric divides the position into shulkers of at most 27 physical stacks, preserving the exact total quantity. Each package derives its gross target proportionally from the API-proven candidate exit and undercuts that target by $1,000. The player still needs enough free inventory for one package at a time and must look at a solid block with an empty adjacent space.

Empty shulker supply comes from a Fabric-scoped backend endpoint built only from current official-API listings for one plain, generic, empty shulker. Fabric accepts only fresh, price-sorted, unique quotes, then independently verifies the live `Lowest Price` auction row, its generic registry ID, empty container component, price, and a balance-scaled spend cap before buying one box at a time. Named, colored, filled, enchanted, or otherwise modified containers do not qualify.

Live capture established `Orders -> Your Orders`, an exact completed row, `Orders -> Edit Order`, the completed identity at slot 10, and `Collect` at slot 13. The exit executor requires that exact item, requested quantity, completion progress, and rounded unit-price bucket in both screens. Automatic exit is initially restricted to vanilla signatures; enchanted, trimmed, named, damaged, container-bearing, or otherwise modifier-sensitive positions stop in `HOLD` rather than relying on item ID alone.

## 2026-08-22 — Open-source reuse policy

The official Donut API schema remains the primary contract, and the retained in-repository API client is more defensive than the surveyed community clients. `cancel-cloud/Donuts-Auctions` is MIT-licensed and useful as a parser/reference implementation, but its automation features are not imported. GPL code from `RubyImpala/Glaze` is not copied into this differently licensed codebase. Projects without an explicit license are reference-only. See `docs/open-source-review.md`.

## 2026-08-25 — Conservative order evidence and capacity priority

Order-to-auction priority is `risk-adjusted profit/day × conservative attainable batches`. This paragraph's former 18-batch cap is superseded by the 2026-08-27 sequential-exit decision below: the backend publishes measured market capacity without applying a player balance or concurrent-listing cap.

Stackable order-to-auction recommendations use the most profitable API-proven exact exit quantity from one item through the item's maximum stack size. This avoids forcing rare or expensive materials into unsupported 64-item exits while still requiring a qualifying auction cohort for the exact quantity that will be relisted. Singular sales remain a conservative ceiling for larger batches, never substitute evidence for them. The current best order reward is treated as the price to beat by exactly one cent per unit, the smallest representable Donut reward increment, so the displayed queue position is a target position of one, not a claim that Donut exposes the true hidden queue. Candidate payloads separate the gross list target (the robust completed-sale clearing price, capped by current independent competition) from conservative expected proceeds (the quick-sale haircut after fees). Profit and priority use only the conservative proceeds, preventing a correct player-facing list price from making the risk model optimistic.

Only a conservative allowlist of base commodities can be signature-complete and receive a priority rank. Any modifier marker, or an inherently variant-sensitive item such as equipment, books, potions, maps, heads, shulker boxes, or music discs, remains unranked research until a future canonical component parser proves equivalence. This deliberately trades market breadth for executable-price correctness.

Legacy inferred fill reductions are retained rather than erased, but have confirmation level zero and are excluded from scoring. Live verification showed that Donut's focused order rows do not expose owner names or usable order IDs, so requiring server identity made `READY` unreachable. A bounded level-one fill now requires the same observer, leased `focused_watch`, target signature, menu page, slot-derived stable key, requested quantity, and unit reward to decrease within two minutes. A server-visible identity upgrades the signal to level two. Discovery scans, disappearances, cross-page matches, long gaps, and neighboring items on a watched page remain quarantined. Five reductions across three stable keys are still required, and strict auction-side volume/stability gates remain unchanged.

Evidence scan counts represent independent menu sessions, not pages. Discovery uses one session across the full pagination traversal; every focused refresh uses a new session. Price stability compares the top reward for an item per session, preventing lower reward tiers on later pages from being mislabeled as market volatility. `research` requires three sessions spanning ten seconds; `actionable` requires five sessions, five bounded reductions across three keys, thirty seconds of evidence, stable pricing, and no conflict. This provides a roughly one-minute fast path when an actively filling item is reachable while remaining fail-closed when no reductions occur.

Outstanding quantity in competing buy orders does not imply that sellers will fill a newly created order. It is excluded from order-to-auction capacity. Until confirmed fill velocity exists, a research candidate receives at most one exploratory batch; confirmed velocity is required before capacity can scale.

`READY` additionally requires at least 50% exact-quantity auction confidence and a latest target-price sale no older than two hours. The 24-hour volume window remains useful for capacity, but does not by itself make older evidence executable.

Conservative profit applies the auction model's price-confidence haircut once. Risk-adjusted profit/day then applies completion probability once and divides by cycle duration. Keeping these terms separate matches the published formula and avoids double-discounting completion.

Superseded by the 2026-08-27 top-page opportunity lane below: focused work may now preempt after the verified first page, and the collector no longer traverses 120 discovery pages. This paragraph is retained only as the historical reason the starvation guard existed.

Completed discovery and focused-watch rotations reset reconnect failure backoff. The monotonic reconnect counter remains visible for diagnostics, but only unexpected short-lived connection or menu failures can increase the delay. This prevents a legitimately small order book from eventually imposing a 60-second idle penalty between successful passes.

## 2026-08-25 — Live decision page and auction-value research lane

The normal order-flipper page has no manual refresh control. It performs a small same-origin fetch once per second and replaces only its decision content when the candidate-feed version changes. The debug console remains separate and heavier; the normal player path does not fetch its database-wide evidence snapshot.

Superseded by the 2026-08-27 top-page opportunity lane and the 2026-08-29 cadence decision below. Discovery now continuously verifies and refreshes the global `Most Per Item` first page, while API-ranked canonical items use bounded direct searches. This paragraph is retained only as historical context for the earlier broad-traversal design.

## 2026-08-27 — Bulk order portfolios and duplicate prevention

Fabric treats each backend candidate quantity as one exact auction resale batch, but combines all locally allocated batches for an item into one acquisition order. For example, 20,000 units create one buy order and 313 sequential exit listings of at most 64 units. Those exits reuse the account's 18 auction slots as they sell; they do not consume 313 simultaneous slots. The allocator admits only `ORDER_TO_AUCTION` candidates, deduplicates by canonical item ID, and never allocates an item already tracked as active.

The backend never receives the player's balance and publishes the complete conservative 24-hour capacity. Fabric alone chooses order size from its local balance, dynamic cash reserve, session budget, conservative executable volume, and a 25% per-item exposure cap. It first maximizes the number of distinct profitable offers up to the 20-order limit, then assigns remaining capital by risk-adjusted profit/day per dollar. Existing auction-slot use remains visible for exit planning but does not incorrectly cap a pending acquisition order whose fills and listings occur over time.

The client solves distinct-item activation with a deterministic Pareto frontier over cash and remaining order slots. Quantity expansion is a bounded linear allocation over the activated items, so million-unit capacities do not require one optimizer iteration per 64-item batch. It has no wall-clock cutoff, so client load cannot silently change the portfolio.

Submitted item IDs are local player state and persist across restarts. Before opening the transactional creation wizard, Fabric inspects the verified `Orders -> Your Orders` container for an exact registry-ID match and aborts if the item already exists. Direct duplicate-order server messages also fail closed and lock the affected item. `Reset order cache` clears only the local lock; it cannot bypass the personal-order menu check. This supports intentional recreation after a fill or cancellation without making a stale local state capable of creating duplicates.

The conservative base-commodity set includes observed plain bulk markets such as iron blocks, sponges, blue ice, breeze rods, hoppers, and gilded blackstone. Modifier-bearing observations still fail signature completion, and inherently variant-sensitive equipment, containers, heads, maps, potions, books, and music discs remain excluded.

Persistent order spreads retain robust price-stability research for one hour, allowing one observer to rotate across a broad portfolio without erasing earlier evidence. Stability uses the central 80% of at least ten session prices so one short-lived top-order spike cannot poison an otherwise steady market; sparse samples and sustained movement remain fail-closed. Signature completeness uses a separate ten-minute window so a newly hardened parser can replace older conservative classifications promptly. General discovery and focused freshness are stored separately: a later discovery page cannot refresh the execution timestamp of an older top price. A focused refresh tracks the latest target price rather than the session maximum, and that focused price temporarily replaces research pricing in candidate economics. Fabric requires an explicitly focused observation within six seconds before the final Create Order action. For backward safety, the legacy `order_fresh_at` feed field also carries focused freshness; `research_fresh_at` is the separate long-lived research timestamp.

The executable auction-liquidity model is `robust-v5-target-liquidity-quantity`. Because acquired stacks are relisted intact, volume, seller diversity, and target-price freshness come only from completed sales of that exact stack quantity near the final conservative resale price. Singular sales remain a conservative per-unit price ceiling but cannot cap or inflate executable stack volume. A five-sale/day exit may remain current for two expected inter-sale intervals (bounded to 2–12 hours), replacing the contradictory fixed two-hour rule. Exact-quantity confidence of at least 35%, five nearby sales, and three distinct nearby sellers are required together; confidence and completion still haircut ranking and exposure. This supersedes the earlier 50%/fixed-two-hour paragraph above.

Persisted active-order item locks are a lower bound on used order slots. If server discovery or a duplicate-order response adds an item that the local counter missed, Fabric immediately raises its used-slot count before reallocating. It never creates a second order for that canonical item; intentional recreation requires the old server order to be gone and the local cache to be explicitly reset.

Microsoft/Xbox/Minecraft token acquisition is explicitly wrapped with the account's assigned proxy. The wrapper is process-local to that isolated observer child and is removed as soon as `minecraft-protocol` emits its authenticated session, before any localhost backend call. The Minecraft socket and Mojang session-server verification continue using the same proxy agent. This closes the gap where passing an HTTP agent to `minecraft-protocol` did not affect `prismarine-auth`'s global fetch implementation.

Confirmed fill profiles are retained in compact aggregate tables. A market with at least five confirmed reductions across three order identities becomes a reusable profile. Profiles are permanent research hints, not permanent transaction approval: a known item receives a priority-75 direct-search revalidation lane after the verified first discovery page. Fabric still requires current stable evidence, a fresh focused price, current auction liquidity, and the exact duplicate/budget checks before creation. Maintaining the aggregate incrementally avoids rescanning 90 days of fill rows on every candidate refresh.

The 20-order-slot objective uses two readiness classes without relaxing item or price identity. CORE offers retain actionable order evidence, at least five near-target exact-stack auction sales from three sellers, and measured capacity. FILLER offers are currently observed, stable, modifier-safe, conservatively profitable markets backed by at least two near-target exact-stack sales from two sellers and at least 25% valuation confidence; they are capped at one resale stack until they graduate. Fabric maximizes distinct CORE/FILLER offers first. A player may manually cancel a filler and clear/reconcile its local lock when a stronger offer appears. Falling, volatile, stale, modifier-ambiguous, single-seller, or unprofitable markets never become fillers.

Candidate alerts do not create focused watches for the entire portfolio. Focused work begins only when the player opens or arms a row, and clearing tracked locks rechecks only the strongest new row. Player watches use a bounded 45-second collector horizon. This prevents one Fabric client from queueing twenty long watches and starving broad discovery.

Signature safety uses the latest classification from each observer inside the ten-minute evidence window. A newer parser result immediately supersedes the same observer's older conservative row, while any other observer whose latest result is incomplete still makes the item incomplete. This keeps disagreement fail-closed without forcing already-known items to wait ten minutes after successful revalidation.

Superseded by the direct-search lane below. Automatic profile revalidation waits only for a complete, verified `Most Per Item` first-page submission; it no longer walks toward an item's historical page. Player-requested watches remain the highest-priority bounded task.

If Donut closes an automatic focused menu before its target is reached, that task completes into its normal per-item cooldown instead of immediately retrying from page one. This prevents one deep profile from monopolizing the sole observer during transient menu limits. A priority-100 player watch may retry within its short lifetime; discovery menu failures remain retryable because discovery is the source of broad coverage.

Repeated Mineflayer login failures back off exponentially to five minutes rather than retrying forever at one-minute intervals, allowing Donut's `Invalid sequence` session cooldown to expire. Normal transient failures still retry in seconds, and a runtime stable for five minutes resets the failure streak. This supersedes the earlier one-minute maximum reconnect paragraph.

Mineflayer is pinned to 4.38.0 with minecraft-protocol 1.68.0. This release sends the 1.21.4+ `player_loaded` acknowledgement immediately after spawn; the previous 4.37.1 runtime could reach Donut's transferred server and then be rejected with `Invalid sequence`. Protocol dependencies remain exact-pinned and upgrades require the collector test suite plus a live observation-only login check.

## 2026-08-27 — Replace broad traversal with a top-page opportunity lane

The sole Mineflayer observer no longer walks 120 or hundreds of globally sorted order pages. Donut's verified `Most Per Item` view is descending by unit reward, so discovery continuously refreshes page one and submits its 45 leading orders as independent samples. The completed-auction API remains authoritative for exact-stack exit value, near-target volume, seller diversity, confidence, volatility, and freshness; the order observer supplies the current competitive entry reward. Official Donut command metadata documents `/order <search...>` with `/orders` as an alias, so API-qualified canonical base items are queried through the primary command as one underscore-delimited argument such as `/order redstone_block`. Reserved/subcommand-like values, spaces, modifiers, namespaces other than `minecraft`, and multiple arguments are denied. Donut search is fuzzy: every displayed row must parse, but only exact canonical matches enter the target's evidence and fuzzy neighbors are ignored. The exact subset must retain descending `Most Per Item` order on page one. Automatic research may preempt immediately after page one; player-requested, proven-profile, and exploratory samples are bounded to 3, 4, and 8 seconds respectively. Successful task changes reuse the open Minecraft session. This prioritizes fast measurement of API-selected, high-value liquid exits without trusting search results optimistically or restoring global pagination. Source: https://github.com/donutdb/donutsmp-wiki/blob/main/wiki/Wikitext/Commands/commands.toml

Order price abbreviations are treated as lower-bound buckets, not exact bids. Fabric starts one cent above the observed bucket and freezes the backend's conservative next-bucket boundary as the consent-time maximum. It must prove `Most Per Item`, find its exact unfilled row on the public canonical search, and accept only rank one. A lower rank may cancel and replace through the exact `Orders -> Edit Order` screen only after canonical item, quantity, visible price bucket, and zero-delivered progress all match uniquely. The cancelled row must disappear and the scoreboard must confirm its refund. Each higher bid is rechecked against the frozen escrow and conservative exit-profit floor; a bid outside either bound is dropped. This keeps price discovery player-side in Fabric, prevents Mineflayer transactions, and avoids blindly paying a bucket ceiling when one cent already wins.

## 2026-08-27 — Price-first exploration and balance-scaled exit-slot floor

The single observer keeps proven CORE markets current, then searches a bounded 60-item auction-API frontier ordered by conservative per-item value before revisiting replaceable FILLER rows. Proven/READY rechecks use a one-minute cooldown; unproven and FILLER exploration uses ten minutes, preventing the same failures from starving broader research. Exploratory samples are bounded to eight seconds and absent markets complete immediately. Liquidity, seller diversity, confidence, freshness, volatility, and modifier safety remain hard qualification gates; volume no longer multiplies price strongly enough for cheap niche commodities to crowd expensive safe items out of discovery. Modifier-blind equipment such as elytras and netherite gear remains non-actionable; fixed-identity high-value items such as netherite materials, dragon heads, nether stars, and upgrade templates may be researched.

Superseded by the 2026-08-29 allocator decision below. Fabric still shows the balance-scaled profit-per-exit target, but it is a ranking target rather than a hard eligibility cutoff; positive affordable READY markets may fill otherwise unused order slots.

## 2026-08-27 — Sidebar balance is authoritative; backend has no reference balance

The backend publishes the complete market-ranked READY and RESEARCH frontier without a `$10M` profit floor or reference portfolio. Fabric samples the Donut sidebar once per second and parses its standalone currency line, including `K`, `M`, `B`, and `T` suffixes, then performs all balance, reserve, exposure, and slot allocation locally. The default is zero until a balance is observed; clearly labeled chat and manual adjustment remain fallbacks when the sidebar is unavailable. The former fixed `$10M` order-session budget is removed because the live local balance, reserve, active positions, and explicit one-order arming checks are the actual spending constraints.

## 2026-08-27 — Automatic order creation requires session consent and post-submit proof

Automatic economic actions remain Fabric-only and default off. A dedicated confirmation screen can authorize sequential creation of the current local portfolio for one Minecraft session. The existing fail-closed wizard is reused one item at a time; every item receives a fresh focused order observation, current auction valuation, duplicate check, exact item/amount/price/total validation, and final allocation check. After `Create Order`, Fabric must reopen `Your Orders` and find exactly one matching canonical item before advancing. Any disagreement, unknown screen, missing or duplicate result, server error, timeout, or disconnect disables the queue and preserves the local item lock. Mineflayer remains permanently observation-only.

The Fabric interface uses a restrained blue/black card system adapted from the operator's ClansQOL information hierarchy: one centered console, status-first metrics, compact market rows, and secondary local controls on their own screen. Transaction controls must never rely on disabled-button styling alone; the exact readiness blocker is visible in the dashboard and consent screen, and the blocked consent control remains clickable to repeat that explanation without bypassing executor validation.

Minecraft's 1.21.11 sidebar renders each row from a decorated name and a separately formatted score. Balance observation therefore composes those two fields exactly as the HUD does before applying the strict currency parser; inspecting each field independently misses Donut's `$` plus `134M` row. Candidate validation accepts any positive exact resale quantity no larger than the canonical maximum stack size. Equality with the maximum is explicitly not required because the backend selects the strongest API-proven exact exit quantity and currently publishes valid 1x, 16x, 32x, and 64x markets.

The HUD may select a team-color-specific sidebar objective before falling back to the ordinary `SIDEBAR` objective. Fabric mirrors that selection exactly; reading only the fallback can leave the visible balance at `$142M` while the allocator remains on its manual value. A focused watch may also replace an armed candidate's price or exact exit quantity before any server navigation begins. In automatic mode only, Fabric may rebase that queued entry to the latest READY allocation for the identical canonical signature, provided the focused observation is current and total escrow remains within the amount frozen at session consent. If it cannot, the client waits for the bounded refresh window and skips that one untouched entry rather than disabling every unrelated queued market. Once server navigation begins, any changed economics or unknown screen still stops the full queue.

Donut's free-order placeholder is recognized only when it is a known stained-glass placeholder whose visible label explicitly says create, new, empty, or available order. A hopper or merely decorative pane is never accepted as a create control. If no verified placeholder exists, Fabric performs no transaction and records a bounded item/label fingerprint in the full local Minecraft log so the live schema can be corrected without uploading inventory or NBT.

Minecraft 1.21.11 can render a server `TextInputControl` as either a normal `TextFieldWidget` or an `EditBoxWidget` when the dialog descriptor carries its multiline shape—even when Donut uses it as a one-line search field. Fabric accepts exactly one of those two vanilla implementations only after separately proving that the dialog declares exactly one text input and its key or label matches the expected search, amount, or price role. Supporting the second vanilla widget does not relax dialog-title, field-count, label, result, or review-value verification.

Stack capacity is an upper bound, not a mandatory listing size. For each order item, the backend compares every 1–64 quantity supported by exact completed-auction evidence and publishes the quantity with the best confidence-adjusted profit per exit listing. This lets expensive stackable materials use affordable 1x or small-batch exits while cheaper commodities can still use profitable 64x exits. Fabric combines repeated exact batches into one bulk acquisition order and never relists a different quantity than the supporting auction valuation.

Signature-completeness evidence lasts for the same one-hour window as its order observation so a broad direct-search rotation does not erase valid markets after ten minutes. Every stored row records the collector parser version, and only rows matching that observer's currently registered version can prove completeness. A parser rollout therefore invalidates old proof immediately and clears completed automatic cooldowns so the new parser starts rebuilding the frontier at once.

The second-PC collector is health-gated on the backend's real `/healthz` readiness response. It does not begin a Minecraft login while the API key is missing, startup valuation is incomplete, or the backend is otherwise unavailable. This avoids needless account reconnects during normal deployments.

Once connected to Minecraft, a temporary backend outage is a control-plane failure, not a reason to discard the authenticated game session. Task polling backs off from one to thirty seconds, re-registers when possible, and resumes on the existing Mineflayer connection. Transactional actions remain impossible and an interrupted task lease expires safely on the backend.

Donut's abbreviated order rewards are display buckets, not exact prices. The collector records both the displayed lower value and the next representable display boundary: for example, `$1.3M` requires a `$1.4M` first-place bid and `$19.1K` requires `$19.2K`; an unabbreviated exact value still uses a one-cent increment. The backend selects exit quantity and recomputes escrow, margin, and rank from that competitive boundary. Legacy observations without a parser-supplied boundary are excluded from live economics, and an unprofitable boundary-crossing candidate is rejected. Queue position one is therefore an execution target based on a fresh focused scan, not a claim that a historical rounded value proved the player's rank.

Donut may localize its server-driven item-selection dialogs independently of the Minecraft language setting. Fabric recognizes only an explicit English/French allowlist for workflow titles and action labels and still requires the exact dialog type, one role-matching text input, exact result count, and later economic review checks. A Donut result label's bracketed `item/<registry-path>@items` metadata is authoritative across localized human labels; an absent or different canonical path remains fail-closed. Conditional candidate-feed HTTP success, rather than an unchanged payload timestamp, proves transport freshness; focused-order and auction evidence retain their separate strict age checks.

The balance-scaled profit-per-exit value is a preferred frontier, not a hard eligibility cutoff. Fabric first maximizes distinct positive, affordable READY items across unused order slots, then expands quantities within cash and exposure bounds; below-target rows are temporary fillers that can be replaced when stronger markets qualify. This supersedes the earlier hard 1%-per-auction-slot rejection because it could reduce a `$181M` player with nineteen free order slots and nine profitable server candidates to one local selection.

Order-price presentation keeps three values distinct: the literal abbreviated `/orders` observation, Fabric's one-cent-higher initial bid, and the conservative upper bucket reserved for repricing and budget/profit checks. Mineflayer cannot recover hidden digits from `$1.3M`; rank feedback in Fabric is the exact-price discovery mechanism. To reduce observer overhead, a globally verified `Most Per Item` sort is reused for at most sixty seconds within the same isolated Minecraft session while every filtered result still proves page one, exact canonical identity, completeness, and descending reward. Reconnects invalidate the proof. Proven/READY markets use shorter samples and a one-minute recheck cooldown; unproven research retains longer sampling and a ten-minute cooldown.

Fabric item creation accepts Donut's fuzzy multi-result chooser only when exactly one visible, active button carries the expected canonical bracket metadata. Item and block result forms such as `[item/breeze_rod@items]` and `[block/ice]` are supported; localized display names are never allowed to override a mismatched registry path. Zero or multiple canonical matches remain fail-closed. This lets an `ice` search select Ice while rejecting Packed Ice, Blue Ice, Frosted Ice, and unrelated fuzzy results.

Explicit automatic-order consent enables a continuous in-memory session, not a one-time candidate snapshot. The session may start with zero READY allocations and waits without turning itself off; whenever Fabric is idle it reconciles the latest local portfolio and queues newly eligible canonical items up to the currently free order slots. Every new queue entry freezes its own current allocation capital as the maximum escrow, then runs the complete fresh-market, duplicate, exact-item, rank, cancellation, and refund checks. A changed or dropped candidate is cooled down for one minute so an unchanged failure cannot create a transaction loop. The session ends only when the player selects Stop Auto, disconnects, or a fail-closed workflow error disables it.

## Assumptions made without operator input

- The official API retains its bearer-authenticated listing and transaction endpoints and 250 requests/minute published limit.
- Live verification found 44 entries per page and valid results beyond page 2,000. The normal scan deliberately caps at the newest 220 pages (9,680 listings) to keep detection near one minute and below the 250-request/minute key limit. It does not claim full-book coverage.
- `/ah <item>` remains a supported manual search command; there is no relied-upon direct-auction-ID command.
- The project operator explicitly confirmed on 2026-08-26 that DonutSMP staff authorize and lead this work. That authorization covers authenticated Mineflayer observers collecting order-market data and the Fabric client assisting with and executing explicitly armed player-side order creation. Mineflayer remains permanently observation-only and must never create, fulfill, cancel, claim, buy, list, transfer, or confirm an economic action.
- The live order menu may continue omitting owner/order IDs; bounded same-page focused reductions are therefore the strongest available volume signal unless the server exposes a stable identity later.
- The current combined-product execution goal is explicitly authorized, session-scoped Fabric order creation plus the separately authorized completed-order exit session. The 2026-08-29 capture and implementation supersede the former assumption that claiming and relisting were deferred; modifier-sensitive exits and legacy pre-alpha28 positions remain manual.
- Loopback is the default deployment. Public binding without a downstream client token is rejected at startup.
