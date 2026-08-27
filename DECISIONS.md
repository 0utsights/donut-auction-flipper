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

Fabric owns the player's parsed or manually overridden balance (default `$10M`), inferred positions, available 20 order slots and 18 auction slots, dynamic 15–35% reserve, and final deterministic integer allocation. The allocator cannot exceed deployable cash, slot limits, conservative executable volume, or per-item exposure bounds. Personal portfolios and complete inventories are never uploaded.

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

## 2026-08-22 — Open-source reuse policy

The official Donut API schema remains the primary contract, and the retained in-repository API client is more defensive than the surveyed community clients. `cancel-cloud/Donuts-Auctions` is MIT-licensed and useful as a parser/reference implementation, but its automation features are not imported. GPL code from `RubyImpala/Glaze` is not copied into this differently licensed codebase. Projects without an explicit license are reference-only. See `docs/open-source-review.md`.

## 2026-08-25 — Conservative order evidence and capacity priority

Order-to-auction priority is `risk-adjusted profit/day × conservative attainable batches`. This paragraph's former 18-batch cap is superseded by the 2026-08-27 sequential-exit decision below: the backend publishes measured market capacity without applying a player balance or concurrent-listing cap.

Every stackable order-to-auction recommendation is a full maximum-stack batch because the player will relist the same quantity. It requires a qualifying auction cohort for that exact batch quantity; singular sales remain a conservative price ceiling inside the valuation model but are never a fallback recommendation. If no same-quantity exit evidence exists, the item remains absent from the executable frontier rather than appearing as a misleading `1x` trade. The current best order reward is treated as the price to beat by exactly one cent per unit, the smallest representable Donut reward increment, so the displayed queue position is a target position of one, not a claim that Donut exposes the true hidden queue. Candidate payloads expose both that exact per-unit reward and the gross exact-quantity auction list target; total acquisition and after-fee proceeds remain separate so neither the dashboard nor Fabric asks the player to reverse-engineer executable inputs from net economics.

Only a conservative allowlist of base commodities can be signature-complete and receive a priority rank. Any modifier marker, or an inherently variant-sensitive item such as equipment, books, potions, maps, heads, shulker boxes, or music discs, remains unranked research until a future canonical component parser proves equivalence. This deliberately trades market breadth for executable-price correctness.

Legacy inferred fill reductions are retained rather than erased, but have confirmation level zero and are excluded from scoring. Live verification showed that Donut's focused order rows do not expose owner names or usable order IDs, so requiring server identity made `READY` unreachable. A bounded level-one fill now requires the same observer, leased `focused_watch`, target signature, menu page, slot-derived stable key, requested quantity, and unit reward to decrease within two minutes. A server-visible identity upgrades the signal to level two. Discovery scans, disappearances, cross-page matches, long gaps, and neighboring items on a watched page remain quarantined. Five reductions across three stable keys are still required, and strict auction-side volume/stability gates remain unchanged.

Evidence scan counts represent independent menu sessions, not pages. Discovery uses one session across the full pagination traversal; every focused refresh uses a new session. Price stability compares the top reward for an item per session, preventing lower reward tiers on later pages from being mislabeled as market volatility. `research` requires three sessions spanning ten seconds; `actionable` requires five sessions, five bounded reductions across three keys, thirty seconds of evidence, stable pricing, and no conflict. This provides a roughly one-minute fast path when an actively filling item is reachable while remaining fail-closed when no reductions occur.

Outstanding quantity in competing buy orders does not imply that sellers will fill a newly created order. It is excluded from order-to-auction capacity. Until confirmed fill velocity exists, a research candidate receives at most one exploratory batch; confirmed velocity is required before capacity can scale.

`READY` additionally requires at least 50% exact-quantity auction confidence and a latest target-price sale no older than two hours. The 24-hour volume window remains useful for capacity, but does not by itself make older evidence executable.

Conservative profit applies the auction model's price-confidence haircut once. Risk-adjusted profit/day then applies completion probability once and divides by cycle duration. Keeping these terms separate matches the published formula and avoids double-discounting completion.

Player-requested focused watches preempt discovery after the currently submitted page. Automatic research cannot preempt before page 120, preventing the same few items from repeatedly starving the highest-priced portion of the market; at the measured cadence this reserves roughly two to three minutes of economically sorted breadth first. The backend signals this only on the observer's authenticated live discovery lease; the collector completes and rotates that passive task, then leases the higher-priority focused task. Player-focused work uses a 45-second bounded horizon and the request expires after one minute, which is sufficient for Fabric's six-second final check without letting one abandoned click monopolize the sole observer.

Completed discovery and focused-watch rotations reset reconnect failure backoff. The monotonic reconnect counter remains visible for diagnostics, but only unexpected short-lived connection or menu failures can increase the delay. This prevents a legitimately small order book from eventually imposing a 60-second idle penalty between successful passes.

## 2026-08-25 — Live decision page and auction-value research lane

The normal order-flipper page has no manual refresh control. It performs a small same-origin fetch once per second and replaces only its decision content when the candidate-feed version changes. The debug console remains separate and heavier; the normal player path does not fetch its database-wide evidence snapshot.

Order discovery continues to scan every verified global page because the server menu's ordering and search control have not been proven safe or stable enough to assume that page one contains every desirable item. When a refreshed candidate feed exposes a viable target—or after a complete discovery pass—the backend schedules a bounded two-minute sample for new research or a 150-second sample for proven profiles, after first covering the highest-priced 120 pages. It chooses among economically viable order-to-auction candidates by `READY`/`RESEARCH` tier, then risk-adjusted priority, conservative profit, and finally exact-quantity resale value. Automatic samples have a global one-minute minimum interval and rotate on a five-minute per-item cooldown. A player-requested watch is higher priority but bounded to 45 seconds. This creates an economic-priority lane without allowing raw price alone to monopolize research or clicking an unverified search UI.

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

Confirmed fill profiles are retained in compact aggregate tables. A market with at least five confirmed reductions across three order identities becomes a reusable profile. Profiles are permanent research hints, not permanent transaction approval: a known item receives a priority-75 focused revalidation lane after the highest-priced 120 discovery pages, with up to 150 seconds to reach and repeatedly sample its prior region. Fabric still requires current stable evidence, a fresh six-second focused price, current auction liquidity, and the exact duplicate/budget checks before creation. Maintaining the aggregate incrementally avoids rescanning 90 days of fill rows on every candidate refresh.

The 20-order-slot objective uses two readiness classes without relaxing item or price identity. CORE offers retain actionable order evidence, at least five near-target exact-stack auction sales from three sellers, and measured capacity. FILLER offers are currently observed, stable, modifier-safe, conservatively profitable markets backed by at least two near-target exact-stack sales from two sellers and at least 25% valuation confidence; they are capped at one resale stack until they graduate. Fabric maximizes distinct CORE/FILLER offers first. A player may manually cancel a filler and clear/reconcile its local lock when a stronger offer appears. Falling, volatile, stale, modifier-ambiguous, single-seller, or unprofitable markets never become fillers.

Candidate alerts do not create focused watches for the entire portfolio. Focused work begins only when the player opens or arms a row, and clearing tracked locks rechecks only the strongest new row. Player watches use a bounded 45-second collector horizon. This prevents one Fabric client from queueing twenty long watches and starving broad discovery.

Signature safety uses the latest classification from each observer inside the ten-minute evidence window. A newer parser result immediately supersedes the same observer's older conservative row, while any other observer whose latest result is incomplete still makes the item incomplete. This keeps disagreement fail-closed without forcing already-known items to wait ten minutes after successful revalidation.

Automatic profile revalidation does not interrupt the highest-per-item discovery lane before page 120. Live review showed the former priority-75 bypass repeatedly stopping discovery around page 36, then timing out after 20 seconds before reaching retained profiles around pages 90–100. The shared breadth floor refreshes economically important items first; the subsequent 150-second focused lane can then reach those profiles and measure repeated quantity changes. Player-requested watches still preempt immediately.

If Donut closes an automatic focused menu before its target is reached, that task completes into its normal per-item cooldown instead of immediately retrying from page one. This prevents one deep profile from monopolizing the sole observer during transient menu limits. A priority-100 player watch may retry within its short lifetime; discovery menu failures remain retryable because discovery is the source of broad coverage.

Repeated Mineflayer login failures back off exponentially to five minutes rather than retrying forever at one-minute intervals, allowing Donut's `Invalid sequence` session cooldown to expire. Normal transient failures still retry in seconds, and a runtime stable for five minutes resets the failure streak. This supersedes the earlier one-minute maximum reconnect paragraph.

Mineflayer is pinned to 4.38.0 with minecraft-protocol 1.68.0. This release sends the 1.21.4+ `player_loaded` acknowledgement immediately after spawn; the previous 4.37.1 runtime could reach Donut's transferred server and then be rejected with `Invalid sequence`. Protocol dependencies remain exact-pinned and upgrades require the collector test suite plus a live observation-only login check.

## 2026-08-27 — Replace broad traversal with a top-page opportunity lane

The sole Mineflayer observer no longer walks 120 or hundreds of globally sorted order pages. Donut's verified `Most Per Item` view is descending by unit reward, so discovery continuously refreshes page one and submits its 45 leading orders as independent samples. The completed-auction API remains authoritative for exact-stack exit value, near-target volume, seller diversity, confidence, volatility, and freshness; the order observer supplies the current competitive entry reward. Official Donut command metadata documents `/order <search...>` with `/orders` as an alias, so API-qualified canonical base items are queried through the primary command as one underscore-delimited argument such as `/order redstone_block`. Reserved/subcommand-like values, spaces, modifiers, namespaces other than `minecraft`, and multiple arguments are denied. Donut search is fuzzy: every displayed row must parse, but only exact canonical matches enter the target's evidence and fuzzy neighbors are ignored. The exact subset must retain descending `Most Per Item` order on page one. Automatic research may preempt immediately after page one, new/profile/manual samples are bounded to 15/8/8 seconds, successful task changes reuse the open Minecraft session, and automatic candidates can rotate after two seconds rather than an artificial one-minute interval. This prioritizes fast measurement of API-selected, high-value liquid exits without trusting search results optimistically or restoring global pagination. Source: https://github.com/donutdb/donutsmp-wiki/blob/main/wiki/Wikitext/Commands/commands.toml

## 2026-08-27 — Price-first exploration and balance-scaled exit-slot floor

The single observer keeps proven CORE markets current, then searches a bounded 24-item auction-API frontier ordered by conservative per-item value before revisiting replaceable FILLER rows. Liquidity, seller diversity, confidence, freshness, volatility, and modifier safety remain hard qualification gates; volume no longer multiplies price strongly enough for cheap niche commodities to crowd expensive safe items out of discovery. Modifier-blind equipment such as elytras and netherite gear remains non-actionable; fixed-identity high-value items such as netherite materials, dragon heads, nether stars, and upgrade templates may be researched.

Fabric enforces the scarce exit-listing economics because only Fabric knows the player's balance and live slots. Each exact batch is at most one stack (normally 64), and its conservative profit must meet a dynamic floor equal to 1% of deployable capital per currently free auction slot. The floor scales with balance and becomes stricter as the 18 slots fill; it is shown in the mod UI. The website applies the same calculation to its explicitly labeled $10M reference view, while player-specific decisions remain local. This rejects insignificant high-volume churn without imposing one fixed dollar threshold on every player.

Stack capacity is an upper bound, not a mandatory listing size. For each order item, the backend compares every 1–64 quantity supported by exact completed-auction evidence and publishes the quantity with the best confidence-adjusted profit per exit listing. This lets expensive stackable materials use affordable 1x or small-batch exits while cheaper commodities can still use profitable 64x exits. Fabric combines repeated exact batches into one bulk acquisition order and never relists a different quantity than the supporting auction valuation.

The second-PC collector is health-gated on the backend's real `/healthz` readiness response. It does not begin a Minecraft login while the API key is missing, startup valuation is incomplete, or the backend is otherwise unavailable. This avoids needless account reconnects during normal deployments.

## Assumptions made without operator input

- The official API retains its bearer-authenticated listing and transaction endpoints and 250 requests/minute published limit.
- Live verification found 44 entries per page and valid results beyond page 2,000. The normal scan deliberately caps at the newest 220 pages (9,680 listings) to keep detection near one minute and below the 250-request/minute key limit. It does not claim full-book coverage.
- `/ah <item>` remains a supported manual search command; there is no relied-upon direct-auction-ID command.
- The project operator explicitly confirmed on 2026-08-26 that DonutSMP staff authorize and lead this work. That authorization covers authenticated Mineflayer observers collecting order-market data and the Fabric client assisting with and executing explicitly armed player-side order creation. Mineflayer remains permanently observation-only and must never create, fulfill, cancel, claim, buy, list, transfer, or confirm an economic action.
- The live order menu may continue omitting owner/order IDs; bounded same-page focused reductions are therefore the strongest available volume signal unless the server exposes a stable identity later.
- The first combined-product execution goal is a single-candidate, locally armed Fabric order-creation workflow with fail-closed screen verification and a final freshness/budget check. Claiming, auction relisting, and repeated unattended execution remain deferred until their live workflows and failure states are captured and separately implemented.
- Loopback is the default deployment. Public binding without a downstream client token is rejected at startup.
