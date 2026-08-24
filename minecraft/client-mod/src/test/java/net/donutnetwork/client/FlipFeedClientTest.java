package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;

import static org.junit.jupiter.api.Assertions.*;

class FlipFeedClientTest {
    @Test void decodesBoundedBackendFeed() {
        String json = """
                {"version":7,"status":"ready","flips":[{
                  "key":"auction-1","auction_id":"auction-1","item_id":"minecraft:redstone_block",
                  "item_name":"Redstone Block","quantity":1,"seller":"alex","price":500000,
                  "reference_value":900000,"profit":400000,"margin_bps":8000,"confidence_bps":7200,
                  "volume_24h":12,"expires_at":"2026-08-22T20:00:00Z","search_command":"/ah alex",
                  "seller_command":"/ah alex","item_search_command":"/ah redstone_block",
                  "model_version":"robust-v3-quantity","expected_sell_minutes":8
                }]}
                """;
        FlipFeedClient.DecodedFeed feed = FlipFeedClient.decode(json.getBytes(StandardCharsets.UTF_8));
        assertEquals(7, feed.version());
        assertEquals(1, feed.flips().size());
        assertEquals("ah alex", FlipFeedClient.commandWithoutSlash(feed.flips().getFirst()));
        assertEquals("ah redstone_block", FlipFeedClient.commandWithoutSlash(feed.flips().getFirst().itemSearchCommand()));
    }

    @Test void rejectsInjectedCommand() {
        String json = """
                {"version":1,"status":"ready","flips":[{
                  "key":"x","item_id":"minecraft:diamond","item_name":"Diamond","quantity":1,
                  "seller":"alex","price":1,"reference_value":2,"profit":1,"margin_bps":1,
                  "confidence_bps":1,"volume_24h":1,"search_command":"/op attacker",
                  "model_version":"robust-v2","expected_sell_minutes":1
                }]}
                """;
        assertThrows(IllegalArgumentException.class,
                () -> FlipFeedClient.decode(json.getBytes(StandardCharsets.UTF_8)));
    }

    @Test void rejectsOversizedFeed() {
        String json = "{\"version\":1,\"status\":\"ready\",\"flips\":[" + "{},".repeat(101) + "{}]}";
        assertThrows(IllegalArgumentException.class,
                () -> FlipFeedClient.decode(json.getBytes(StandardCharsets.UTF_8)));
    }

    @Test void rejectsInjectedSecondaryCommand() {
        String json = """
                {"version":1,"status":"ready","flips":[{
                  "key":"x","item_id":"minecraft:diamond","item_name":"Diamond","quantity":1,
                  "seller":"alex","price":1,"reference_value":2,"profit":1,"margin_bps":1,
                  "confidence_bps":1,"volume_24h":1,"search_command":"/ah alex",
                  "seller_command":"/ah alex","item_search_command":"/op attacker",
                  "model_version":"robust-v3-quantity","expected_sell_minutes":1
                }]}
                """;
        assertThrows(IllegalArgumentException.class,
                () -> FlipFeedClient.decode(json.getBytes(StandardCharsets.UTF_8)));
    }

    @Test void toleratesLegacyNullFeedDuringStartup() {
        String json = "{\"version\":0,\"status\":\"collecting\",\"flips\":null}";
        FlipFeedClient.DecodedFeed feed = FlipFeedClient.decode(json.getBytes(StandardCharsets.UTF_8));
        assertTrue(feed.flips().isEmpty());
        assertEquals("collecting", feed.state());
    }
}
