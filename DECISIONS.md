# Architecture decisions

## 2026-08-16 — Go modular monolith

Go is available in the execution environment and provides low operational overhead, predictable latency, a strong standard library, and inexpensive concurrency. The first deployment is one backend process with explicit market, network, scheduler, worker, and Donut API packages. These boundaries can be split later without imposing premature distributed failure modes on the runtime.

## 2026-08-16 — Memory-first local runtime, PostgreSQL production schema

The local backend uses an in-memory repository when `DN_DATABASE_URL` is absent, so it can start without Docker while remaining empty until authenticated data arrives. Compose wires PostgreSQL persistence by default. Migrations model durable storage and deliberately index current listings, time-series observations, and transaction lookup; repository interfaces keep persistence replaceable without a JSON-backed substitute.

## 2026-08-16 — Binary WebSocket envelope with JSON control payloads

Realtime frames use a compact binary envelope (protocol version, priority, type, payload length) while payloads remain JSON during the reference phase. This prevents text-frame ambiguity and gives priority scheduling without committing every schema to generated code. Protobuf is the planned compatibility migration once message evolution stabilizes.

## 2026-08-16 — Client hot path is entirely local

Workers atomically replace an immutable price map on snapshot receipt. Listing evaluation performs normalization, one map lookup, and integer threshold comparisons. Observation and purchase telemetry are queued after the decision and can never gate it.

## 2026-08-16 — Safe purchase default

Only notify, assisted, and simulated modes execute in the reference build. A live interaction adapter is represented by an interface and must revalidate screen identity, item signature, seller, and price; no evasion or bypass behavior is included.

## 2026-08-16 — Current Donut API contract

The collector targets the official Swagger 2.0 document retrieved from `https://api.donutsmp.net/doc.json` on 2026-08-16: bearer authentication, `GET /v1/auction/list/{page}` with optional search/sort body, `GET /v1/auction/transactions/{page}`, and a published 250 requests/minute/key limit. Money is defensively decoded from JSON numbers but converted to integer currency at the boundary.

## 2026-08-16 — Function-first Minecraft operator UI

The initial decorative dashboard direction was removed. The shipped UI is a square, table-first monospace console with restrained grass/dirt accents, no generated promotional imagery, and no fabricated fallback dataset. Visual styling is intentionally deferred; offline state is explicit.

## 2026-08-16 — Real admin boundary through a dashboard BFF

Dashboard and metrics projections require the separate admin bearer token. Browser code calls a same-origin server route that supplies the token from runtime environment, so an operator secret is not compiled into the client bundle.

## 2026-08-16 — Bounded active-market influence

Listings expire at their explicit deadline or after a two-minute fallback TTL, and `LISTING_GONE` removes them immediately. This prevents stale low asks from suppressing quick-sale values indefinitely. The simulator uses 30-second auction lifetimes.

## 2026-08-16 — Hierarchical local fallback

The engine maintains exact-variant models and an aggregated base-family model. Base fallbacks receive a confidence penalty, and both Go and Fabric clients use them locally only when an exact signature is absent. This preserves the no-network critical-path invariant for unseen modifier combinations.

## 2026-08-16 — Local-only dashboard delivery

The dashboard is run beside the backend on localhost or through Docker Compose. Hosting metadata, Cloudflare/Sites plugins, the deployment worker, and the hosted Git remote were removed. The local server-side proxy still keeps the admin credential out of browser JavaScript.

## 2026-08-16 — Live-only runtime by default

Normal startup must never manufacture market activity. The dashboard begins empty, reports `live` as its data mode, and explains which authenticated feed is missing. The official Donut collector is enabled only with the `live` Compose profile and a user-supplied `DONUT_API_KEY`; the simulator is isolated behind the explicit `simulation` profile. Existing in-memory synthetic state was discarded by restarting the backend without the simulator. Persisted data is never silently classified as live, so a database previously populated by simulation should be removed or migrated deliberately before reuse.

Live mode rejects simulator-tagged HTTP records and simulation-mode WebSocket clients, and ignores any persisted simulator transaction history during restoration. This makes the source label an enforced boundary rather than a cosmetic dashboard setting.

Batch ingestion uses a separate collector token from ordinary worker traffic. The dashboard also reports the age of the last accepted official API batch; backend connectivity alone is never presented as proof that the official feed is running.

## 2026-08-17 — Read-only auction screen observer

The first live Minecraft adapter is notify-only. Fabric screen lifecycle events attach only to conservatively recognized handled auction listing screens. The adapter ignores player inventory, fingerprints all server-owned slots, and parses only changed stacks. A bounded exact-signature order book provides an immediate screen-median baseline without network I/O; fresh official snapshots take precedence while both sources remain in the evaluation result for later accuracy measurement. No slot click, refresh, command, or purchase behavior is registered.

## 2026-08-20 — Fabric Loader 0.19.2 baseline

The mod targets Loader 0.19.2 to match the operator’s Prism instance. Fabric API 0.141.6 declares Loader 0.17.3 or newer, and the mod uses no Loader 0.19.3-only API, so lowering the dependency is compatible rather than merely suppressing the launcher check. The 0.19.2 baseline is compiled and tested directly.

## 2026-08-20 — Backend-only Donut API credential

The Donut API credential belongs only to the independently deployed collector. It is supplied at runtime through `DONUT_API_KEY`, excluded by `.gitignore`, and must never be embedded in the Fabric JAR, dashboard browser bundle, WebSocket snapshots, logs, or repository files. The distributed mod receives normalized valuation snapshots from our backend and has no direct Donut API access. API access is treated as revocable infrastructure: collection failures make snapshot age explicit, retain previously persisted history, and fail closed for time-sensitive recommendations rather than exposing or transferring the upstream credential.

## 2026-08-20 — Cofl-inspired multi-signal valuation, not a single price formula

Cofl's public documentation and finder implementations show that reliable auction assessment comes from combining specialized signals: exact completed-sale comparables, modifier-aware buckets, active lowest listings, short- and long-horizon robust prices, liquidity/sell-time evidence, craft or component value caps, manipulation resistance, and explainable reference auctions. Donut valuations will follow the same principles without copying Cofl's game-specific constants. Exact sold comparables remain authoritative; similar-item and component estimates are explicitly lower-confidence fallbacks. A fast precomputed lookup serves detection, while slower recomputation, calibration, and replay evaluation remain on the backend. Every emitted recommendation must expose its source, reference age, sample count, liquidity, volatility, and active-market cap.

## 2026-08-20 — API-first hybrid detection

The official API is the broad-market source of completed sales and active listings. The collector scans all ten transaction pages and up to 220 44-row listing pages once per minute while enforcing a 240-request/minute client-side ceiling. The Fabric parser remains a read-only, optional low-latency supplement: it observes changed auction slots, evaluates them against a precomputed backend snapshot locally, and falls back to the current screen median only when backend evidence is unavailable or stale. This keeps slow statistical work off the listing-detection path.

The official schema does not provide an auction ID. API listing identity therefore combines seller, exact observable signature, price, quantity, and a five-second expiry bucket. It is stable across normal polls and distinguishes most duplicate listings, but it is documented as a best-effort identity rather than an upstream guarantee.

## 2026-08-20 — Robust-v2 valuation and modifier-fidelity warning

`robust-v2` uses seller-day deduplication, MAD outlier filtering, a conservative minimum of short- and long-horizon estimates, capped per-seller volume, distinct-seller active references, falling-market protection, reference age, volatility, sell-time estimates, and explicit risk flags. A single low ask cannot cap resale without three distinct active sellers, and one seller cannot create an artificial listing wall. Sensitive equipment receives an `api_modifier_blindspot` confidence penalty because the public API's enchantment field is not reliably populated; parsed exact modifiers may use the conservative base-family fallback until better evidence exists.

## 2026-08-20 — Compact polling for the distributed Fabric client

The mod polls `/api/v1/client-snapshot` every ten seconds with ETag revalidation. This endpoint contains only fair value, quick-sale value, confidence, and volume. Full evidence remains behind the admin boundary. Backend restart versions may reset, so the atomic cache accepts a lower version only when its generation time is newer. The mod config contains our backend URL and client token, never the Donut API key.

## 2026-08-20 — Operator evidence and automatic migrations

The local dashboard Debug view is the primary inspection surface. It shows collection cycle counts, latency, retries, rate limits, model version, short/long estimates, distinct-seller active cap, confidence, risk flags, and the raw comparable sales for exact or base-fallback decisions. PostgreSQL migrations run under an advisory lock at backend startup, raw records are batch-written, robust-v2 valuations are historized, and collector cycles are retained. Docker's first-start SQL remains supported, but startup migration is authoritative for existing volumes.

## 2026-08-20 — Assumptions made without operator input

An operator-supplied API key was validated successfully through the collector's runtime environment on 2026-08-20. The credential is not persisted in the repository or client; local live collection lasts only for the lifetime of that collector process, and future restarts must supply `DONUT_API_KEY` again. The system targets DonutSMP Java 1.21.11, Fabric Loader 0.19.2, a one-minute official scan, notify-only interaction, and localhost development tokens. Automatic purchasing remains intentionally absent pending explicit server permission and a separately reviewed revalidation adapter.

## 2026-08-20: Backend-ranked chat alerts use safe auction search navigation

The backend, not the distributed mod, ranks official-API listings against quick-sell values. The client polls a compact authenticated opportunity feed every two seconds, deduplicates alerts by authoritative listing ID, caps bursts at five, and shows the current top six in a minimal `N`/`/dn` screen. DonutSMP publicly supports `/ah <item>` filtered searches, but no reliable direct-listing-ID command was found. Therefore chat and GUI clicks open a sanitized item search; they never click a slot, confirm a purchase, or claim to navigate directly to one listing.
