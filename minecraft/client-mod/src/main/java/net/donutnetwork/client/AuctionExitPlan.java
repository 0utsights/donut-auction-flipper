package net.donutnetwork.client;

import java.util.ArrayList;
import java.util.List;

/** Deterministic exact-quantity listing plan produced only for a completely filled order. */
record AuctionExitPlan(String itemId, String itemName, Mode mode, int totalQuantity, int physicalInventorySlots,
                       int requiredShulkers, long grossTargetDollars, long undercutDollars,
                       List<Listing> listings) {
    static final int SHULKER_SLOT_THRESHOLD = 27;
    static final int SHULKER_CAPACITY_SLOTS = 27;
    static final long DEFAULT_UNDERCUT_DOLLARS = 1_000;

    enum Mode { DIRECT, SHULKER }
    record Listing(int sequence, int itemQuantity, int occupiedContainerSlots, long grossTargetDollars,
                   long listPriceDollars, boolean shulker) {}

    AuctionExitPlan {
        listings = List.copyOf(listings);
        if (itemId == null || itemId.isBlank() || itemName == null || itemName.isBlank() || mode == null
                || totalQuantity <= 0 || physicalInventorySlots <= 0 || requiredShulkers < 0
                || grossTargetDollars <= 0 || undercutDollars < 0 || listings.isEmpty()) {
            throw new IllegalArgumentException("invalid auction exit plan");
        }
    }

    static AuctionExitPlan from(LocalOrderPosition position) {
        return from(position, DEFAULT_UNDERCUT_DOLLARS);
    }

    static AuctionExitPlan from(LocalOrderPosition position, long undercutDollars) {
        if (position == null || position.deliveredQuantity() != position.totalQuantity()
                || position.state() == LocalOrderPosition.State.PENDING_VERIFICATION
                || position.state() == LocalOrderPosition.State.ACTIVE
                || position.state() == LocalOrderPosition.State.EXITED
                || position.state() == LocalOrderPosition.State.HOLD) {
            throw new IllegalArgumentException("auction exits require a completely filled, claim-ready order");
        }
        if (undercutDollars < 0) throw new IllegalArgumentException("undercut cannot be negative");
        long gross = Math.multiplyExact(position.targetListPrice(), position.batches());
        int physicalSlots = ceilingDivide(position.totalQuantity(), position.maxStackSize());
        Mode mode = physicalSlots >= SHULKER_SLOT_THRESHOLD ? Mode.SHULKER : Mode.DIRECT;
        List<Listing> listings = mode == Mode.SHULKER
                ? shulkerListings(position, gross, undercutDollars)
                : directListings(position, undercutDollars);
        long conservativeProceeds = Math.multiplyExact(position.expectedProceedsPerBatch(), position.batches());
        long totalUndercut = Math.multiplyExact(undercutDollars, listings.size());
        if (conservativeProceeds <= Math.addExact(position.escrowDollars(), totalUndercut)) {
            throw new IllegalArgumentException("conservative exit profit does not cover listing undercuts");
        }
        int shulkers = mode == Mode.SHULKER ? listings.size() : 0;
        return new AuctionExitPlan(position.itemId(), position.itemName(), mode, position.totalQuantity(),
                physicalSlots, shulkers, gross, undercutDollars, listings);
    }

    private static List<Listing> directListings(LocalOrderPosition position, long undercut) {
        if (position.targetListPrice() <= undercut) {
            throw new IllegalArgumentException("direct listing target does not cover the configured undercut");
        }
        List<Listing> result = new ArrayList<>(position.batches());
        for (int index = 0; index < position.batches(); index++) {
            result.add(new Listing(index + 1, position.batchQuantity(),
                    ceilingDivide(position.batchQuantity(), position.maxStackSize()), position.targetListPrice(),
                    position.targetListPrice() - undercut, false));
        }
        return List.copyOf(result);
    }

    private static List<Listing> shulkerListings(LocalOrderPosition position, long totalGross, long undercut) {
        int capacity = Math.multiplyExact(SHULKER_CAPACITY_SLOTS, position.maxStackSize());
        int remaining = position.totalQuantity();
        long assignedGross = 0;
        List<Listing> result = new ArrayList<>(ceilingDivide(position.physicalInventorySlots(), SHULKER_CAPACITY_SLOTS));
        while (remaining > 0) {
            int quantity = Math.min(capacity, remaining);
            boolean last = quantity == remaining;
            long packageGross = last ? totalGross - assignedGross
                    : multiplyDivideFloor(totalGross, quantity, position.totalQuantity());
            if (packageGross <= undercut) {
                throw new IllegalArgumentException("shulker listing target does not cover the configured undercut");
            }
            result.add(new Listing(result.size() + 1, quantity,
                    ceilingDivide(quantity, position.maxStackSize()), packageGross, packageGross - undercut, true));
            assignedGross = Math.addExact(assignedGross, packageGross);
            remaining -= quantity;
        }
        return List.copyOf(result);
    }

    private static long multiplyDivideFloor(long value, int multiplier, int divisor) {
        long quotient = value / divisor;
        long remainder = value % divisor;
        return Math.addExact(Math.multiplyExact(quotient, multiplier), Math.multiplyExact(remainder, multiplier) / divisor);
    }

    private static int ceilingDivide(int value, int divisor) { return (value + divisor - 1) / divisor; }
}
