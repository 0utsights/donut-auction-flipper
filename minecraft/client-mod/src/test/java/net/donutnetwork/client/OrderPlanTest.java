package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Instant;

import static org.junit.jupiter.api.Assertions.*;

class OrderPlanTest {
    @Test void verifiesCanonicalDonutItemMetadataAcrossLocalizedHumanLabels() {
        assertTrue(OrderPlan.exactItemResultLabel("[item/breeze_rod@items] Breeze Rod",
                "minecraft:breeze_rod", "Breeze Rod", "Breeze Rods"));
        assertTrue(OrderPlan.exactItemResultLabel("[item/breeze_rod@items] Bâton de brise",
                "minecraft:breeze_rod", "Breeze Rod", "Breeze Rods"));
        assertFalse(OrderPlan.exactItemResultLabel("[item/blaze_rod@items] Breeze Rod",
                "minecraft:breeze_rod", "Breeze Rod", "Breeze Rods"));
    }

    @Test void selectsOnlyPlainIceFromDonutsFuzzyIceResults() {
        assertTrue(OrderPlan.exactItemResultLabel("[block/ice] Ice",
                "minecraft:ice", "Ice", "Ice"));
        assertFalse(OrderPlan.exactItemResultLabel("[block/packed_ice] Packed Ice",
                "minecraft:ice", "Ice", "Ice"));
        assertFalse(OrderPlan.exactItemResultLabel("[block/blue_ice] Blue Ice",
                "minecraft:ice", "Ice", "Ice"));
        assertFalse(OrderPlan.exactItemResultLabel("[block/frosted_ice] Frosted Ice",
                "minecraft:ice", "Ice", "Ice"));
    }

    @Test void acceptsCapturedEnchantedGoldenAppleIconAliasOnlyWithAnExactVisibleName() {
        assertTrue(OrderPlan.exactItemResultLabel("[item/golden_apple@items] Enchanted Golden Apple",
                "minecraft:enchanted_golden_apple", "Enchanted Golden Apple", "Enchanted Golden Apple"));
        assertFalse(OrderPlan.exactItemResultLabel("[item/golden_apple@items] Golden Apple",
                "minecraft:enchanted_golden_apple", "Enchanted Golden Apple", "Enchanted Golden Apple"));
        assertFalse(OrderPlan.exactItemResultLabel("[item/apple@items] Enchanted Golden Apple",
                "minecraft:enchanted_golden_apple", "Enchanted Golden Apple", "Enchanted Golden Apple"));
    }

    @Test void preservesCentPreciseRewardAndCheckedStackEscrow() {
        CandidateFeedClient.Candidate candidate = candidate(64, 500_001, 320_001);
        OrderPlan plan = OrderPlan.from(candidate);
        assertEquals("5000.01", plan.priceInput());
        assertEquals(32_000_064, plan.totalCents());
        assertEquals(320_001, plan.escrowDollars());
        assertEquals("diamond_block", plan.itemPathQuery());
    }

    @Test void preservesAnExactSubstackExitWithoutInflatingItToTheStackCap() {
        OrderPlan plan = OrderPlan.from(candidate(32, 500_001, 160_001));
        assertEquals(32, plan.batchQuantity());
        assertEquals(32, plan.quantity());
        assertEquals(160_001, plan.escrowDollars());
    }

    @Test void rejectsBackendEscrowThatDoesNotMatchQuantityAndUnitPrice() {
        assertThrows(IllegalArgumentException.class, () -> OrderPlan.from(candidate(64, 500_001, 320_000)));
    }

    @Test void combinesAllocatedStacksIntoOneOrderWithoutMultiplyingRoundup() {
        CandidateFeedClient.Candidate candidate = candidate(64, 101, 65);
        candidate = withExecutableBatches(candidate, 3);
        OrderPlan plan = OrderPlan.from(candidate, 3);
        assertEquals(64, plan.batchQuantity());
        assertEquals(3, plan.batches());
        assertEquals(192, plan.quantity());
        assertEquals(19_392, plan.totalCents());
        assertEquals(194, plan.escrowDollars());
    }

    @Test void rejectsMoreStacksThanConservativeVolume() {
        assertThrows(IllegalArgumentException.class, () -> OrderPlan.from(candidate(64, 500_001, 320_001), 2));
    }

    @Test void detectsChangedEconomicSnapshot() {
        OrderPlan plan = OrderPlan.from(candidate(64, 500_001, 320_001));
        assertTrue(plan.matches(candidate(64, 500_001, 320_001)));
        assertFalse(plan.matches(candidate(64, 500_002, 320_002)));
    }

    @Test void recognizesVanillaAndDonutWordOrderWithoutSubstringGuessing() {
        assertTrue(OrderPlan.equivalentItemLabel("Block of Diamond", "Block of Diamond", "Diamond Block"));
        assertTrue(OrderPlan.equivalentItemLabel("Diamond Block", "Block of Diamond", "Diamond Block"));
        assertFalse(OrderPlan.equivalentItemLabel("Diamond", "Block of Diamond", "Diamond Block"));
    }

    @Test void validatesExactAndAbbreviatedMoneyButNotPlainQuantities() {
        assertTrue(OrderPlan.textContainsMoney("Price: $5,000.01 each", 500_001));
        assertTrue(OrderPlan.textContainsMoney("Price: $910K each", 91_000_001));
        assertFalse(OrderPlan.textContainsMoney("Amount: 64", 6_400));
        assertFalse(OrderPlan.textContainsMoney("Amount: 64 Maximum", 6_400_000_000L));
        assertFalse(OrderPlan.textContainsMoney("0/1.2K Delivered", 120_000));
        assertTrue(OrderPlan.textContainsMoney("Unit reward: 1.2K", 120_000));
        assertFalse(OrderPlan.textContainsMoney("Price: $910K each", 91_100_001));
        assertTrue(OrderPlan.textContainsMoney("Price: $1.3M each", 139_999_999));
        assertFalse(OrderPlan.textContainsMoney("Price: $1.3M each", 140_000_000));
    }

    @Test void validatesLabeledQuantityWithoutPrefixCollisions() {
        assertTrue(OrderPlan.textContainsQuantity("Amount: 64", 64));
        assertTrue(OrderPlan.textContainsQuantity("Quantity 1,024", 1_024));
        assertFalse(OrderPlan.textContainsQuantity("Amount: 640", 64));
        assertFalse(OrderPlan.textContainsQuantity("64 available", 64));
        assertTrue(OrderPlan.textContainsQuantity("Quantité : 64", 64));
    }

    @Test void parsesExactOrderProgressAndRejectsPartialFills() {
        assertTrue(OrderPlan.textContainsOrderProgress("$ 1.3M each 0/31 Delivered", 31, true));
        assertTrue(OrderPlan.textContainsOrderProgress("12/1.2K Delivered", 1_200, false));
        assertFalse(OrderPlan.textContainsOrderProgress("12/1.2K Delivered", 1_200, true));
    }

    @Test void unitRewardParserCannotMistakeTotalsOrDeliveredQuantitiesForRankPrices() {
        assertTrue(OrderPlan.textContainsUnitReward("$ 1.3M each 0/31 Delivered Total $40.3M", 130_000_001));
        assertEquals(130_000_000, OrderPlan.firstUnitRewardCents("$ 1.3M each 0/31 Delivered Total $40.3M").orElseThrow());
        assertFalse(OrderPlan.textContainsUnitReward("Total $1.3M 0/31 Delivered", 130_000_001));
        assertTrue(OrderPlan.firstUnitRewardCents("Total $1.3M 0/31 Delivered").isEmpty());
    }

    @Test void startsOneCentAboveObservationThenUsesCompetitiveBoundaryOnce() {
        CandidateFeedClient.Candidate value = candidate(31, 140_000_000, 43_400_000);
        value = withObservedReward(value, 130_000_000);
        OrderPlan plan = OrderPlan.from(value);
        assertEquals(130_000_001, plan.unitRewardCents());
        assertEquals(140_000_000, plan.nextUnitReward(43_400_000, 1).orElseThrow());
        OrderPlan replacement = plan.withUnitReward(140_000_000);
        assertTrue(replacement.nextUnitReward(43_400_000, 1).isEmpty());
    }

    @Test void rejectsNonOrderRoute() {
        CandidateFeedClient.Candidate value = candidate(1, 100, 1);
        value = new CandidateFeedClient.Candidate(value.id(), "AUCTION_TO_ORDER", value.state(), value.reason(), value.signature(), value.itemId(),
                value.itemName(), value.quantity(), value.maxStackSize(), value.acquisitionCost(), value.expectedProceeds(), value.observedOrderUnitRewardCents(), value.orderUnitRewardCents(),
                value.targetListPrice(), value.conservativeProfit(), value.marginBps(), value.completionBps(), value.expectedCycleMinutes(),
                value.riskAdjustedProfitDay(), value.executableBatches(), value.queuePosition(), value.orderSlots(), value.auctionSlots(),
                value.inventorySlots(), value.profitInventorySlot(), value.confidenceBps(), value.orderTier(), value.orderFreshAt(), value.focusedFreshAt(), value.auctionFreshAt(),
                value.orderCommand(), value.auctionCommand());
        CandidateFeedClient.Candidate wrongRoute = value;
        assertThrows(IllegalArgumentException.class, () -> OrderPlan.from(wrongRoute));
    }

    private static CandidateFeedClient.Candidate candidate(int quantity, long unitCents, long cost) {
        Instant now = Instant.now();
        return new CandidateFeedClient.Candidate("candidate", "ORDER_TO_AUCTION", "READY", "", "minecraft:diamond_block",
                "minecraft:diamond_block", "Diamond Block", quantity, 64, cost, cost + 100_000, Math.max(1, unitCents - 1), unitCents, cost + 120_000,
                80_000, 2_000, 8_000, 30, 100_000, 1, 1, 1, 1, 1, 80_000,
                9_000, "actionable", now, now, now, "/orders", "/ah diamond_block");
    }

    private static CandidateFeedClient.Candidate withExecutableBatches(CandidateFeedClient.Candidate value, int batches) {
        return new CandidateFeedClient.Candidate(value.id(), value.route(), value.state(), value.reason(), value.signature(), value.itemId(),
                value.itemName(), value.quantity(), value.maxStackSize(), value.acquisitionCost(), value.expectedProceeds(), value.observedOrderUnitRewardCents(), value.orderUnitRewardCents(),
                value.targetListPrice(), value.conservativeProfit(), value.marginBps(), value.completionBps(), value.expectedCycleMinutes(),
                value.riskAdjustedProfitDay(), batches, value.queuePosition(), value.orderSlots(), value.auctionSlots(), value.inventorySlots(),
                value.profitInventorySlot(), value.confidenceBps(), value.orderTier(), value.orderFreshAt(), value.focusedFreshAt(), value.auctionFreshAt(), value.orderCommand(), value.auctionCommand());
    }

    private static CandidateFeedClient.Candidate withObservedReward(CandidateFeedClient.Candidate value, long observed) {
        return new CandidateFeedClient.Candidate(value.id(), value.route(), value.state(), value.reason(), value.signature(), value.itemId(),
                value.itemName(), value.quantity(), value.maxStackSize(), value.acquisitionCost(), value.expectedProceeds(), observed,
                value.orderUnitRewardCents(), value.targetListPrice(), value.conservativeProfit(), value.marginBps(), value.completionBps(),
                value.expectedCycleMinutes(), value.riskAdjustedProfitDay(), value.executableBatches(), value.queuePosition(), value.orderSlots(),
                value.auctionSlots(), value.inventorySlots(), value.profitInventorySlot(), value.confidenceBps(), value.orderTier(),
                value.orderFreshAt(), value.focusedFreshAt(), value.auctionFreshAt(), value.orderCommand(), value.auctionCommand());
    }
}
