# Backend HTTP API

## `GET /`

Plain one-second auto-refreshing operational/debug page. It shows fast-lane publish time and duration, broad-scan state and counts, upstream request statistics, current thresholds, the rejection funnel, highest-volume valuations, and qualified flips. No fake fallback rows are generated.

## `GET /healthz`

Returns `200` after at least one successful scan, including while a later scan is collecting. Returns `503` while starting or when the first scan fails.

## `GET /order-auction-flipper`

Barebones order-to-auction research page. It reports the live auction source, the intentionally missing order source, proposed absolute-profit/margin/liquidity/slot-efficiency gates, and no opportunity rows until real order snapshots are connected.

## `GET /api/v1/flips`

The Fabric feed. If `DN_CLIENT_TOKEN` is configured, send `Authorization: Bearer <token>`.

```json
{
  "version": 12,
  "generated_at": "2026-08-22T22:00:00Z",
  "status": "ready",
  "flips": [{
    "key": "donut:...",
    "item_id": "minecraft:diamond",
    "item_name": "Diamond",
    "quantity": 1,
    "seller": "player",
    "price": 500000,
    "reference_value": 900000,
    "unit_reference_value": 900000,
    "singular_unit_reference": 900000,
    "quantity_unit_reference": 900000,
    "profit": 400000,
    "margin_bps": 8000,
    "confidence_bps": 7200,
    "volume_24h": 4,
    "market_volume_24h": 12,
    "price_seller_count": 3,
    "price_band_low": 810000,
    "price_band_high": 990000,
    "singular_volume_24h": 4,
    "quantity_volume_24h": 4,
    "search_command": "/ah player",
    "seller_command": "/ah player",
    "item_search_command": "/ah diamond",
    "model_version": "robust-v4-price-volume-quantity",
    "pricing_basis": "exact-quantity",
    "expected_sell_minutes": 8
  }]
}
```

Responses include an `ETag`. Send it back as `If-None-Match`; unchanged feeds return `304` with no JSON body. At most 100 distinct exact-signature opportunities are published.

`unit_reference_value` is always a per-item value. For a stacked listing, `reference_value` is that conservative unit value multiplied by the unchanged listing quantity. The unit value is the lower of a quantity-one completed-sale model and an exact-quantity completed-sale model; stacks without both evidence cohorts are rejected.

`volume_24h` is qualifying liquidity inside `price_band_low`–`price_band_high`, the ±10% band around the proposed per-item resale value. `price_seller_count` is the number of distinct sellers supplying that evidence. `market_volume_24h` is the broader item-wide count for diagnosis only and cannot qualify an alert.

`search_command` is the preferred seller route and remains for client compatibility. `seller_command` opens the seller-filtered auction view; `item_search_command` uses the canonical Minecraft identifier path with underscores. The upstream API does not return a server-addressable listing ID, so `auction_id` is a backend fingerprint and must not be sent as a command.

## `GET /api/v1/debug`

Machine-readable version of the debug snapshot: status, thresholds, API request/error/retry counts, scan counts, history/valuation counts, and flips.

## `GET /api/v1/debug/valuation?signature=...`

Explains one exact/base signature from the last completed immutable engine. The response includes status/reason, the robust valuation, up to 100 recent comparable sales, up to 100 active listings, and the raw recent sample count. The same client bearer token applies when configured.
