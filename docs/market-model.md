# Market model

Money uses signed 64-bit integer currency units throughout the hot path. Stack quantity is excluded from item identity and included in total/unit-price arithmetic.

An item has three identities:

- Base signature: normalized namespaced item ID, such as `minecraft:elytra`.
- Modifier string: sorted economically relevant enchantments, trim, custom name, durability bucket and allow-listed components.
- Exact signature: base plus modifier string.

Unknown components are preserved in raw source item JSON but do not silently fragment valuations. Fingerprints prefer an authoritative auction ID; because the public Donut API currently documents none, the fallback hashes seller, exact signature, total price and quantity with FNV-1a.

## Valuation robust-v2

For affected signatures, the engine selects the most recent 30 days, collapses repeated seller/day evidence to a median, requires at least three samples, and removes points more than six median absolute deviations from the median. The long estimate uses a 30-day decay; the short estimate selects the narrowest 24-hour, 72-hour, or seven-day window with enough evidence. Fair value is the conservative minimum, preventing an unsupported rising market from being extrapolated while responding quickly to falling prices.

Quick-sale value is discounted for volatility and can be capped by the third-cheapest distinct active seller. One bait listing and one seller's listing wall cannot define that cap. Twenty-four-hour volume permits at most three sales per seller, and confidence incorporates samples, distinct sellers, volume, active depth, volatility, reference age, market regime, and API modifier fidelity. Active listings leave the model at explicit expiry, on `LISTING_GONE`, or after a two-minute fallback TTL. Missing evidence produces no valuation rather than a fabricated price.

The engine maintains exact-variant models and a base-family aggregation. Clients use the base model only when the exact signature is absent, and base-family confidence is penalized when it combines modifier variants. A future calibrated model should add modifier-family priors and measure error against eventual sales.
