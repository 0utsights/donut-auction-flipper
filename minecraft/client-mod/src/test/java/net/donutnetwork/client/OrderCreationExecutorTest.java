package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
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
}
