# Backend HTTP API

## `GET /`

Plain auto-refreshing operational/debug page. It shows scan state, counts, upstream request statistics, current thresholds, the rejection funnel, highest-volume valuations, and qualified flips. No fake fallback rows are generated.

## `GET /healthz`

Returns `200` after at least one successful scan, including while a later scan is collecting. Returns `503` while starting or when the first scan fails.

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
    "volume_24h": 12,
    "singular_volume_24h": 12,
    "quantity_volume_24h": 12,
    "search_command": "/ah Diamond",
    "model_version": "robust-v3-quantity",
    "pricing_basis": "exact-quantity",
    "expected_sell_minutes": 8
  }]
}
```

Responses include an `ETag`. Send it back as `If-None-Match`; unchanged feeds return `304` with no JSON body. At most 100 distinct exact-signature opportunities are published.

`unit_reference_value` is always a per-item value. For a stacked listing, `reference_value` is that conservative unit value multiplied by the unchanged listing quantity. The unit value is the lower of a quantity-one completed-sale model and an exact-quantity completed-sale model; stacks without both evidence cohorts are rejected.

## `GET /api/v1/debug`

Machine-readable version of the debug snapshot: status, thresholds, API request/error/retry counts, scan counts, history/valuation counts, and flips.

## `GET /api/v1/debug/valuation?signature=...`

Explains one exact/base signature from the last completed immutable engine. The response includes status/reason, the robust valuation, up to 100 recent comparable sales, up to 100 active listings, and the raw recent sample count. The same client bearer token applies when configured.
