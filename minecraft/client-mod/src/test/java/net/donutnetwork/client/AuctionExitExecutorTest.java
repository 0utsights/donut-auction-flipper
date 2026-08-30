package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class AuctionExitExecutorTest {
    @Test void onlyDurableUnreceiptedEconomicActionsAreIrreversible() {
        assertTrue(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.SUPPLY_PENDING));
        assertTrue(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.CLAIM_PENDING));
        assertTrue(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.PACKAGE_PENDING));
        assertTrue(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.LISTING_PENDING));
        assertFalse(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.CLAIM_READY));
        assertFalse(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.CLAIMED));
        assertFalse(AuctionExitExecutor.irreversibleState(LocalOrderPosition.State.LISTING));
    }

    @Test void immediateCollectMayDeliverTheNextBatchOrTheWholeRemainingOrder() {
        assertTrue(AuctionExitExecutor.validImmediateClaim(1, 1, 21));
        assertTrue(AuctionExitExecutor.validImmediateClaim(21, 1, 21));
        assertTrue(AuctionExitExecutor.validImmediateClaim(64, 64, 512));
        assertFalse(AuctionExitExecutor.validImmediateClaim(63, 64, 512));
        assertFalse(AuctionExitExecutor.validImmediateClaim(513, 64, 512));
    }

    @Test void requestedQuantitySupportsExactLargeCompactOrders() {
        assertTrue(AuctionExitExecutor.textContainsRequested("Amount: 1,500,000 Requested", 1_500_000));
        assertTrue(AuctionExitExecutor.textContainsRequested("1.5M Requested", 1_500_000));
        assertFalse(AuctionExitExecutor.textContainsRequested("1.5M Requested", 1_500_001));
        assertFalse(AuctionExitExecutor.textContainsRequested("1.55M Requested", 1_500_000));
    }

    @Test void completedPersonalOrderUsesExactProgressWithoutBrittleStatusPhrases() {
        String liveRow = "Netherite Scraps $ 1.4M each 21/21 Delivered Click to manage order";
        assertTrue(AuctionExitExecutor.completedOrderTextMatches(liveRow, 21, 140_000_001));
        assertTrue(AuctionExitExecutor.trackedOrderTextMatches(
                "Netherite Scraps $ 1.4M each 7/21 Delivered Click to deliver items", 21, 140_000_001));
        assertFalse(AuctionExitExecutor.completedOrderTextMatches(
                "Netherite Scraps $ 1.4M each 20/21 Delivered Click to deliver items", 21, 140_000_001));
        assertFalse(AuctionExitExecutor.completedOrderTextMatches(liveRow, 22, 140_000_001));
        assertFalse(AuctionExitExecutor.completedOrderTextMatches(liveRow, 21, 150_000_001));
    }
}
