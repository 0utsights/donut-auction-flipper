package net.donutnetwork.client;

import java.util.HashMap;
import java.util.Map;
import java.util.TreeMap;

/** Bounded current-screen order book used as a local baseline before API comparison is available. */
public final class AuctionMarketIndex {
    public record Quote(long medianUnitPrice, long lowestUnitPrice, int comparableListings) {}
    private static final int MAX_LOCATIONS = 512;
    private final Map<String, ParsedListing> byLocation = new HashMap<>();
    private final Map<String, PriceMultiset> bySignature = new HashMap<>();

    public synchronized Quote upsertAndQuote(String location, ParsedListing listing) {
        remove(location);
        if (byLocation.size() >= MAX_LOCATIONS) {
            remove(byLocation.keySet().iterator().next());
        }
        byLocation.put(location, listing);
        bySignature.computeIfAbsent(listing.signature(), ignored -> new PriceMultiset()).add(listing.unitPrice());
        return quoteExcluding(location);
    }

    public synchronized Quote quoteExcluding(String location) {
        ParsedListing current = byLocation.get(location);
        if (current == null) {
            return new Quote(0, 0, 0);
        }
        PriceMultiset prices = bySignature.get(current.signature());
        return prices == null ? new Quote(0, 0, 0) : prices.quoteExcluding(current.unitPrice());
    }

    public synchronized void remove(String location) {
        ParsedListing previous = byLocation.remove(location);
        if (previous == null) {
            return;
        }
        PriceMultiset prices = bySignature.get(previous.signature());
        if (prices != null && prices.remove(previous.unitPrice())) {
            bySignature.remove(previous.signature());
        }
    }

    public synchronized void clearScreen(String screenPrefix) {
        byLocation.keySet().stream().filter(key -> key.startsWith(screenPrefix)).toList().forEach(this::remove);
    }

    public synchronized int size() {
        return byLocation.size();
    }

    private static final class PriceMultiset {
        private final TreeMap<Long, Integer> prices = new TreeMap<>();
        private int size;

        void add(long price) {
            prices.merge(price, 1, Integer::sum);
            size++;
        }

        boolean remove(long price) {
            Integer count = prices.get(price);
            if (count == null) {
                return size == 0;
            }
            if (count == 1) {
                prices.remove(price);
            } else {
                prices.put(price, count - 1);
            }
            size--;
            return size == 0;
        }

        Quote quoteExcluding(long excludedPrice) {
            int comparable = Math.max(0, size - 1);
            if (comparable == 0) {
                return new Quote(0, 0, 0);
            }
            int target = (comparable - 1) / 2;
            int seen = 0;
            boolean excluded = false;
            long median = 0;
            long lowest = 0;
            for (Map.Entry<Long, Integer> entry : prices.entrySet()) {
                int count = entry.getValue();
                if (!excluded && entry.getKey() == excludedPrice) {
                    count--;
                    excluded = true;
                }
                if (count <= 0) {
                    continue;
                }
                if (lowest == 0) {
                    lowest = entry.getKey();
                }
                if (seen + count > target) {
                    median = entry.getKey();
                    break;
                }
                seen += count;
            }
            return new Quote(median, lowest, comparable);
        }
    }
}
