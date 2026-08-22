# Cofl market-assessment research

Research date: 2026-08-20

## What Cofl actually does

Cofl does not rely on one universal "AI price." Its public system has several finders with different latency and coverage characteristics:

- `FLIP` performs slower searches over historical auctions with the same or similar economically relevant modifiers.
- `SNIPE` uses precomputed modifier-keyed price buckets and requires a candidate to be below both a relevant active lowest listing and the historical median.
- `MSNIPE` is a fast median-based path that can find candidates even when they are not the absolute lowest listing.
- Craft-cost and experimental/ML finders provide independent estimates for items with different evidence shapes, but the public documentation warns that craft cost alone does not prove resale demand.
- User filters and blacklists constrain recommendations to a player's capital, risk, and market knowledge.

The public code further shows these reliability controls:

1. Canonical modifier buckets distinguish properties that materially affect price.
2. Exact completed sales are preferred; similar or higher-value variants are fallback references.
3. Seller, buyer, item-UID, and repeated back-and-forth trades are deduplicated or penalized to reduce manipulation.
4. A short-term robust price protects against sudden drops, while a longer-term price protects against a small manipulated window; conservative estimates take the lower signal.
5. Very fresh, high-volume samples use a lower percentile rather than trusting a median during fast moves.
6. Active listings cap historical resale estimates so a stale median cannot ignore cheaper current supply.
7. Thin buckets require stronger discounts, more references, or support from comparable higher-value variants.
8. Volume, volatility, reference age, seller diversity, and estimated time-to-sell are separate risk signals rather than being hidden inside expected profit.
9. Emitted flips retain reference identifiers and calculation properties so incorrect valuations can be explained and reported.
10. Pricing and detection are separated: slower background jobs update immutable lookup structures, while each new listing is checked against those structures on a very small hot path.

## Donut-specific implications

The official Donut API exposes current auction pages and at most ten transaction pages of 100 recent sales. Reliable long-term history therefore requires continuous backend collection and persistence. The API does not document an authoritative listing ID or buyer identity, so Cofl's buyer-pair manipulation checks cannot be reproduced exactly. We can still deduplicate by transaction fingerprint, cap the influence of one seller, detect repeated same-seller price clusters, compare API listings with independently observed screen listings, and quarantine abrupt regimes until enough new sales confirm them.

Cofl's Donut site currently states that its live DonutSMP data ended after the account providing API access was banned and subsequent applications were rejected. Our collector must consequently treat authorization as revocable, use conservative pacing, expose feed freshness, preserve collected history, and stop producing time-sensitive recommendations when the feed is stale.

## Recommended Donut valuation stack

1. **Exact sold model:** exact item signature, seller-deduplicated sales, robust outlier filtering, short and long windows.
2. **Fast sniper index:** precomputed exact-signature record containing conservative resale value, current depth cap, confidence, volume, volatility, age, and model version.
3. **Active-market cap:** use several cheapest comparable listings rather than letting one suspicious lowest listing define the whole market.
4. **Family fallback:** borrow evidence from the base item or comparable modifier variants only with an explicit confidence haircut and dominance checks.
5. **Component/craft floor and cap:** useful as a cross-check, never as proof that a buyer exists.
6. **Regime guard:** down-weight old history when short-term prices fall, and require confirmation after abrupt changes or new-item launches.
7. **Opportunity scorer:** rank net expected profit after resale friction, margin, confidence, liquidity, capital-at-risk, and expected holding time.
8. **Explanation payload:** send the mod the valuation source, sample count, recent volume, volatility, snapshot age, active cap, and compact reference IDs alongside the target value.
9. **Outcome calibration:** match alerts to later sold/expired outcomes and measure precision, error, time-to-sale, and realized profit by item family and model version.

## Sources

- Cofl finder documentation: <https://sky.coflnet.com/wiki/flip-finders>
- Cofl auction-flipper FAQ: <https://sky.coflnet.com/flipper>
- Cofl public SkyFlipper implementation: <https://github.com/Coflnet/SkyFlipper>
- Cofl public SkySniper implementation: <https://github.com/Coflnet/SkySniper>
- Cofl service architecture: <https://github.com/Coflnet/HypixelSkyblock>
- Cofl Donut status: <https://donut.coflnet.com/>
- Donut API schema: <https://api.donutsmp.net/doc.json>
