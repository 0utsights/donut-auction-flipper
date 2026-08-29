package net.donutnetwork.client;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

class OrderPositionStoreTest {
    @TempDir Path directory;

    @Test void roundTripsAValidatedPosition() {
        OrderPositionStore store = new OrderPositionStore(directory.resolve("positions.json"));
        LocalOrderPosition position = position("minecraft:diamond_block");
        store.save(List.of(position));
        assertEquals(position, store.load().get(position.itemId()));
    }

    @Test void rejectsDuplicateItemsOnLoadInsteadOfChoosingOne() throws Exception {
        OrderPositionStore store = new OrderPositionStore(directory.resolve("positions.json"));
        store.save(List.of(position("minecraft:diamond_block")));
        String one = Files.readString(directory.resolve("positions.json"));
        Files.writeString(directory.resolve("positions.json"), "[" + one.substring(1, one.length() - 1) + ","
                + one.substring(1, one.length() - 1) + "]");
        assertTrue(store.load().isEmpty());
    }

    @Test void reportsAnUnwritableDurableTarget() throws Exception {
        Path blockedParent = directory.resolve("blocked-parent");
        Files.writeString(blockedParent, "not a directory");
        Path target = blockedParent.resolve("positions.json");
        OrderPositionStore store = new OrderPositionStore(target);
        assertFalse(store.save(List.of(position("minecraft:diamond_block"))));
    }

    @Test void onlyCompletePositionsAreEligibleForExitPlanning() {
        LocalOrderPosition base = position("minecraft:diamond_block");
        LocalOrderPosition pending = new LocalOrderPosition(base.candidateId(), base.signature(), base.itemId(), base.itemName(),
                base.batchQuantity(), base.maxStackSize(), base.batches(), base.totalQuantity(), base.unitRewardCents(),
                base.escrowDollars(), base.targetListPrice(), base.expectedProceedsPerBatch(), 0,
                0, 0, 0, LocalOrderPosition.State.PENDING_VERIFICATION, base.createdAt(), base.updatedAt());
        LocalOrderPosition active = pending.verified(32, Instant.parse("2026-08-29T12:01:00Z"));
        assertEquals(LocalOrderPosition.State.ACTIVE, active.state());
        assertThrows(IllegalArgumentException.class, () -> AuctionExitPlan.from(active));
        LocalOrderPosition complete = active.completed(Instant.parse("2026-08-29T12:02:00Z"));
        assertDoesNotThrow(() -> AuctionExitPlan.from(complete));
    }

    @Test void durableExitProgressIsMonotonicAndEndsExited() {
        LocalOrderPosition position = position("minecraft:diamond_block");
        Instant first = Instant.parse("2026-08-29T12:01:00Z");
        position = position.claimed(64, true, first);
        assertEquals(LocalOrderPosition.State.LISTING, position.state());
        assertEquals(64, position.packagedQuantity());
        position = position.claimed(10, true, first.plusSeconds(1));
        assertEquals(64, position.claimedQuantity());
        position = position.listed(64, first.plusSeconds(2));
        assertEquals(LocalOrderPosition.State.EXITED, position.state());
        assertEquals(0, position.remainingToList());
    }

    private static LocalOrderPosition position(String itemId) {
        Instant now = Instant.parse("2026-08-29T12:00:00Z");
        return new LocalOrderPosition("candidate", "signature", itemId, "Diamond Block", 64, 64, 1, 64,
                500_001, 320_001, 500_000, 490_000, 64, 0, 0, 0,
                LocalOrderPosition.State.CLAIM_READY, now, now);
    }
}
