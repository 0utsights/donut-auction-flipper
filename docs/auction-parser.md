# Auction parser

The Fabric 0.3 client contains a read-only Donut auction observer plus a compact backend snapshot poller. It recognizes handled inventory screens whose visible title contains `auction`, `auctions`, `auction house`, `browse auctions`, or `market listings`. Confirmation, purchase, creation, personal-auction, and expired-auction screens are excluded. At least nine non-player slots must be present.

Only server-owned slots are scanned. Player inventory slots are ignored by inventory identity, not by a hard-coded slot range. Each stack is fingerprinted from item ID, quantity, and its immutable data components; unchanged stacks incur no lore parsing or valuation work.

## Accepted listing lore

Price lines accept case-insensitive labels `Price`, `Cost`, `Buy Now`, or `BIN`, as well as a leading dollar sign. Commas, spaces, decimals, and `k`/`m`/`b`/`t` suffixes are supported. Lines marked `per item`, `per unit`, `/item`, `/unit`, or `each` are multiplied by the stack quantity before evaluation. Examples:

```text
Price: $281,000,000
Buy Now: $1.5m
BIN - 750k
```

Seller lines accept `Seller`, `Sold by`, or `Owner`. `Auction ID`, `Auction #`, `Listing ID`, and `Listing #` become authoritative identifiers. If no identifier exists, the screen sync ID and slot form a session-local identity.

Canonical signatures mirror the Go backend ordering for item ID, enchantments, armor trim, custom name, durability bucket, custom model data, and dyed color. This lets later backend snapshots and API records share an exact comparison key.

## Local valuation

The current screen is a bounded order book, not completed-sales history. For each exact signature, the candidate is excluded and the median unit ask of the remaining listings is used after at least three comparables exist. Notifications explicitly label this source as `screen median`. A fresh official snapshot takes precedence for opportunity decisions; the evaluation still records the screen median, official quick-sell total, and source spread.

This is intentionally notify-only. It does not click, purchase, refresh, search, or send commands.

The poller reads `config/donut-network-client.properties`, requests `/api/v1/client-snapshot` with ETag revalidation, validates an 8 MiB response bound and numeric ranges, and atomically replaces the local cache. Backend failures retain the last cache for diagnosis but it stops influencing decisions after two minutes. The upstream Donut API credential is never accepted by or stored in the mod.

## Verification

```powershell
$env:JAVA_HOME='path-to-jdk-21'
gradle -p minecraft/client-mod clean test build
gradle -p minecraft/client-mod benchmarkAuctionPipeline
```

Before enabling the mod on DonutSMP, capture sanitized screen title and item-lore fixtures from several pages, item types, stacks, enchantments, and confirmation screens. Add those strings as parser/classifier tests. If Donut changes a title or label, extend the conservative allow-list rather than accepting every chest screen.
