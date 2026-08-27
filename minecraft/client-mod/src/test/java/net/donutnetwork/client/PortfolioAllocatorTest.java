package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Random;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.*;

class PortfolioAllocatorTest {
    @Test void neverExceedsCashOrOrderSlots() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        for (int index = 0; index < 30; index++) candidates.add(candidate("item_" + index, 250_000, 100_000 - index, 1, 1, 5));
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 10_000_000, 3, 2);
        long capital = allocation.selections().stream().mapToLong(selection -> selection.candidate().acquisitionCost() * selection.batches()).sum();
        int orders = allocation.selections().stream().mapToInt(selection -> selection.candidate().orderSlots()).sum();
        assertTrue(capital <= allocation.deployable());
        assertTrue(orders <= 17);
        assertFalse(allocation.selections().isEmpty());
    }

    @Test void balanceChangesThePortfolioFrontierWithoutADollarThreshold() {
        CandidateFeedClient.Candidate small = candidate("small", 500_000, 30_000, 1, 1, 1);
        CandidateFeedClient.Candidate large = candidate("large", 3_000_000, 500_000, 1, 1, 1);
        PortfolioAllocator allocator = new PortfolioAllocator();
        PortfolioAllocator.Allocation starter = allocator.allocate(List.of(large, small), 2_000_000, 0, 0);
        PortfolioAllocator.Allocation progressed = allocator.allocate(List.of(large, small), 20_000_000, 0, 0);
        assertTrue(starter.selections().stream().noneMatch(selection -> selection.candidate().id().equals("large")));
        assertTrue(progressed.selections().stream().anyMatch(selection -> selection.candidate().id().equals("large")));
    }

    @Test void researchCandidatesCannotConsumeCapitalOrSlots() {
        CandidateFeedClient.Candidate research = withState(candidate("research", 1, 1_000_000, 1, 1, 100), "RESEARCH");
        assertTrue(new PortfolioAllocator().allocate(List.of(research), 10_000_000, 0, 0).selections().isEmpty());
    }

    @Test void minimumExitProfitScalesWithBalanceAndFreeAuctionSlots() {
        CandidateFeedClient.Candidate tiny = candidate("tiny", 100_000, 1_000, 1, 1, 1);
        CandidateFeedClient.Candidate useful = candidate("useful", 100_000, 10_000, 1, 1, 1);
        PortfolioAllocator allocator = new PortfolioAllocator();
        PortfolioAllocator.Allocation starter = allocator.allocate(List.of(tiny, useful), 1_000_000, 0, 0);
        PortfolioAllocator.Allocation progressed = allocator.allocate(List.of(tiny, useful), 100_000_000, 0, 0);
        assertTrue(starter.minimumProfitPerExit() < progressed.minimumProfitPerExit());
        assertTrue(starter.selections().stream().anyMatch(selection -> selection.candidate().id().equals("useful")));
        assertTrue(progressed.selections().stream().noneMatch(selection -> selection.candidate().id().equals("tiny")));

        PortfolioAllocator.Allocation scarce = allocator.allocate(List.of(useful), 10_000_000, 0, 17);
        assertTrue(scarce.minimumProfitPerExit() > allocator.allocate(List.of(useful), 10_000_000, 0, 0).minimumProfitPerExit());
    }

    @Test void multipleBatchesFromOneBuyOrderUseOneOrderSlot() {
        CandidateFeedClient.Candidate bulk = candidate("bulk", 100_000, 50_000, 1, 1, 5);
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(List.of(bulk), 10_000_000, 19, 0);
        assertEquals(5, allocation.selections().getFirst().batches());
        assertEquals(320, allocation.selections().getFirst().orderQuantity());
    }

    @Test void neverSelectsTwoOrdersForTheSameItem() {
        CandidateFeedClient.Candidate strongest = candidate("same", 100_000, 80_000, 1, 1, 1);
        CandidateFeedClient.Candidate duplicate = new CandidateFeedClient.Candidate("duplicate", strongest.route(), strongest.state(), strongest.reason(),
                strongest.signature(), strongest.itemId(), strongest.itemName(), strongest.quantity(), strongest.maxStackSize(), 90_000,
                strongest.expectedProceeds(), strongest.orderUnitRewardCents(), strongest.targetListPrice(), 70_000, strongest.marginBps(),
                strongest.completionBps(), strongest.expectedCycleMinutes(), 70_000, 1, strongest.queuePosition(), strongest.orderSlots(),
                strongest.auctionSlots(), strongest.inventorySlots(), strongest.profitInventorySlot(), strongest.confidenceBps(), strongest.orderTier(),
                strongest.orderFreshAt(), strongest.focusedFreshAt(), strongest.auctionFreshAt(), strongest.orderCommand(), strongest.auctionCommand());
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(List.of(duplicate, strongest), 10_000_000, 0, 0);
        assertEquals(1, allocation.selections().size());
        assertEquals("same", allocation.selections().getFirst().candidate().id());
    }

    @Test void activePersonalOrderExcludesThatItemFromAllocation() {
        CandidateFeedClient.Candidate active = candidate("active", 100_000, 80_000, 1, 1, 1);
        CandidateFeedClient.Candidate available = candidate("available", 100_000, 70_000, 1, 1, 1);
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(List.of(active, available), 10_000_000, 0, 0,
                Set.of(active.itemId()));
        assertTrue(allocation.selections().stream().noneMatch(selection -> selection.candidate().itemId().equals(active.itemId())));
        assertTrue(allocation.selections().stream().anyMatch(selection -> selection.candidate().itemId().equals(available.itemId())));
    }

    @Test void broadFrontierMaxesDistinctOrderSlotsBeforeIncreasingQuantities() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        for (int index = 0; index < 25; index++) candidates.add(candidate("bulk_" + index, 10_000, 100_000 - index, 1, 1, 18));
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 10_000_000, 0, 0);
        assertEquals(20, allocation.selections().size());
        assertEquals(20, allocation.selectedOrderSlots());
    }

    @Test void broadFrontierCompletesWithinItsDeterministicBudget() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        for (int index = 0; index < 30; index++) {
            candidates.add(candidate("frontier_" + index, 20_000 + index * 1_000L, 100_000 - index * 317L, 1, 1, 18));
        }
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 10_000_000, 0, 0);
        assertEquals(20, allocation.selections().size());
        assertTrue(allocation.selections().stream().mapToInt(PortfolioAllocator.Selection::batches).sum() > 18);
    }

    @Test void largeOrderUsesSequentialExitListingsInsteadOfAnEighteenStackCap() {
        CandidateFeedClient.Candidate bulk = candidate("bulk", 1_000, 100_000, 1, 1, 50_000);
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(List.of(bulk), 10_000_000, 19, 18);
        PortfolioAllocator.Selection selected = allocation.selections().getFirst();
        assertTrue(selected.batches() > 18);
        assertEquals(selected.batches() * 64, selected.orderQuantity());
        assertEquals(0, allocation.availableAuctionSlots());
        assertTrue(selected.capital() <= allocation.deployable() / 4);
    }

    @Test void optimizerChoosesTheHigherCombinedScoreUnderCashPressure() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        candidates.add(candidate("expensive", 150_000, 100_000, 1, 1, 1));
        for (int index = 0; index < 6; index++) candidates.add(candidate("cheap_" + index, 100_000, 70_000, 1, 1, 1));
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 800_000, 0, 0);
        assertEquals(450_000, allocation.riskAdjustedProfitDay());
        assertEquals(6, allocation.selections().size());
        assertTrue(allocation.selections().stream().anyMatch(selection -> selection.candidate().id().equals("expensive")));
    }

    @Test void randomizedPortfoliosRespectEveryLocalAcquisitionBound() {
        Random random = new Random(0xD0A17L);
        PortfolioAllocator allocator = new PortfolioAllocator();
        for (int run = 0; run < 200; run++) {
            long balance = 1_000_000L + random.nextLong(99_000_001L);
            int usedOrders = random.nextInt(21), usedAuctions = random.nextInt(19);
            List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
            for (int index = 0; index < 30; index++) {
                candidates.add(candidate("random_" + run + "_" + index, 1_000 + random.nextLong(2_000_000),
                        1 + random.nextLong(1_000_000), 1, 1, 1 + random.nextInt(100_000)));
            }
            Set<String> active = new HashSet<>();
            for (int index = 0; index < 3; index++) if (random.nextBoolean()) active.add(candidates.get(index).itemId());
            PortfolioAllocator.Allocation allocation = allocator.allocate(candidates, balance, usedOrders, usedAuctions, active);
            assertTrue(allocation.selectedCapital() <= allocation.deployable());
            assertTrue(allocation.selectedOrderSlots() <= allocation.availableOrderSlots());
            assertEquals(allocation.selections().size(), allocation.selections().stream().map(value -> value.candidate().itemId()).distinct().count());
            for (PortfolioAllocator.Selection selection : allocation.selections()) {
                assertFalse(active.contains(selection.candidate().itemId()));
                assertTrue(selection.batches() > 0 && selection.batches() <= selection.candidate().executableBatches());
                assertTrue(selection.capital() <= Math.max(1, allocation.deployable() / 4));
                assertEquals(selection.candidate().quantity() * selection.batches(), selection.orderQuantity());
            }
        }
    }

    private static CandidateFeedClient.Candidate candidate(String id, long cost, long score, int orderSlots, int auctionSlots, int batches) {
        return new CandidateFeedClient.Candidate(id, "ORDER_TO_AUCTION", "READY", "", "minecraft:" + id,
                "minecraft:" + id, id, 64, 64, cost, cost + score, Math.max(1, cost * 100 / 64), cost + score,
                score, 1000, 9000, 30,
                score, batches, 1, orderSlots, auctionSlots, 1, score, 9000, "actionable", Instant.now(), Instant.now(), Instant.now(),
                "/orders", "/ah " + id);
    }

    private static CandidateFeedClient.Candidate withState(CandidateFeedClient.Candidate value, String state) {
        return new CandidateFeedClient.Candidate(value.id(), value.route(), state, value.reason(), value.signature(), value.itemId(),
                value.itemName(), value.quantity(), value.maxStackSize(), value.acquisitionCost(), value.expectedProceeds(),
                value.orderUnitRewardCents(), value.targetListPrice(),
                value.conservativeProfit(), value.marginBps(), value.completionBps(), value.expectedCycleMinutes(),
                value.riskAdjustedProfitDay(), value.executableBatches(), value.queuePosition(), value.orderSlots(), value.auctionSlots(),
                value.inventorySlots(), value.profitInventorySlot(), value.confidenceBps(), value.orderTier(), value.orderFreshAt(),
                value.focusedFreshAt(), value.auctionFreshAt(), value.orderCommand(), value.auctionCommand());
    }
}
