package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;

import static org.junit.jupiter.api.Assertions.*;

class CandidateFeedClientTest {
    @Test void decodesEmptyCandidateFeed() {
        String json = """
                {"version":0,"generated_at":"2026-08-23T20:00:00Z","candidates":[]}
                """;
        CandidateFeedClient.DecodedFeed feed = CandidateFeedClient.decode(json.getBytes(StandardCharsets.UTF_8));
        assertTrue(feed.candidates().isEmpty());
    }

    @Test void decodesBoundedCandidateFeed() {
        String json = """
                {"version":3,"generated_at":"2026-08-23T20:00:00Z","candidates":[{
                  "id":"candidate_1","route":"ORDER_TO_AUCTION","state":"READY","reason":"",
                  "signature":"minecraft:diamond_block","item_id":"minecraft:diamond_block","item_name":"Diamond Block",
                  "quantity":64,"max_stack_size":64,"acquisition_cost":10000000,"expected_proceeds":20000000,
                  "order_unit_reward_cents":15625000,"target_list_price":20512820,
                  "conservative_profit":8000000,"margin_bps":10000,"completion_bps":8000,"expected_cycle_minutes":30,
                  "risk_adjusted_profit_day":307200000,"executable_batches":2,"queue_position":1,"order_slots":1,"auction_slots":1,
                  "inventory_slots":1,"profit_per_inventory_slot":8000000,"confidence_bps":9000,"order_tier":"actionable",
                  "order_fresh_at":"2026-08-23T20:00:00Z","focused_fresh_at":"2026-08-23T20:00:00Z","auction_fresh_at":"2026-08-23T20:00:00Z",
                  "order_command":"/orders","auction_command":"/ah diamond_block"
                }]}
                """;
        CandidateFeedClient.DecodedFeed feed = CandidateFeedClient.decode(json.getBytes(StandardCharsets.UTF_8));
        assertEquals(3, feed.version());
        assertEquals(1, feed.candidates().size());
        assertEquals("READY", feed.candidates().getFirst().state());
        assertEquals(15_625_000, feed.candidates().getFirst().orderUnitRewardCents());
        assertEquals(20_512_820, feed.candidates().getFirst().targetListPrice());
    }

    @Test void rejectsServerSuppliedTransactionCommand() {
        String json = """
                {"version":1,"generated_at":"2026-08-23T20:00:00Z","candidates":[{
                  "id":"c","route":"ORDER_TO_AUCTION","state":"READY","reason":"","signature":"minecraft:diamond",
                  "item_id":"minecraft:diamond","item_name":"Diamond","quantity":1,"max_stack_size":64,
                  "acquisition_cost":1,"expected_proceeds":2,"order_unit_reward_cents":1,"target_list_price":2,
                  "conservative_profit":1,"margin_bps":1,"completion_bps":1,
                  "expected_cycle_minutes":1,"risk_adjusted_profit_day":1,"executable_batches":1,"queue_position":1,"order_slots":1,
                  "auction_slots":1,"inventory_slots":1,"profit_per_inventory_slot":1,"confidence_bps":1,"order_tier":"actionable",
                  "order_fresh_at":"2026-08-23T20:00:00Z","focused_fresh_at":"2026-08-23T20:00:00Z","auction_fresh_at":"2026-08-23T20:00:00Z",
                  "order_command":"/orders buy diamond","auction_command":"/ah diamond"}]}
                """;
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decode(json.getBytes(StandardCharsets.UTF_8)));
    }
}
