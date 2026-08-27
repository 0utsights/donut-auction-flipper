package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class OrderCreationExecutorTest {
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
}
