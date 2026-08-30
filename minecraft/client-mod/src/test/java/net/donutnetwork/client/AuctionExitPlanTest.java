package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Instant;

import static org.junit.jupiter.api.Assertions.*;

class AuctionExitPlanTest {
    @Test void oneNormalStackRemainsAnExactQuantityDirectListing() {
        AuctionExitPlan plan = AuctionExitPlan.from(position(64, 64, 1, 30_000));
        assertEquals(AuctionExitPlan.Mode.DIRECT, plan.mode());
        assertEquals(1, plan.physicalInventorySlots());
        assertEquals(0, plan.requiredShulkers());
        assertEquals(1, plan.listings().size());
        assertEquals(64, plan.listings().getFirst().itemQuantity());
        assertEquals(29_000, plan.listings().getFirst().listPriceDollars());
    }

    @Test void twentySixUnstackableItemsRemainDirectButTwentySevenSwitchToOneShulker() {
        AuctionExitPlan direct = AuctionExitPlan.from(position(1, 1, 26, 2_000_000));
        AuctionExitPlan shulker = AuctionExitPlan.from(position(1, 1, 27, 2_000_000));
        assertEquals(AuctionExitPlan.Mode.DIRECT, direct.mode());
        assertEquals(26, direct.listings().size());
        assertEquals(AuctionExitPlan.Mode.SHULKER, shulker.mode());
        assertEquals(1, shulker.requiredShulkers());
        assertEquals(27, shulker.listings().getFirst().itemQuantity());
    }

    @Test void largeTotemOrderBecomesSixFullShulkersAndOnePartialShulker() {
        AuctionExitPlan plan = AuctionExitPlan.from(position(1, 1, 172, 1_800_000));
        assertEquals(AuctionExitPlan.Mode.SHULKER, plan.mode());
        assertEquals(172, plan.physicalInventorySlots());
        assertEquals(7, plan.requiredShulkers());
        assertEquals(27, plan.listings().get(0).itemQuantity());
        assertEquals(10, plan.listings().get(6).itemQuantity());
        assertEquals(172, plan.listings().stream().mapToInt(AuctionExitPlan.Listing::itemQuantity).sum());
        assertEquals(plan.grossTargetDollars(),
                plan.listings().stream().mapToLong(AuctionExitPlan.Listing::grossTargetDollars).sum());
    }

    @Test void stackableBulkUsesPhysicalSlotsInsteadOfNumberOfResaleBatches() {
        AuctionExitPlan plan = AuctionExitPlan.from(position(22, 64, 22, 500_000));
        assertEquals(484, plan.totalQuantity());
        assertEquals(8, plan.physicalInventorySlots());
        assertEquals(AuctionExitPlan.Mode.DIRECT, plan.mode());
        assertEquals(22, plan.listings().size());
    }

    @Test void incompleteOrUnprofitableExitFailsClosed() {
        LocalOrderPosition complete = position(64, 64, 1, 1_000);
        LocalOrderPosition incomplete = new LocalOrderPosition(complete.candidateId(), complete.signature(), complete.itemId(),
                complete.itemName(), complete.batchQuantity(), complete.maxStackSize(), complete.batches(), complete.totalQuantity(),
                complete.unitRewardCents(), complete.escrowDollars(), complete.targetListPrice(), complete.expectedProceedsPerBatch(),
                63, 0, 0, 0, LocalOrderPosition.State.ACTIVE, complete.createdAt(), complete.updatedAt());
        assertThrows(IllegalArgumentException.class, () -> AuctionExitPlan.from(incomplete));
        assertThrows(IllegalArgumentException.class, () -> AuctionExitPlan.from(complete));
    }

    @Test void everyBulkPlanPreservesQuantityAndGrossWithoutOverfilledPackages() {
        for (int maxStack : new int[]{1, 16, 64}) {
            for (int total = 27 * maxStack; total <= 80 * maxStack; total += Math.max(1, maxStack - 1)) {
                LocalOrderPosition position = position(1, maxStack, total, 20_000);
                AuctionExitPlan plan = AuctionExitPlan.from(position);
                assertEquals(AuctionExitPlan.Mode.SHULKER, plan.mode());
                assertEquals(total, plan.listings().stream().mapToInt(AuctionExitPlan.Listing::itemQuantity).sum());
                assertEquals(plan.grossTargetDollars(), plan.listings().stream()
                        .mapToLong(AuctionExitPlan.Listing::grossTargetDollars).sum());
                assertTrue(plan.listings().stream().allMatch(listing -> listing.occupiedContainerSlots() <= 27));
                assertTrue(plan.listings().stream().allMatch(listing -> listing.listPriceDollars() > 0));
            }
        }
    }

    @Test void currentAuctionQuoteReplacesTheFrozenOrderTimeExitPrice() {
        LocalOrderPosition position = position(64, 64, 2, 30_000);
        AuctionExitPlan plan = AuctionExitPlan.from(position, 45_000, 42_000, 1_000);
        assertEquals(90_000, plan.grossTargetDollars());
        assertEquals(44_000, plan.listings().getFirst().listPriceDollars());
        assertEquals(44_000, plan.listings().get(1).listPriceDollars());
    }

    @Test void dynamicQuoteStillRejectsAnExitThatBecameUnprofitable() {
        LocalOrderPosition position = position(64, 64, 1, 30_000);
        assertThrows(IllegalArgumentException.class,
                () -> AuctionExitPlan.from(position, 2_000, 1_000, 1_000));
    }

    private static LocalOrderPosition position(int batchQuantity, int maxStackSize, int batches, long target) {
        Instant now = Instant.parse("2026-08-29T12:00:00Z");
        int total = Math.multiplyExact(batchQuantity, batches);
        return new LocalOrderPosition("candidate", "signature", "minecraft:totem_of_undying", "Totem of Undying",
                batchQuantity, maxStackSize, batches, total, 10_000, 1, target, target,
                total, 0, 0, 0, LocalOrderPosition.State.CLAIM_READY, now, now);
    }
}
