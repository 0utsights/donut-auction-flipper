# Architecture decisions

## 2026-08-22 — Preserve the old system before redesign

The complete pre-redesign repository was committed as `be6a624`, tagged `legacy-full-system-v0.4.0`, pushed to branch `archive/full-system-v0.4.0`, and stored in the private GitHub repository `0utsights/donut-auction-flipper`. The parser is not shipped now, but remains recoverable from that immutable tag and archive branch.

## 2026-08-23 — Permanent Mineflayer/Fabric boundary

Mineflayer is the permanent order-market observation layer. It parses order menus, accepts only research assignments, and submits observations; it never buys, fulfills, creates, confirms, cancels, claims, collects, lists, or transfers inventory. Auctions remain sourced from the official Donut API. Fabric is the player-facing flipping client: it receives scored candidates, keeps personal state locally, and assists with manual actions. Adapting Fabric code to run in Mineflayer is deferred.

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

## 2026-08-22 — Barebones client and debug UI

The in-game screen and backend debug page are intentionally plain, information-first interfaces. The Fabric screen uses an opaque fill and does not call `renderBackground`, fixing the Minecraft 1.21.11 `Can only blur once per frame` crash. Styling remains deferred.

The default `/order-auction-flipper` page is a player decision surface, not an engineering console. It displays only `READY` order-to-auction opportunities, exact order and relist inputs, and a collapsed research queue clearly marked as not buyable. Collector operations, evidence, rejected routes, and legacy quarantine totals remain available at `/order-auction-flipper/debug` without cluttering the normal workflow.

## 2026-08-25 — Exact order-price cents and live menu schema

Mineflayer stores order rewards as integer cents (`unit_reward_cents`). This preserves Donut values such as `$0.01`, `$230.10`, and compact `$19.1K` prices without floating-point comparisons or silent rounding. The backend keeps auction/candidate totals in whole dollars: order acquisition costs round up only after multiplying cents by quantity, while expected order proceeds round down. This is conservative and prevents cheap stacked commodities from being inflated or discarded.

The first real 1.21.11 order fixture verified `Orders (Page N)`, listing slots `0–44`, the non-transactional `Filter` hopper at slot `47`, the `Orders` refresh book at slot `49`, and the `Next page` arrow at slot `53`. Only those exact fingerprints are enabled. Before submitting any scan, the collector cycles the three-state hopper at most three times and proves `Most Per Item` behavior from a fully parsed page whose per-item rewards are descending. Missing, partial, or non-descending evidence stops the task in schema hold. Server-confirmed discovery uses a 750ms click interval; focused watches use a separate 500ms minimum plus their requested freshness delay. Each discovery connection performs one pass and rotates at the real last page, an expired menu, or a 1,000-page runaway guard; the former 200-page cap was removed after the live market exceeded it. `/orders` opens that pass once. Listing rows, `Your Orders`, search, shop, create, deliver, and every unknown or changed control remain non-clickable. Parsed signatures remain modifier-incomplete and therefore research-only until stable order identity and modifier equivalence are proven.

Focused watches locate their assigned item only by traversing the same verified pagination control; they never type into a sign, invoke an unverified search, or click a listing. Remote collector-to-backend traffic requires HTTPS, while loopback HTTP remains permitted for development. Backend calls use explicit timeouts, reconnect backoff is based on rapid failures rather than the lifetime reconnect diagnostic, and a configured egress mismatch disables only the affected account.

Mineflayer mutates a window optimistically when `clickWindow` is called, before the server confirms a custom-menu page. The collector therefore requires a matching server `window_items` or `open_window` packet and verifies that `Orders (Page N)` advances before submitting another page. Observers are stationary, so after login transfers settle the pinned Mineflayer 4.37.1 mount-state path suspends its otherwise unavoidable idle movement updates. A live acceptance scan completed 364 consecutively confirmed pages (16,380 rows), rotated cleanly at the server-reported end, and resumed from page 1 without the prior `Invalid sequence` kick. A live focused-watch test paginated to its assigned item, refreshed that page repeatedly, and now rotates before returning to discovery so a later full scan cannot inherit a partial page position.

Legacy SQLite `unit_reward` values remain quarantined as whole-dollar development data. Exact observations write to a new `unit_reward_cents` column, and only positive values from that column enter evidence or candidate economics. The system does not guess a conversion for mixed historical rows.

## 2026-08-22 — Compatibility baseline

The mod targets Minecraft 1.21.11, Java 21, Fabric API 0.141.6, and Fabric Loader 0.19.2 or newer to match the current Prism instance. Fabric Loom 1.17.19 requires Gradle 9.5 or newer for builds; Gradle 9.7 is used in CI.

## 2026-08-22 — Open-source reuse policy

The official Donut API schema remains the primary contract, and the retained in-repository API client is more defensive than the surveyed community clients. `cancel-cloud/Donuts-Auctions` is MIT-licensed and useful as a parser/reference implementation, but its automation features are not imported. GPL code from `RubyImpala/Glaze` is not copied into this differently licensed codebase. Projects without an explicit license are reference-only. See `docs/open-source-review.md`.

## 2026-08-25 — Conservative order evidence and capacity priority

Order-to-auction priority is `risk-adjusted profit/day × conservative attainable batches`, with batch capacity capped at the 18 auction-listing limit. The allocator treats one acquisition order for an item as one order slot even when it can supply several same-quantity auction batches; each resale batch still consumes one auction slot. Priority is therefore comparable across cheap, high-volume commodities and expensive, slot-efficient commodities without a hardcoded balance or profit floor.

The exact full maximum-stack auction cohort is preferred for stackable commodities because the player will relist the same quantity. Single-item valuation is only a fallback when the API has no qualifying exact-stack cohort. The current best order reward is treated as the price to beat by exactly one cent per unit, the smallest representable Donut reward increment, so the displayed queue position is a target position of one, not a claim that Donut exposes the true hidden queue. Candidate payloads expose both that exact per-unit reward and the gross exact-quantity auction list target; total acquisition and after-fee proceeds remain separate so neither the dashboard nor Fabric asks the player to reverse-engineer executable inputs from net economics.

Only a conservative allowlist of base commodities can be signature-complete and receive a priority rank. Any modifier marker, or an inherently variant-sensitive item such as equipment, books, potions, maps, heads, shulker boxes, or music discs, remains unranked research until a future canonical component parser proves equivalence. This deliberately trades market breadth for executable-price correctness.

Legacy inferred fill reductions are retained rather than erased, but have confirmation level zero and are excluded from scoring. A fill counts only when the same observer sees the same stable order key and price decrease within two minutes, on the same menu page of the same leased `focused_watch` task, the reduced row's canonical signature exactly matches that task's target signature, and both observations carry a server-visible order ID or owner identity. Anonymous synthetic slot keys remain useful for snapshots but can never prove volume. Discovery scans and neighboring items on a watched page can never create trusted fill volume. Page/task/signature/identity matching prevents two distinct orders occupying the same slot on adjacent pages, or an unrelated busy item beside the target, from masquerading as target volume while allowing the collector's deliberately idempotent refresh-session IDs to rotate. This quarantines impossible long-gap, cross-page, neighbor-item, and weak-identity volume while focused watches rebuild trustworthy fill velocity. The dashboard exposes confirmed and quarantined totals separately.

Outstanding quantity in competing buy orders does not imply that sellers will fill a newly created order. It is excluded from order-to-auction capacity. Until confirmed fill velocity exists, a research candidate receives at most one exploratory batch; confirmed velocity is required before capacity can scale.

`READY` additionally requires at least 50% exact-quantity auction confidence and a latest target-price sale no older than two hours. The 24-hour volume window remains useful for capacity, but does not by itself make older evidence executable.

Conservative profit applies the auction model's price-confidence haircut once. Risk-adjusted profit/day then applies completion probability once and divides by cycle duration. Keeping these terms separate matches the published formula and avoids double-discounting completion.

Focused watches preempt discovery after the currently submitted page. The backend signals this only on the observer's authenticated live discovery lease; the collector completes and rotates that passive task, then leases the higher-priority focused task. Focused work uses a four-minute bounded work horizon while successful per-page heartbeats renew the backend's short failure-detection lease. A watch remains requested for 15 minutes, allowing deep pagination and repeated same-page samples without turning a lost collector into a long-lived lease.

Completed discovery and focused-watch rotations reset reconnect failure backoff. The monotonic reconnect counter remains visible for diagnostics, but only unexpected short-lived connection or menu failures can increase the delay. This prevents a legitimately small order book from eventually imposing a 60-second idle penalty between successful passes.

## 2026-08-25 — Live decision page and auction-value research lane

The normal order-flipper page has no manual refresh control. It performs a small same-origin fetch once per second and replaces only its decision content when the candidate-feed version changes. The debug console remains separate and heavier; the normal player path does not fetch its database-wide evidence snapshot.

Order discovery continues to scan every verified global page because the server menu's ordering and search control have not been proven safe or stable enough to assume that page one contains every desirable item. When a refreshed candidate feed first exposes a viable target—or after a complete discovery pass—the backend schedules one bounded 30-second focused sample. It chooses among economically viable order-to-auction candidates using the official auction API's exact-quantity resale target, preferring `READY` over `RESEARCH`, then higher resale value, then the existing risk-adjusted profit score. Automatic samples have a global one-minute minimum interval and rotate on a five-minute per-item cooldown. A player-requested watch remains higher priority and retains its longer four-minute research window. This creates a high-value lane without starving broad discovery or clicking an unverified search UI.

## Assumptions made without operator input

- The official API retains its bearer-authenticated listing and transaction endpoints and 250 requests/minute published limit.
- Live verification found 44 entries per page and valid results beyond page 2,000. The normal scan deliberately caps at the newest 220 pages (9,680 listings) to keep detection near one minute and below the 250-request/minute key limit. It does not claim full-book coverage.
- `/ah <item>` remains a supported manual search command; there is no relied-upon direct-auction-ID command.
- DonutSMP permits passive market observation by authenticated accounts. If server-team rules disallow it, collectors must remain disabled.
- The first combined-product goal is evidence collection and notify-only, player-confirmed flipping; not automated purchasing.
- Loopback is the default deployment. Public binding without a downstream client token is rejected at startup.
