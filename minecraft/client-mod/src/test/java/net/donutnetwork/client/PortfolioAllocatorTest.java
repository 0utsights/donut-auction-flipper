package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.*;

class PortfolioAllocatorTest {
    @Test void neverExceedsCashOrMarketSlots() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        for (int index = 0; index < 30; index++) candidates.add(candidate("item_" + index, 250_000, 100_000 - index, 1, 1, 5));
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 10_000_000, 3, 2);
        long capital = allocation.selections().stream().mapToLong(selection -> selection.candidate().acquisitionCost() * selection.batches()).sum();
        int orders = allocation.selections().stream().mapToInt(selection -> selection.candidate().orderSlots()).sum();
        int auctions = allocation.selections().stream().mapToInt(selection -> selection.candidate().auctionSlots() * selection.batches()).sum();
        assertTrue(capital <= allocation.deployable());
        assertTrue(orders <= 17);
        assertTrue(auctions <= 16);
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

    @Test void broadFrontierSpreadsAuctionCapacityAcrossDistinctOrders() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        for (int index = 0; index < 18; index++) candidates.add(candidate("bulk_" + index, 10_000, 100_000 - index, 1, 1, 18));
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 10_000_000, 0, 0);
        assertEquals(18, allocation.selections().size());
        assertTrue(allocation.selections().stream().allMatch(selection -> selection.batches() == 1));
    }

    @Test void broadFrontierCompletesWithinItsDeterministicBudget() {
        List<CandidateFeedClient.Candidate> candidates = new ArrayList<>();
        for (int index = 0; index < 30; index++) {
            candidates.add(candidate("frontier_" + index, 20_000 + index * 1_000L, 100_000 - index * 317L, 1, 1, 18));
        }
        PortfolioAllocator.Allocation allocation = new PortfolioAllocator().allocate(candidates, 10_000_000, 0, 0);
        assertEquals(18, allocation.selections().stream().mapToInt(PortfolioAllocator.Selection::batches).sum());
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
