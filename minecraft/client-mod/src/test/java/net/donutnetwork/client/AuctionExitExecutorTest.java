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
}
