package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class AuctionScreenClassifierTest {
    @Test
    void recognizesListingBrowsersConservatively() {
        assertTrue(AuctionScreenClassifier.isListingScreen("Auction House", 54));
        assertTrue(AuctionScreenClassifier.isListingScreen("Browse Auctions - Page 2", 45));
        assertFalse(AuctionScreenClassifier.isListingScreen("Confirm Purchase", 54));
        assertFalse(AuctionScreenClassifier.isListingScreen("Your Auctions", 54));
        assertFalse(AuctionScreenClassifier.isListingScreen("Auction House", 5));
        assertFalse(AuctionScreenClassifier.isListingScreen("Large Chest", 54));
    }
}
