package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;

class AuctionMarketIndexTest {
    @Test
    void quotesMedianAndLowAskWithoutTheCandidate() {
        AuctionMarketIndex index = new AuctionMarketIndex();
        index.upsertAndQuote("s:1", listing("one", 100));
        index.upsertAndQuote("s:2", listing("two", 120));
        index.upsertAndQuote("s:3", listing("three", 90));
        AuctionMarketIndex.Quote quote = index.upsertAndQuote("s:4", listing("candidate", 40));
        assertEquals(3, quote.comparableListings());
        assertEquals(100, quote.medianUnitPrice());
        assertEquals(90, quote.lowestUnitPrice());
    }

    @Test
    void replacementAndScreenCleanupDoNotLeaveStalePrices() {
        AuctionMarketIndex index = new AuctionMarketIndex();
        index.upsertAndQuote("screen:1:slot:1", listing("one", 100));
        index.upsertAndQuote("screen:1:slot:1", listing("replacement", 200));
        index.upsertAndQuote("screen:1:slot:2", listing("two", 300));
        assertEquals(200, index.quoteExcluding("screen:1:slot:2").medianUnitPrice());
        index.clearScreen("screen:1:");
        assertEquals(0, index.size());
    }

    private static ParsedListing listing(String id, long unitPrice) {
        return new ParsedListing(id, "minecraft:elytra", unitPrice, 1, "seller");
    }
}
