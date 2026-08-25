package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

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
    }

    private static CandidateFeedClient.Candidate candidate(String id, long cost, long score, int orderSlots, int auctionSlots, int batches) {
        return new CandidateFeedClient.Candidate(id, "ORDER_TO_AUCTION", "READY", "", "minecraft:" + id,
                "minecraft:" + id, id, 64, 64, cost, cost + score, score, 1000, 9000, 30,
                score, batches, 1, orderSlots, auctionSlots, 1, score, 9000, "actionable", Instant.now(), Instant.now(),
                "/orders", "/ah " + id);
    }

    private static CandidateFeedClient.Candidate withState(CandidateFeedClient.Candidate value, String state) {
        return new CandidateFeedClient.Candidate(value.id(), value.route(), state, value.reason(), value.signature(), value.itemId(),
                value.itemName(), value.quantity(), value.maxStackSize(), value.acquisitionCost(), value.expectedProceeds(),
                value.conservativeProfit(), value.marginBps(), value.completionBps(), value.expectedCycleMinutes(),
                value.riskAdjustedProfitDay(), value.executableBatches(), value.queuePosition(), value.orderSlots(), value.auctionSlots(),
                value.inventorySlots(), value.profitInventorySlot(), value.confidenceBps(), value.orderTier(), value.orderFreshAt(),
                value.auctionFreshAt(), value.orderCommand(), value.auctionCommand());
    }
}
