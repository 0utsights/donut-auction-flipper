package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

class BackendOpportunityClientTest {
    @Test
    void decodesRankedOpportunityAndBuildsSafeSearchCommand() {
        byte[] json = ("{\"version\":9,\"state\":\"ready\",\"opportunities\":[{" +
                "\"key\":\"auction-1\",\"fingerprint\":\"id:auction-1\",\"authoritative_id\":\"auction-1\"," +
                "\"seller\":\"seller\",\"item_id\":\"minecraft:diamond_sword\",\"quantity\":1," +
                "\"price\":2000000,\"reference_value\":10000000,\"expires_at\":\"2026-08-21T12:00:00Z\"," +
                "\"confidence_bps\":7000,\"volume_24h\":12,\"profit\":8000000,\"margin_bps\":40000}]}")
                .getBytes(StandardCharsets.UTF_8);

        BackendOpportunityClient.DecodedFeed feed = BackendOpportunityClient.decode(json);
        assertEquals(9, feed.version());
        assertEquals(1, feed.opportunities().size());
        BackendOpportunityClient.Opportunity opportunity = feed.opportunities().getFirst();
        assertEquals("ah diamond sword", opportunity.auctionCommand());
        assertEquals(10_000_000L, opportunity.referenceValue());
    }

    @Test
    void rejectsNonPositivePrices() {
        byte[] json = ("{\"version\":1,\"state\":\"ready\",\"opportunities\":[{" +
                "\"key\":\"x\",\"fingerprint\":\"x\",\"seller\":\"seller\"," +
                "\"item_id\":\"minecraft:diamond\",\"quantity\":1,\"price\":0," +
                "\"reference_value\":1,\"confidence_bps\":1,\"volume_24h\":1," +
                "\"profit\":1,\"margin_bps\":1}]}").getBytes(StandardCharsets.UTF_8);
        assertThrows(IllegalArgumentException.class, () -> BackendOpportunityClient.decode(json));
    }
}
