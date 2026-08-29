package net.donutnetwork.client;

import java.time.Instant;
import java.util.Locale;

/** Durable local truth for an order after the server has accepted its escrow. */
record LocalOrderPosition(String candidateId, String signature, String itemId, String itemName,
                          int batchQuantity, int maxStackSize, int batches, int totalQuantity,
                          long unitRewardCents, long escrowDollars, long targetListPrice,
                          long expectedProceedsPerBatch, int deliveredQuantity, int claimedQuantity,
                          int packagedQuantity, int listedQuantity, State state,
                          Instant createdAt, Instant updatedAt) {
    enum State { PENDING_VERIFICATION, ACTIVE, CLAIM_READY, CLAIM_PENDING, CLAIMING, CLAIMED,
        PACKAGE_PENDING, PACKAGING, LISTING_PENDING, LISTING, EXITED, HOLD }

    LocalOrderPosition {
        candidateId = clean(candidateId, 128);
        signature = clean(signature, 2048);
        itemId = clean(itemId, 128).toLowerCase(Locale.ROOT);
        itemName = clean(itemName, 128);
        if (candidateId.isBlank() || signature.isBlank() || !itemId.matches("[a-z0-9_.-]+:[a-z0-9_./-]+")
                || itemName.isBlank() || batchQuantity <= 0 || maxStackSize <= 0 || maxStackSize > 64
                || batchQuantity > maxStackSize || batches <= 0 || totalQuantity <= 0
                || totalQuantity != Math.multiplyExact(batchQuantity, batches)
                || unitRewardCents <= 0 || escrowDollars <= 0 || targetListPrice <= 0
                || expectedProceedsPerBatch <= 0 || deliveredQuantity < 0 || deliveredQuantity > totalQuantity
                || claimedQuantity < 0 || claimedQuantity > deliveredQuantity
                || packagedQuantity < 0 || packagedQuantity > claimedQuantity
                || listedQuantity < 0 || listedQuantity > packagedQuantity
                || state == null || createdAt == null || updatedAt == null || updatedAt.isBefore(createdAt)) {
            throw new IllegalArgumentException("invalid local order position");
        }
    }

    static LocalOrderPosition submitted(CandidateFeedClient.Candidate candidate, OrderPlan plan, Instant now) {
        return new LocalOrderPosition(plan.candidateId(), plan.signature(), plan.itemId(), plan.itemName(),
                plan.batchQuantity(), candidate.maxStackSize(), plan.batches(), plan.quantity(),
                plan.unitRewardCents(), plan.escrowDollars(), plan.targetListPrice(),
                plan.expectedProceedsPerBatch(), 0, 0, 0, 0, State.PENDING_VERIFICATION, now, now);
    }

    LocalOrderPosition verified(int delivered, Instant now) {
        int bounded = Math.max(deliveredQuantity, Math.min(totalQuantity, delivered));
        State next = bounded == totalQuantity ? State.CLAIM_READY : State.ACTIVE;
        return copy(bounded, next, now);
    }

    LocalOrderPosition completed(Instant now) { return copy(totalQuantity, State.CLAIM_READY, now); }
    LocalOrderPosition withState(State next, Instant now) { return copy(deliveredQuantity, claimedQuantity, packagedQuantity, listedQuantity, next, now); }

    LocalOrderPosition claimed(int quantity, boolean direct, Instant now) {
        int claimed = Math.max(claimedQuantity, Math.min(totalQuantity, quantity));
        int packaged = direct ? Math.max(packagedQuantity, claimed) : packagedQuantity;
        State next = claimed == totalQuantity ? (direct ? State.LISTING : State.CLAIMED) : State.CLAIMING;
        return copy(deliveredQuantity, claimed, packaged, listedQuantity, next, now);
    }

    LocalOrderPosition packaged(int quantity, Instant now) {
        int packaged = Math.max(packagedQuantity, Math.min(claimedQuantity, quantity));
        State next = packaged == totalQuantity ? State.LISTING : State.PACKAGING;
        return copy(deliveredQuantity, claimedQuantity, packaged, listedQuantity, next, now);
    }

    LocalOrderPosition listed(int quantity, Instant now) {
        int listed = Math.max(listedQuantity, Math.min(packagedQuantity, quantity));
        State next = listed == totalQuantity ? State.EXITED : State.LISTING;
        return copy(deliveredQuantity, claimedQuantity, packagedQuantity, listed, next, now);
    }

    int physicalInventorySlots() { return ceilingDivide(totalQuantity, maxStackSize); }
    boolean requiresShulkers() { return physicalInventorySlots() >= AuctionExitPlan.SHULKER_SLOT_THRESHOLD; }

    int remainingToClaim() { return totalQuantity - claimedQuantity; }
    int remainingToPackage() { return claimedQuantity - packagedQuantity; }
    int remainingToList() { return packagedQuantity - listedQuantity; }

    private LocalOrderPosition copy(int delivered, State next, Instant now) {
        return copy(delivered, claimedQuantity, packagedQuantity, listedQuantity, next, now);
    }

    private LocalOrderPosition copy(int delivered, int claimed, int packaged, int listed, State next, Instant now) {
        return new LocalOrderPosition(candidateId, signature, itemId, itemName, batchQuantity, maxStackSize,
                batches, totalQuantity, unitRewardCents, escrowDollars, targetListPrice,
                expectedProceedsPerBatch, delivered, claimed, packaged, listed, next, createdAt, now);
    }

    private static int ceilingDivide(int value, int divisor) { return (value + divisor - 1) / divisor; }
    private static String clean(String value, int limit) {
        String result = value == null ? "" : value.replace('\r', ' ').replace('\n', ' ').strip();
        if (result.length() > limit) throw new IllegalArgumentException("local position text is too long");
        return result;
    }
}
