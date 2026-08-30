package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.List;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class OrderCreationExecutorTest {
	@Test void defaultAllowlistIncludesDonutsCurrentSrvTarget() {
		assertTrue(Set.of(ClientConfig.DEFAULT_ORDER_SERVER_HOSTS.split(",")).contains("java.donutsmp.net"));
	}

    @Test void recognizesDirectServerDuplicateResponses() {
        assertTrue(OrderCreationExecutor.isDuplicateOrderMessage("You already have an active order for this item."));
        assertTrue(OrderCreationExecutor.isDuplicateOrderMessage("Only create one order per item"));
        assertTrue(OrderCreationExecutor.isDuplicateOrderMessage("A duplicate order is not allowed"));
    }

    @Test void ignoresOrdinaryConversationAboutOrders() {
        assertFalse(OrderCreationExecutor.isDuplicateOrderMessage("I already have my order ready"));
        assertFalse(OrderCreationExecutor.isDuplicateOrderMessage("That order already sold"));
        assertFalse(OrderCreationExecutor.isDuplicateOrderMessage("orders are looking good"));
    }

    @Test void submissionVerificationRemainsAnActiveFailClosedPhase() {
        OrderCreationExecutor.Status pending = new OrderCreationExecutor.Status(
                OrderCreationExecutor.Phase.PENDING_VERIFICATION, "verifying", 1_000, "candidate");
        assertTrue(pending.active());
        assertFalse(new OrderCreationExecutor.Status(OrderCreationExecutor.Phase.IDLE, "idle", 0, "").active());
        assertFalse(new OrderCreationExecutor.Status(OrderCreationExecutor.Phase.ABORTED, "stopped", 0, "").active());
    }

    @Test void automaticQueueCannotExceedConsentTimeEscrow() {
        assertEquals(4, OrderCreationExecutor.authorizedBatches(10, 250_000, 1_000_000));
        assertEquals(2, OrderCreationExecutor.authorizedBatches(2, 250_000, 1_000_000));
        assertEquals(0, OrderCreationExecutor.authorizedBatches(10, 250_000, 249_999));
    }

    @Test void recognizesOnlySemanticallyNamedCreateOrderPlaceholders() {
        assertTrue(OrderCreationExecutor.isCreateOrderControl("minecraft:black_stained_glass_pane", "Click to Create Order"));
        assertTrue(OrderCreationExecutor.isCreateOrderControl("minecraft:gray_stained_glass_pane", "Empty Order Slot"));
        assertTrue(OrderCreationExecutor.isCreateOrderControl("minecraft:lime_stained_glass_pane", "New Order"));
        assertFalse(OrderCreationExecutor.isCreateOrderControl("minecraft:hopper", "Create Order"));
        assertFalse(OrderCreationExecutor.isCreateOrderControl("minecraft:black_stained_glass_pane", "Decoration"));
    }

    @Test void acceptsOnlyTheCapturedEditOrderCancellationControl() {
        assertTrue(OrderCreationExecutor.isCancelOrderControl("minecraft:red_terracotta", "Cancel"));
        assertTrue(OrderCreationExecutor.isCancelOrderControl("minecraft:red_terracotta", "Cancel Order"));
        assertFalse(OrderCreationExecutor.isCancelOrderControl("minecraft:red_terracotta", "Confirm"));
        assertFalse(OrderCreationExecutor.isCancelOrderControl("minecraft:barrier", "Cancel"));
    }

    @Test void skipsOnlyCandidateSpecificChangesBeforeServerNavigation() {
        assertTrue(OrderCreationExecutor.isSkippablePreTransactionChange("armed candidate changed or disappeared"));
        assertTrue(OrderCreationExecutor.isSkippablePreTransactionChange("allocated stack count was reduced"));
        assertFalse(OrderCreationExecutor.isSkippablePreTransactionChange("backend candidate feed is not ready"));
        assertFalse(OrderCreationExecutor.isSkippablePreTransactionChange("local order slots are exhausted"));
    }

    @Test void rebasesOnlyEconomicsThatCanChangeBeforeNavigation() {
        assertTrue(OrderCreationExecutor.isRebasablePreTransactionChange("armed candidate changed or disappeared"));
        assertTrue(OrderCreationExecutor.isRebasablePreTransactionChange("allocated stack count was reduced"));
        assertFalse(OrderCreationExecutor.isRebasablePreTransactionChange("an order for this item is already active or pending"));
        assertFalse(OrderCreationExecutor.isRebasablePreTransactionChange("auction exit is stale"));
    }

    @Test void continuousSessionWaitsThroughTransientDataAndBalanceGaps() {
        assertTrue(OrderCreationExecutor.isTransientWaitError("focused order observation is not fresh yet"));
        assertTrue(OrderCreationExecutor.isTransientWaitError("backend candidate feed is not ready"));
        assertTrue(OrderCreationExecutor.isTransientWaitError("candidate feed is stale"));
        assertTrue(OrderCreationExecutor.isTransientWaitError("auction exit is stale"));
        assertTrue(OrderCreationExecutor.isTransientWaitError("waiting for the live scoreboard balance or a manual override"));
        assertFalse(OrderCreationExecutor.isTransientWaitError("review item does not match the armed item"));
    }

    @Test void recognizesOnlyTheExpectedServerTextInputDescriptor() {
        assertTrue(OrderCreationExecutor.recognizedTextInputDescriptor("search", "Item", Set.of("search")));
        assertTrue(OrderCreationExecutor.recognizedTextInputDescriptor("value", "Price per item", Set.of("price")));
        assertFalse(OrderCreationExecutor.recognizedTextInputDescriptor("notes", "Item", Set.of("price")));
    }

    @Test void recognizesObservedLocalizedItemDialogWithoutWeakSubstringMatching() {
        assertTrue(OrderCreationExecutor.localizedTitleEquals("Choisir un objet", Set.of("Choose Item", "Choisir un objet")));
        assertTrue(OrderCreationExecutor.isItemResultTitle("Choisir un objet (1 résultat)"));
        assertTrue(OrderCreationExecutor.isItemResultTitle("Choose Item (1 result)"));
        assertTrue(OrderCreationExecutor.isItemResultTitle("Choose Item (6 results)"));
        assertTrue(OrderCreationExecutor.isItemResultTitle("Choisir un objet (10 résultats)"));
        assertFalse(OrderCreationExecutor.isItemResultTitle("Choose Item (many results)"));
        assertFalse(OrderCreationExecutor.localizedTitleEquals("Choisir un objet dangereux", Set.of("Choisir un objet")));
    }

    @Test void continuousSessionCanWaitEmptyThenQueueANewDistinctCandidate() {
        PortfolioAllocator.Selection ice = selection("ice-one", "ice");
        assertTrue(OrderCreationExecutor.autoQueueAdditions(
                List.of(), 20, Set.of(), Set.of(), Set.of()).isEmpty());
        assertEquals(List.of(ice), OrderCreationExecutor.autoQueueAdditions(
                List.of(ice), 20, Set.of(), Set.of(), Set.of()));

        PortfolioAllocator.Selection replacement = selection("ice-two", "ice");
        assertEquals(List.of(ice), OrderCreationExecutor.autoQueueAdditions(
                List.of(ice, replacement), 20, Set.of(), Set.of(), Set.of()));
        assertTrue(OrderCreationExecutor.autoQueueAdditions(
                List.of(ice), 20, Set.of(), Set.of("minecraft:ice"), Set.of()).isEmpty());
    }

    @Test void continuousSessionBoundsQueuedItemsToAvailableSlots() {
        PortfolioAllocator.Selection ice = selection("ice-one", "ice");
        PortfolioAllocator.Selection sponge = selection("sponge-one", "sponge");
        assertEquals(List.of(ice), OrderCreationExecutor.autoQueueAdditions(
                List.of(ice, sponge), 1, Set.of(), Set.of(), Set.of()));
    }

    @Test void sessionQuarantineRejectsReplacementCandidatesForTheSameItemButKeepsOthers() {
        PortfolioAllocator.Selection replacementIce = selection("ice-two", "ice");
        PortfolioAllocator.Selection sponge = selection("sponge-one", "sponge");
        assertEquals(List.of(sponge), OrderCreationExecutor.autoQueueAdditions(
                List.of(replacementIce, sponge), 20, Set.of(), Set.of(), Set.of("minecraft:ice")));
    }

    @Test void onlyItemInputPhasesCanBeQuarantinedBeforeSubmission() {
        assertTrue(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.ITEM_SEARCH));
        assertTrue(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.ITEM_RESULT));
        assertTrue(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.AMOUNT));
        assertTrue(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.PRICE));
        assertTrue(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.REVIEW));
        assertFalse(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.ORDER_BOARD));
        assertFalse(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.PENDING_VERIFICATION));
        assertFalse(OrderCreationExecutor.isPreSubmitItemPhase(OrderCreationExecutor.Phase.VERIFY_YOUR_ORDERS));
    }

    private static PortfolioAllocator.Selection selection(String id, String itemPath) {
        Instant now = Instant.parse("2026-08-29T19:00:00Z");
        CandidateFeedClient.Candidate candidate = new CandidateFeedClient.Candidate(id, "ORDER_TO_AUCTION", "READY", "",
                "minecraft:" + itemPath, "minecraft:" + itemPath, itemPath, 64, 64, 1_000, 2_000,
                1_500, 1_501, 2_100, 1_000, 5_000, 8_000, 30, 10_000,
                1, 1, 1, 1, 1, 1_000, 9_000, "actionable", now, now, now,
                "/orders", "/ah " + itemPath);
        return new PortfolioAllocator.Selection(candidate, 1);
    }
}
