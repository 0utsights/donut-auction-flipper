# Architecture

The runtime has one direction of data flow:

```text
Official Donut API
        |
        v
one rate-limited scan -> bounded sale history -> fresh robust-v2 model
                                                |
                                                v
                                     immutable ranked flip feed
                                                |
                                      HTTP polling with ETag
                                                |
                                                v
                                   Fabric chat + manual /ah search
```

## Backend cycle

1. Fetch up to ten official completed-transaction pages.
2. Fetch the newest active listings in recently-listed order until the first short 44-row page or the 220-page latency cap (9,680 rows).
3. Merge transactions by stable fingerprint and sale timestamp; discard records older than 31 days and cap the archive at 100,000.
4. Build a new market engine from history and the current active book.
5. Publish a complete immutable snapshot through one atomic pointer.
6. Persist history with a temporary file and backup rotation.

An error changes the visible status and preserves the previous feed for inspection. The mod emits alerts only while status is `ready`.

## Trust boundaries

- The official API key ends at the Donut API client and is never serialized.
- `/api/v1/flips` uses an optional bearer client token; one is mandatory for non-loopback binds.
- `/api/v1/debug` and `/` contain market evidence and operational counters but no secrets.
- Seller and canonical item-ID commands are generated from restricted ASCII alphabets on the backend and independently checked by the mod. Backend auction fingerprints are never treated as server commands.

## Valuation

The model uses completed sales as the authority. It canonicalizes item modifiers, deduplicates seller/day influence, filters outliers with median absolute deviation, compares short and long windows, caps per-seller volume, estimates liquidity and sale time, and uses distinct active sellers only as a conservative market cap. A recommendation is blocked when evidence is stale or the official API cannot represent economically sensitive modifiers reliably.

Opportunity pricing uses `robust-v3-quantity`. Quantity-one completed sales establish the mandatory per-item ceiling. A stack also requires completed sales at its exact quantity, capturing real bulk discounts. The lower per-unit quick-sell value is multiplied by the unchanged resale quantity. The engine never assumes a stack will be split for resale and rejects stacks lacking either cohort.

## Why polling

The full upstream book has thousands of pages and cannot be exhaustively rescanned quickly under the published rate limit. The backend instead scans the newest 220 pages in about a minute; completed sales provide broad-market valuation. Two-second conditional polling gives the mod prompt delivery after publication with ordinary HTTP semantics, tiny unchanged responses (`304 Not Modified`), simple reconnection, and no WebSocket lifecycle or fanout machinery.
