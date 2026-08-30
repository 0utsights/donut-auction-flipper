package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.*;

class CandidateFeedClientTest {
    @Test void separatesDurablePositionLocksFromLiveServerOrderLocks() {
        assertEquals(Set.of("minecraft:totem_of_undying"), CandidateFeedClient.liveOrderLocks(
                Set.of("minecraft:netherite_scrap", "minecraft:totem_of_undying"),
                Set.of("minecraft:netherite_scrap")));
        assertThrows(IllegalArgumentException.class,
                () -> CandidateFeedClient.liveOrderLocks(null, Set.of()));
    }

    @Test void validatesAuthoritativePersonalOrderSnapshots() {
        assertEquals(Set.of("minecraft:diamond_block"), CandidateFeedClient.validatedPersonalOrderSnapshot(
                Set.of("minecraft:diamond_block"), 2));
        assertThrows(IllegalArgumentException.class,
                () -> CandidateFeedClient.validatedPersonalOrderSnapshot(Set.of("minecraft:diamond_block"), 0));
        assertThrows(IllegalArgumentException.class,
                () -> CandidateFeedClient.validatedPersonalOrderSnapshot(Set.of("not an item"), 1));
        assertThrows(IllegalArgumentException.class,
                () -> CandidateFeedClient.validatedPersonalOrderSnapshot(Set.of(), 21));
    }

    @Test
    void decodesOnlyFreshSortedUniqueShulkerSupply() {
        Instant now = Instant.parse("2026-08-29T17:00:00Z");
        String valid = """
                {"generated_at":"2026-08-29T16:59:59Z","supplies":[
                  {"auction_id":"a","seller":"SafeSeller","item_id":"minecraft:shulker_box","price":4800,
                   "last_seen":"2026-08-29T16:59:58Z","expires_at":"2026-08-29T18:00:00Z"},
                  {"auction_id":"b","seller":"SafeSeller2","item_id":"minecraft:shulker_box","price":5000,
                   "last_seen":"2026-08-29T16:59:58Z","expires_at":"2026-08-29T18:00:00Z"}]}
                """;
        assertEquals(2, CandidateFeedClient.decodeShulkerSupplies(valid.getBytes(StandardCharsets.UTF_8), now).size());

        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decodeShulkerSupplies(
                valid.replace("4800", "5800").getBytes(StandardCharsets.UTF_8), now));
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decodeShulkerSupplies(
                valid.replace("16:59:58Z", "16:40:00Z").getBytes(StandardCharsets.UTF_8), now));
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decodeShulkerSupplies(
                valid.replace("\"b\"", "\"a\"").getBytes(StandardCharsets.UTF_8), now));
    }
    @Test void decodesEmptyCandidateFeed() {
        String json = """
                {"version":0,"generated_at":"2026-08-23T20:00:00Z","candidates":[]}
                """;
        CandidateFeedClient.DecodedFeed feed = CandidateFeedClient.decode(json.getBytes(StandardCharsets.UTF_8));
        assertTrue(feed.candidates().isEmpty());
    }

    @Test void decodesBoundedCandidateFeed() {
        String json = validFeed();
        CandidateFeedClient.DecodedFeed feed = CandidateFeedClient.decode(json.getBytes(StandardCharsets.UTF_8));
        assertEquals(3, feed.version());
        assertEquals(1, feed.candidates().size());
        assertEquals("READY", feed.candidates().getFirst().state());
        assertEquals(15_625_000, feed.candidates().getFirst().orderUnitRewardCents());
        assertEquals(20_512_820, feed.candidates().getFirst().targetListPrice());
    }

    @Test void decodesExactSubstackExitQuantity() {
        CandidateFeedClient.DecodedFeed feed = CandidateFeedClient.decode(
                validFeed().replace("\"quantity\":64", "\"quantity\":32")
                        .replace("\"acquisition_cost\":10000000", "\"acquisition_cost\":5000000")
                        .getBytes(StandardCharsets.UTF_8));
        assertEquals(32, feed.candidates().getFirst().quantity());
        assertEquals(64, feed.candidates().getFirst().maxStackSize());
    }

    @Test void rejectsExitAboveStackCapacityAndInvalidSlotSemantics() {
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decode(
                validFeed().replace("\"max_stack_size\":64", "\"max_stack_size\":32").getBytes(StandardCharsets.UTF_8)));
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decode(
                validFeed().replace("\"order_slots\":1", "\"order_slots\":0").getBytes(StandardCharsets.UTF_8)));
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decode(
                validFeed().replace("\"acquisition_cost\":10000000", "\"acquisition_cost\":9999999")
                        .getBytes(StandardCharsets.UTF_8)));
    }

    @Test void notModifiedResponseRecoversAnErroredCandidateFeed() {
        Instant now = Instant.parse("2026-08-27T20:00:00Z");
        CandidateFeedClient.Status recovered = CandidateFeedClient.connectedNotModified(
                new CandidateFeedClient.Status("error", Instant.EPOCH, "timeout", 17, 4), now);
        assertEquals("ready", recovered.state());
        assertEquals("connected", recovered.message());
        assertEquals(now, recovered.lastSuccess());
        assertEquals(17, recovered.version());
        assertEquals(4, recovered.candidateCount());
    }

    private static String validFeed() {
        return """
                {"version":3,"generated_at":"2026-08-23T20:00:00Z","candidates":[{
                  "id":"candidate_1","route":"ORDER_TO_AUCTION","state":"READY","reason":"",
                  "signature":"minecraft:diamond_block","item_id":"minecraft:diamond_block","item_name":"Diamond Block",
                  "quantity":64,"max_stack_size":64,"acquisition_cost":10000000,"expected_proceeds":20000000,
                  "observed_order_unit_reward_cents":15000000,"order_unit_reward_cents":15625000,"target_list_price":20512820,
                  "conservative_profit":8000000,"margin_bps":10000,"completion_bps":8000,"expected_cycle_minutes":30,
                  "risk_adjusted_profit_day":307200000,"executable_batches":2,"queue_position":1,"order_slots":1,"auction_slots":1,
                  "inventory_slots":1,"profit_per_inventory_slot":8000000,"confidence_bps":9000,"order_tier":"actionable",
                  "order_fresh_at":"2026-08-23T20:00:00Z","focused_fresh_at":"2026-08-23T20:00:00Z","auction_fresh_at":"2026-08-23T20:00:00Z",
                  "order_command":"/orders","auction_command":"/ah diamond_block"
                }]}
                """;
    }

    @Test void rejectsServerSuppliedTransactionCommand() {
        String json = """
                {"version":1,"generated_at":"2026-08-23T20:00:00Z","candidates":[{
                  "id":"c","route":"ORDER_TO_AUCTION","state":"READY","reason":"","signature":"minecraft:diamond",
                  "item_id":"minecraft:diamond","item_name":"Diamond","quantity":1,"max_stack_size":64,
                  "acquisition_cost":2,"expected_proceeds":3,"observed_order_unit_reward_cents":100,"order_unit_reward_cents":101,"target_list_price":3,
                  "conservative_profit":1,"margin_bps":1,"completion_bps":1,
                  "expected_cycle_minutes":1,"risk_adjusted_profit_day":1,"executable_batches":1,"queue_position":1,"order_slots":1,
                  "auction_slots":1,"inventory_slots":1,"profit_per_inventory_slot":1,"confidence_bps":1,"order_tier":"actionable",
                  "order_fresh_at":"2026-08-23T20:00:00Z","focused_fresh_at":"2026-08-23T20:00:00Z","auction_fresh_at":"2026-08-23T20:00:00Z",
                  "order_command":"/orders buy diamond","auction_command":"/ah diamond"}]}
                """;
        assertThrows(IllegalArgumentException.class, () -> CandidateFeedClient.decode(json.getBytes(StandardCharsets.UTF_8)));
    }

    @Test void parsesCompactDonutSidebarBalance() {
        assertEquals("$ 134M", DonutNetworkClient.composeSidebarRow("$", "134M"));
        assertEquals(134_000_000L, CandidateFeedClient.parseSidebarBalance(
                DonutNetworkClient.composeSidebarRow("$", "134M")).orElseThrow());
        assertEquals(119_000_000L, CandidateFeedClient.parseSidebarBalance("$ 119M").orElseThrow());
        assertEquals(2_500_000L, CandidateFeedClient.parseSidebarBalance("§a$ 2.5M").orElseThrow());
        assertEquals(143_000_000L, CandidateFeedClient.parseSidebarBalance("§f$ 143M\u0080").orElseThrow());
        assertEquals(143_000_000L, CandidateFeedClient.parseSidebarBalance("f$ 143M\u0080").orElseThrow());
        assertEquals(143_000_000L, CandidateFeedClient.parseSidebarBalance("§§f$ 143M\u0080").orElseThrow());
        assertEquals(143_000_000L, CandidateFeedClient.parseSidebarBalance("§f$ 143M§\u0080").orElseThrow());
        assertEquals(32_000L, CandidateFeedClient.parseSidebarBalance("$ 32K").orElseThrow());
        assertTrue(CandidateFeedClient.parseSidebarBalance("MARKWO bought 64 Stone for $10.9K").isEmpty());
        assertTrue(CandidateFeedClient.parseSidebarBalance("not$ 143M").isEmpty());
        assertTrue(CandidateFeedClient.parseSidebarBalance("$ 999999999999999999999T").isEmpty());
    }

    @Test void verifiedCancellationRestoresKnownPreSubmitBalanceWithoutDoubleCrediting() {
        assertEquals(150_000_000L, CandidateFeedClient.reconciledCancellationBalance(126_000_000L, 150_000_000L));
        assertEquals(151_000_000L, CandidateFeedClient.reconciledCancellationBalance(151_000_000L, 150_000_000L));
        assertThrows(IllegalArgumentException.class,
                () -> CandidateFeedClient.reconciledCancellationBalance(-1, 1));
    }
}
