# Architecture decisions

## 2026-08-22 — Preserve the old system before redesign

The complete pre-redesign repository was committed as `be6a624`, tagged `legacy-full-system-v0.4.0`, pushed to branch `archive/full-system-v0.4.0`, and stored in the private GitHub repository `0utsights/donut-auction-flipper`. The parser is not shipped now, but remains recoverable from that immutable tag and archive branch.

## 2026-08-22 — API-only normal path

The normal product is one Go backend plus one thin Fabric client. PostgreSQL, Node, WebSockets, workers, sharding, screen observation, local price caches, telemetry, simulation, and purchase scaffolding were removed. This is the smallest design that can collect official data, build a market model, notify a player quickly, and expose its reasoning.

## 2026-08-22 — Backend-only upstream credential

`DONUT_API_KEY` exists only in the backend environment. It is excluded from Git, state files, logs, HTTP responses, debug HTML, Fabric resources, and mod configuration. The distributed mod only receives ranked flip records. A separate optional `DN_CLIENT_TOKEN` protects that downstream feed.

For Windows local development, `scripts/start-local.ps1` may cache the key as a user-scoped DPAPI ciphertext under the Git-ignored `data/` directory. This keeps the normal launch one-command without placing the plaintext key in source, mod files, command arguments, or shell history.

## 2026-08-22 — Bounded file persistence before a database

The only durable state needed now is recent completed-sale history. It is stored as a versioned gzip JSON document with safe rotation, 31-day retention, deduplication, and a 100,000-row cap. Active listings are always recollected. A database should be introduced only when multi-process operation, longer retention, or calibrated analytics actually requires one.

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

## 2026-08-22 — Barebones client and debug UI

The in-game screen and backend debug page are intentionally plain, information-first interfaces. The Fabric screen uses an opaque fill and does not call `renderBackground`, fixing the Minecraft 1.21.11 `Can only blur once per frame` crash. Styling remains deferred.

## 2026-08-22 — Compatibility baseline

The mod targets Minecraft 1.21.11, Java 21, Fabric API 0.141.6, and Fabric Loader 0.19.2 or newer to match the current Prism instance. Fabric Loom 1.17.19 requires Gradle 9.5 or newer for builds; Gradle 9.7 is used in CI.

## 2026-08-22 — Open-source reuse policy

The official Donut API schema remains the primary contract, and the retained in-repository API client is more defensive than the surveyed community clients. `cancel-cloud/Donuts-Auctions` is MIT-licensed and useful as a parser/reference implementation, but its automation features are not imported. GPL code from `RubyImpala/Glaze` is not copied into this differently licensed codebase. Projects without an explicit license are reference-only. See `docs/open-source-review.md`.

## Assumptions made without operator input

- The official API retains its bearer-authenticated listing and transaction endpoints and 250 requests/minute published limit.
- Live verification found 44 entries per page and valid results beyond page 2,000. The normal scan deliberately caps at the newest 220 pages (9,680 listings) to keep detection near one minute and below the 250-request/minute key limit. It does not claim full-book coverage.
- `/ah <item>` remains a supported manual search command; there is no relied-upon direct-auction-ID command.
- The first production goal is useful notify-only flipping, not automated purchasing or distributed scale.
- Loopback is the default deployment. Public binding without a downstream client token is rejected at startup.
