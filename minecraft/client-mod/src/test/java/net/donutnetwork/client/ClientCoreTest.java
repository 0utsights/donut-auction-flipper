package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

class ClientCoreTest {
    @Test
    void evaluatesAgainstCurrentScreenMedianWithoutNetworkIo() {
        ClientCore core = new ClientCore();
        core.onSlotChanged("s:1", item(100_000_000L));
        core.onSlotChanged("s:2", item(105_000_000L));
        core.onSlotChanged("s:3", item(110_000_000L));
        ListingEvaluation result = core.onSlotChanged("s:4", item(50_000_000L)).orElseThrow();
        assertEquals(ListingEvaluation.Source.AUCTION_SCREEN, result.source());
        assertTrue(result.opportunity());
        assertEquals(55_000_000L, result.expectedProfit());
        assertEquals(3, result.comparableListings());
        assertTrue(result.latencyNanos() > 0);
    }

    @Test
    void officialSnapshotTakesPrecedenceWhenFresh() {
        ClientCore core = new ClientCore();
        core.onSlotChanged("s:1", item(100_000_000L));
        core.onSlotChanged("s:2", item(105_000_000L));
        core.onSlotChanged("s:3", item(110_000_000L));
        PriceSnapshotCache.Value official = new PriceSnapshotCache.Value(120_000_000L,
                115_000_000L, 8_000, 20);
        core.prices().replace(new PriceSnapshotCache.Snapshot(1, java.time.Instant.now(),
                Map.of("minecraft:elytra", official)));
        ListingEvaluation result = core.onSlotChanged("s:4", item(90_000_000L)).orElseThrow();
        assertEquals(ListingEvaluation.Source.OFFICIAL_SNAPSHOT, result.source());
        assertTrue(result.opportunity());
        assertEquals(105_000_000L, result.screenMedianTotal());
        assertEquals(115_000_000L, result.officialQuickSellTotal());
        assertEquals(-869, result.sourceSpreadBps());
    }

    @Test
    void clearingSlotRemovesItFromLocalIndex() {
        ClientCore core = new ClientCore();
        core.onSlotChanged("screen:1:slot:1", item(100_000_000L));
        assertEquals(1, core.indexedListings());
        core.onSlotCleared("screen:1:slot:1");
        assertEquals(0, core.indexedListings());
    }

    @Test
    void restartedBackendCanReplaceHigherOldVersion() {
        PriceSnapshotCache cache = new PriceSnapshotCache(java.time.Duration.ofMinutes(2));
        assertTrue(cache.replace(new PriceSnapshotCache.Snapshot(100, java.time.Instant.parse("2026-08-20T10:00:00Z"), Map.of())));
        assertTrue(cache.replace(new PriceSnapshotCache.Snapshot(1, java.time.Instant.parse("2026-08-20T10:01:00Z"), Map.of())));
        assertEquals(1, cache.version());
        assertFalse(cache.replace(new PriceSnapshotCache.Snapshot(1, java.time.Instant.parse("2026-08-20T10:00:30Z"), Map.of())));
    }

    @Test
    void evaluatorFailsClosedOnPriceOverflow() {
        PriceSnapshotCache cache = new PriceSnapshotCache(java.time.Duration.ofMinutes(2));
        cache.replace(new PriceSnapshotCache.Snapshot(1, java.time.Instant.now(), Map.of("minecraft:elytra",
                new PriceSnapshotCache.Value(Long.MAX_VALUE, Long.MAX_VALUE, 9_000, 10))));
        FlipEvaluator evaluator = new FlipEvaluator(cache, new FlipEvaluator.Thresholds(1, 1, 1, Long.MAX_VALUE, 1));
        ParsedListing listing = new ParsedListing("id", "minecraft:elytra", 2, 2, "seller");
        FlipEvaluator.Decision decision = evaluator.evaluate(listing, System.nanoTime());
        assertFalse(decision.flip());
        assertEquals("price overflow", decision.reason());
    }

    private static ItemStackView item(long price) {
        return new ItemStackView("minecraft:elytra", 1, "",
                List.of("Price: $" + price, "Seller: test"), Map.of(), "", "");
    }
}
