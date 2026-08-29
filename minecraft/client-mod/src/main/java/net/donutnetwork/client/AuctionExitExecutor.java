package net.donutnetwork.client;

import net.donutnetwork.client.mixin.DialogScreenAccessor;
import net.minecraft.block.ShulkerBoxBlock;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.gui.Element;
import net.minecraft.client.gui.ParentElement;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.screen.dialog.DialogScreen;
import net.minecraft.client.gui.screen.ingame.GenericContainerScreen;
import net.minecraft.client.gui.screen.ingame.HandledScreen;
import net.minecraft.client.gui.screen.ingame.ShulkerBoxScreen;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.client.input.MouseInput;
import net.minecraft.component.DataComponentTypes;
import net.minecraft.component.type.ContainerComponent;
import net.minecraft.component.type.LoreComponent;
import net.minecraft.dialog.body.DialogBody;
import net.minecraft.dialog.body.ItemDialogBody;
import net.minecraft.dialog.body.PlainMessageDialogBody;
import net.minecraft.dialog.type.Dialog;
import net.minecraft.entity.player.PlayerInventory;
import net.minecraft.item.ItemStack;
import net.minecraft.item.Item;
import net.minecraft.registry.Registries;
import net.minecraft.registry.tag.ItemTags;
import net.minecraft.screen.GenericContainerScreenHandler;
import net.minecraft.screen.ScreenHandler;
import net.minecraft.screen.ShulkerBoxScreenHandler;
import net.minecraft.screen.slot.Slot;
import net.minecraft.screen.slot.SlotActionType;
import net.minecraft.text.Text;
import net.minecraft.util.Hand;
import net.minecraft.util.Identifier;
import net.minecraft.util.hit.BlockHitResult;
import net.minecraft.util.hit.HitResult;
import net.minecraft.util.math.BlockPos;
import net.minecraft.util.math.Vec3d;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.OptionalInt;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Session-scoped, fail-closed order exit workflow. It is the only client path
 * allowed to collect order inventory, acquire empty shulkers, package items,
 * or create auction listings. Mineflayer has no equivalent adapter.
 */
final class AuctionExitExecutor {
    enum Phase {
        IDLE, PREFLIGHT, BUY_OPEN, BUY_SELECT, BUY_CONFIRM, BUY_VERIFY,
        CLAIM_BOARD, CLAIM_YOUR_ORDERS, CLAIM_MANAGE, CLAIM_COLLECT, CLAIM_TRANSFER,
        PACKAGE_PREPARE, PACKAGE_PLACE, PACKAGE_OPEN, PACKAGE_LOAD, PACKAGE_CLOSE,
        PACKAGE_BREAK, PACKAGE_PICKUP, LIST_PREPARE, LIST_COMMAND, LIST_CONFIRM,
        LIST_VERIFY_BOARD, LIST_VERIFY_ITEMS, WAITING, ABORTED
    }

    record Status(Phase phase, String message, boolean enabled, String itemName,
                  int listingNumber, int listingCount) {
        boolean active() { return phase != Phase.IDLE && phase != Phase.WAITING && phase != Phase.ABORTED; }
    }

    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final Pattern AUCTION_TITLE = Pattern.compile("^Auction \\(Page [0-9]+\\)$");
    private static final Pattern ORDERS_TITLE = Pattern.compile("^Orders \\(Page [0-9]+\\)$");
    private static final Duration SCREEN_TIMEOUT = Duration.ofSeconds(8);
    private static final Duration WORKFLOW_TIMEOUT = Duration.ofMinutes(4);
    private static final Duration SUPPLY_MAX_AGE = Duration.ofSeconds(15);
    private static final Duration LISTING_RECEIPT_TIMEOUT = Duration.ofSeconds(8);
    private static final long ACTION_DELAY_NANOS = Duration.ofMillis(350).toNanos();

    private final CandidateFeedClient feed;
    private boolean enabled;
    private Phase phase = Phase.IDLE;
    private String message = "automatic exits are off";
    private LocalOrderPosition position;
    private AuctionExitPlan plan;
    private AuctionExitPlan.Listing listing;
    private Instant workflowAt = Instant.EPOCH;
    private Instant phaseAt = Instant.EPOCH;
    private long nextActionAt;
    private int shulkerCountBefore;
    private int itemCountBefore;
    private int desiredClaimCumulative;
    private int desiredPackageCumulative;
    private int desiredListingCumulative;
    private long selectedSupplyPrice;
    private int selectedSupplySlot = -1;
    private boolean confirmationSent;
    private BlockPos placedShulker;
    private BlockHitResult placementHit;
    private int selectedHotbarBefore = -1;
    private int loadedInOpenShulker;
    private boolean breakingStarted;
    private long shulkerSpendThisWorkflow;
    private int filledShulkerCountBefore;
    private StackAssembler assembler;

    AuctionExitExecutor(CandidateFeedClient feed) { this.feed = feed; }

    Status status() {
        return new Status(phase, message, enabled, position == null ? "" : position.itemName(),
                listing == null ? 0 : listing.sequence(), plan == null ? 0 : plan.listings().size());
    }

    boolean enabled() { return enabled; }

    boolean blocksOrderCreation() {
        if (!enabled || position == null || phase == Phase.IDLE || phase == Phase.ABORTED) return false;
        if (phase != Phase.WAITING) return true;
        return position.claimedQuantity() > position.listedQuantity();
    }

    boolean canEnable(MinecraftClient client) { return serverError(client).isEmpty(); }

    String readiness(MinecraftClient client) {
        String server = serverError(client);
        if (!server.isEmpty()) return server;
        if (feed.readyExitPlans().isEmpty()) return "no completed tracked orders are ready to exit";
        if (feed.usedAuctionSlots() >= 18) return "all 18 local auction slots are marked as used";
        return "completed orders can be claimed and listed";
    }

    void enable(MinecraftClient client) {
        String server = serverError(client);
        if (!server.isEmpty()) {
            message = server;
            tell(client, "Automatic exits remain off: " + server);
            return;
        }
        clearCurrent();
        enabled = true;
        phase = Phase.WAITING;
        message = feed.readyExitPlans().isEmpty()
                ? "waiting for a tracked order to complete"
                : "exit session enabled; preparing the first completed order";
        tell(client, "Automatic exits enabled for this session. Purchases, claims, packaging, and listings remain guarded by exact checks.");
    }

    void disable(MinecraftClient client, String reason) {
        enabled = false;
        assembler = null;
        if (phase != Phase.IDLE && phase != Phase.WAITING && phase != Phase.ABORTED) {
            abort(client, reason == null || reason.isBlank() ? "automatic exits stopped by player" : reason);
            return;
        }
        phase = Phase.IDLE;
        message = reason == null || reason.isBlank() ? "automatic exits are off" : safe(reason);
        clearCurrent();
        tell(client, message);
    }

    void tick(MinecraftClient client) {
        if (!enabled) return;
        if (client.player == null || client.getNetworkHandler() == null || client.interactionManager == null) {
            abort(client, "disconnected during the automatic exit session");
            return;
        }
        if (phase == Phase.IDLE || phase == Phase.WAITING || phase == Phase.ABORTED) {
            if (phase == Phase.ABORTED) return;
            if (phase == Phase.WAITING && position != null) {
                workflowAt = Instant.now();
                transition(Phase.PREFLIGHT, "rechecking paused exit prerequisites");
            } else if (!startNext(client)) {
                return;
            }
        }
        if (System.nanoTime() < nextActionAt) return;
        Instant now = Instant.now();
        if (Duration.between(workflowAt, now).compareTo(WORKFLOW_TIMEOUT) > 0) {
            abort(client, "exit workflow timed out");
            return;
        }
        if (Duration.between(phaseAt, now).compareTo(SCREEN_TIMEOUT) > 0 && expectsServerScreen()) {
            abort(client, "server screen did not advance before its deadline");
            return;
        }
        try {
            switch (phase) {
                case PREFLIGHT -> preflight(client);
                case BUY_OPEN -> buyOpen(client);
                case BUY_SELECT -> buySelect(client);
                case BUY_CONFIRM -> buyConfirm(client);
                case BUY_VERIFY -> buyVerify(client);
                case CLAIM_BOARD -> claimBoard(client);
                case CLAIM_YOUR_ORDERS -> claimYourOrders(client);
                case CLAIM_MANAGE -> claimManage(client);
                case CLAIM_COLLECT -> claimCollect(client);
                case CLAIM_TRANSFER -> claimTransfer(client);
                case PACKAGE_PREPARE -> packagePrepare(client);
                case PACKAGE_PLACE -> packagePlace(client);
                case PACKAGE_OPEN -> packageOpen(client);
                case PACKAGE_LOAD -> packageLoad(client);
                case PACKAGE_CLOSE -> packageClose(client);
                case PACKAGE_BREAK -> packageBreak(client);
                case PACKAGE_PICKUP -> packagePickup(client);
                case LIST_PREPARE -> listPrepare(client);
                case LIST_COMMAND -> listCommand(client);
                case LIST_CONFIRM -> listConfirm(client);
                case LIST_VERIFY_BOARD -> listVerifyBoard(client);
                case LIST_VERIFY_ITEMS -> listVerifyItems(client);
                default -> { }
            }
        } catch (RuntimeException error) {
            abort(client, "screen or inventory verification failed: " + safe(error.getMessage()));
        }
    }

    private boolean startNext(MinecraftClient client) {
        if (feed.usedAuctionSlots() >= 18) {
            phase = Phase.WAITING;
            message = "waiting for a free auction slot (18/18 locally used)";
            return false;
        }
        Optional<LocalOrderPosition> next = feed.orderPositions().stream()
                .filter(AuctionExitExecutor::canResume).findFirst();
        if (next.isEmpty()) {
            phase = Phase.WAITING;
            message = "waiting for a tracked order to complete";
            return false;
        }
        position = next.get();
        plan = AuctionExitPlan.from(position);
        workflowAt = Instant.now();
        selectNextListing();
        transition(Phase.PREFLIGHT, "checking exact exit prerequisites");
        return true;
    }

    private static boolean canResume(LocalOrderPosition value) {
        return value.deliveredQuantity() == value.totalQuantity()
                && value.state() != LocalOrderPosition.State.EXITED && value.state() != LocalOrderPosition.State.HOLD;
    }

    private void selectNextListing() {
        int cumulative = 0;
        listing = null;
        for (AuctionExitPlan.Listing candidate : plan.listings()) {
            cumulative = Math.addExact(cumulative, candidate.itemQuantity());
            if (position.listedQuantity() < cumulative) {
                listing = candidate;
                desiredListingCumulative = cumulative;
                desiredPackageCumulative = cumulative;
                desiredClaimCumulative = cumulative;
                return;
            }
        }
        throw new IllegalStateException("position has no remaining listing");
    }

    private void preflight(MinecraftClient client) {
        String server = serverError(client);
        if (!server.isEmpty()) throw new IllegalStateException(server);
        if (client.currentScreen != null) {
            phase = Phase.WAITING;
            message = "close the open screen so the next exit can start safely";
            return;
        }
        if (!vanillaSignature(position)) {
            throw new IllegalStateException("automatic exits currently require a vanilla item signature; modifiers need manual review");
        }
        if ((position.state() == LocalOrderPosition.State.CLAIM_PENDING && position.claimedQuantity() < desiredClaimCumulative)
                || (position.state() == LocalOrderPosition.State.PACKAGE_PENDING && position.packagedQuantity() < desiredPackageCumulative)
                || (position.state() == LocalOrderPosition.State.LISTING_PENDING && position.listedQuantity() < desiredListingCumulative)) {
            throw new IllegalStateException("an irreversible action was interrupted before its receipt; reconcile this position manually");
        }
        if (feed.usedAuctionSlots() >= 18) {
            phase = Phase.WAITING;
            message = "waiting for a free auction slot";
            return;
        }
        if (plan.mode() == AuctionExitPlan.Mode.SHULKER
                && position.packagedQuantity() < desiredPackageCumulative) {
            int owned = countEmptyShulkers(client.player.getInventory());
            if (owned < 1) {
                if (Duration.between(feed.shulkerSupplySuccess(), Instant.now()).compareTo(SUPPLY_MAX_AGE) > 0
                        || feed.shulkerSupplies().isEmpty()) {
                    phase = Phase.WAITING;
                    message = "waiting for a fresh backend empty-shulker supply quote";
                    return;
                }
                transition(Phase.BUY_OPEN, "opening the lowest-price empty-shulker market");
                return;
            }
        }
        routeAfterSupply(client);
    }

    private void routeAfterSupply(MinecraftClient client) {
        if (position.packagedQuantity() >= desiredPackageCumulative) {
            transition(Phase.LIST_PREPARE, "preparing the next exact auction listing");
        } else if (position.claimedQuantity() >= desiredClaimCumulative) {
            transition(plan.mode() == AuctionExitPlan.Mode.SHULKER ? Phase.PACKAGE_PREPARE : Phase.LIST_PREPARE,
                    plan.mode() == AuctionExitPlan.Mode.SHULKER ? "ready to package claimed items" : "ready to list claimed items");
        } else {
            int neededSlots = ceilingDivide(desiredClaimCumulative - position.claimedQuantity(), position.maxStackSize());
            if (freeInventorySlots(client.player.getInventory()) < neededSlots) {
                phase = Phase.WAITING;
                message = "free " + neededSlots + " inventory slots to collect the next exact exit batch";
                return;
            }
            client.getNetworkHandler().sendChatCommand("orders");
            transition(Phase.CLAIM_BOARD, "opening completed personal order");
            delay();
        }
    }

    private void buyOpen(MinecraftClient client) {
        client.getNetworkHandler().sendChatCommand("auction shulker_box");
        transition(Phase.BUY_SELECT, "verifying lowest-price empty shulkers");
        delay();
    }

    private void buySelect(MinecraftClient client) {
        Screen screen = client.currentScreen;
        if (!(screen instanceof GenericContainerScreen) || !AUCTION_TITLE.matcher(title(screen)).matches()) return;
        GenericContainerScreenHandler handler = genericHandler(client);
        ItemStack filter = handler.getSlot(47).getStack();
        if (!label(filter).equals("filter") || !normalizedText(filter).contains("lowest price")) {
            throw new IllegalStateException("auction search is not sorted by Lowest Price");
        }
        long backendLowest = feed.shulkerSupplies().getFirst().price();
        long perBoxCap = Math.min(Math.max(backendLowest + 2_000, multiplyDivideCeil(backendLowest, 12, 10)),
                Math.max(25_000, feed.balance() / 400));
        long aggregateCap = Math.max(25_000, feed.balance() / 100);
        int missing = remainingShulkersNeeded(client);
        selectedSupplySlot = -1;
        selectedSupplyPrice = 0;
        int limit = Math.min(45, handler.getInventory().size());
        for (int slot = 0; slot < limit; slot++) {
            ItemStack row = handler.getSlot(slot).getStack();
            Optional<Long> price = safeEmptyShulkerAuctionRow(row);
            if (price.isEmpty()) continue;
            if (price.get() > perBoxCap || Math.multiplyExact(price.get(), Math.max(1, missing)) > aggregateCap) {
                throw new IllegalStateException("empty-shulker supply exceeds the balance-scaled safety cap");
            }
            long projectedSpend = Math.addExact(shulkerSpendThisWorkflow,
                    Math.multiplyExact(price.get(), Math.max(1, missing)));
            if (projectedSpend >= conservativeProfitAfterUndercuts()) {
                throw new IllegalStateException("empty-shulker cost would consume the remaining conservative profit");
            }
            selectedSupplySlot = slot;
            selectedSupplyPrice = price.get();
            break;
        }
        if (selectedSupplySlot < 0) throw new IllegalStateException("no exact empty shulker row was available on Lowest Price page 1");
        shulkerCountBefore = countEmptyShulkers(client.player.getInventory());
        confirmationSent = false;
        clickSlot(client, selectedSupplySlot, 0, SlotActionType.PICKUP);
        transition(Phase.BUY_CONFIRM, "confirming one exact empty shulker at $" + selectedSupplyPrice);
        delay();
    }

    private void buyConfirm(MinecraftClient client) {
        if (countEmptyShulkers(client.player.getInventory()) == shulkerCountBefore + 1) {
            transition(Phase.BUY_VERIFY, "verifying empty-shulker receipt");
            return;
        }
        if (!(client.currentScreen instanceof DialogScreen<?> dialogScreen) || !(dialogScreen instanceof DialogScreenAccessor accessor)) return;
        Dialog dialog = accessor.donut$getDialog();
        String rawBody = dialogText(dialog);
        String body = OrderPlan.normalizeLabel(rawBody);
        if (!body.contains("shulker box") || !moneyTextMatches(rawBody, selectedSupplyPrice)) {
            throw new IllegalStateException("shulker purchase confirmation does not match item and price");
        }
        if (!confirmationSent) {
            requireButton(dialogScreen, Set.of("buy", "confirm", "confirm purchase")).onPress(new MouseInput(0, 0));
            confirmationSent = true;
            phaseAt = Instant.now();
            delay();
        }
    }

    private void buyVerify(MinecraftClient client) {
        if (countEmptyShulkers(client.player.getInventory()) != shulkerCountBefore + 1) return;
        shulkerSpendThisWorkflow = Math.addExact(shulkerSpendThisWorkflow, selectedSupplyPrice);
        closeScreen(client);
        transition(Phase.PREFLIGHT, "empty shulker received; rechecking the exact exit batch");
        delay();
    }

    private void claimBoard(MinecraftClient client) {
        Screen screen = client.currentScreen;
        if (!(screen instanceof GenericContainerScreen) || !ORDERS_TITLE.matcher(title(screen)).matches()) return;
        ItemStack control = genericHandler(client).getSlot(51).getStack();
        if (!label(control).equals("your orders")) throw new IllegalStateException("Your Orders control did not match slot 51");
        clickSlot(client, 51, 0, SlotActionType.PICKUP);
        transition(Phase.CLAIM_YOUR_ORDERS, "opening exact completed order");
        delay();
    }

    private void claimYourOrders(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !title(client.currentScreen).equals("Orders -> Your Orders")) return;
        GenericContainerScreenHandler handler = genericHandler(client);
        List<Integer> matches = new ArrayList<>();
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int slot = 0; slot < limit; slot++) {
            ItemStack row = handler.getSlot(slot).getStack();
            if (itemMatches(row, position.itemId()) && normalizedText(row).contains("order completed")
                    && normalizedText(row).contains(position.totalQuantity() + "/" + position.totalQuantity() + " delivered")
                    && textContainsRequested(stackText(row), position.totalQuantity())
                    && OrderPlan.textContainsUnitReward(stackText(row), position.unitRewardCents())) matches.add(slot);
        }
        if (matches.size() != 1) throw new IllegalStateException("completed personal order is missing or ambiguous");
        clickSlot(client, matches.getFirst(), 0, SlotActionType.PICKUP);
        transition(Phase.CLAIM_MANAGE, "opening verified Collect control");
        delay();
    }

    private void claimManage(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !title(client.currentScreen).equals("Orders -> Edit Order")) return;
        GenericContainerScreenHandler handler = genericHandler(client);
        ItemStack identity = handler.getSlot(10).getStack();
        ItemStack collect = handler.getSlot(13).getStack();
        if (!itemMatches(identity, position.itemId()) || !normalizedText(identity).contains("order completed")
                || !textContainsRequested(stackText(identity), position.totalQuantity())
                || !OrderPlan.textContainsUnitReward(stackText(identity), position.unitRewardCents())
                || !label(collect).equals("collect") || !Registries.ITEM.getId(collect.getItem()).toString().equals("minecraft:chest")) {
            throw new IllegalStateException("completed order identity or Collect control changed");
        }
        itemCountBefore = countPlainItem(client.player.getInventory(), position.itemId());
        feed.recordExitState(position.itemId(), LocalOrderPosition.State.CLAIM_PENDING);
        position = feed.orderPosition(position.itemId()).orElseThrow();
        clickSlot(client, 13, 0, SlotActionType.PICKUP);
        transition(Phase.CLAIM_COLLECT, "opening server-held order inventory");
        delay();
    }

    private void claimCollect(MinecraftClient client) {
        int gained = countPlainItem(client.player.getInventory(), position.itemId()) - itemCountBefore;
        int needed = desiredClaimCumulative - position.claimedQuantity();
        if (gained > 0) {
            if (gained != needed) throw new IllegalStateException("immediate Collect moved an unexpected item quantity");
            acceptClaim(client, gained);
            return;
        }
        if (!(client.currentScreen instanceof GenericContainerScreen) || title(client.currentScreen).equals("Orders -> Edit Order")) return;
        GenericContainerScreenHandler handler = genericHandler(client);
        int matching = countMenuPlainItem(handler, position.itemId());
        if (matching < needed) throw new IllegalStateException("collection inventory does not contain the exact next exit quantity");
        transition(Phase.CLAIM_TRANSFER, "moving one exact exit batch from server-held inventory");
    }

    private void claimTransfer(MinecraftClient client) {
        GenericContainerScreenHandler handler = genericHandler(client);
        int needed = desiredClaimCumulative - position.claimedQuantity();
        int gained = countPlainItem(client.player.getInventory(), position.itemId()) - itemCountBefore;
        if (gained == needed) {
            closeScreen(client);
            acceptClaim(client, gained);
            return;
        }
        if (gained < 0 || gained > needed) throw new IllegalStateException("collection transfer quantity changed unexpectedly");
        int remaining = needed - gained;
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int slot = 0; slot < limit; slot++) {
            ItemStack stack = handler.getSlot(slot).getStack();
            if (!plainItemMatches(stack, position.itemId())) continue;
            if (stack.getCount() > remaining) throw new IllegalStateException("partial server-held stack transfer is not yet schema-safe");
            clickSlot(client, slot, 0, SlotActionType.QUICK_MOVE);
            delay();
            return;
        }
        throw new IllegalStateException("exact server-held item rows disappeared during collection");
    }

    private void acceptClaim(MinecraftClient client, int gained) {
        int total = Math.addExact(position.claimedQuantity(), gained);
        feed.recordClaimed(position.itemId(), total, plan.mode() == AuctionExitPlan.Mode.DIRECT);
        position = feed.orderPosition(position.itemId()).orElseThrow();
        closeScreen(client);
        if (plan.mode() == AuctionExitPlan.Mode.DIRECT) transition(Phase.LIST_PREPARE, "preparing exact claimed stack for auction");
        else transition(Phase.PACKAGE_PREPARE, "look at a solid block with an empty adjacent placement space");
        delay();
    }

    private void packagePrepare(MinecraftClient client) {
        if (position.claimedQuantity() < desiredPackageCumulative) throw new IllegalStateException("package quantity was not fully claimed");
        if (!(client.crosshairTarget instanceof BlockHitResult hit) || hit.getType() != HitResult.Type.BLOCK) {
            phase = Phase.WAITING;
            message = "look at a solid block with room beside it, then the exit session will resume";
            return;
        }
        BlockPos target = hit.getBlockPos().offset(hit.getSide());
        if (!client.world.getBlockState(target).isReplaceable() || client.world.getBlockState(hit.getBlockPos()).isReplaceable()) {
            phase = Phase.WAITING;
            message = "the highlighted shulker placement space is not safe and empty";
            return;
        }
        int emptyShulkerIndex = findEmptyShulker(client.player.getInventory());
        if (emptyShulkerIndex < 0) throw new IllegalStateException("required empty shulker is no longer in inventory");
        selectedHotbarBefore = client.player.getInventory().getSelectedSlot();
        feed.recordExitState(position.itemId(), LocalOrderPosition.State.PACKAGE_PENDING);
        position = feed.orderPosition(position.itemId()).orElseThrow();
        int hotbar = moveInventoryItemToHotbar(client, emptyShulkerIndex);
        client.player.getInventory().setSelectedSlot(hotbar);
        placementHit = hit;
        placedShulker = target.toImmutable();
        filledShulkerCountBefore = countFilledShulkers(client.player.getInventory(), position.itemId(), listing.itemQuantity());
        transition(Phase.PACKAGE_PLACE, "placing one verified empty shulker");
        delay();
    }

    private void packagePlace(MinecraftClient client) {
        if (client.world.getBlockState(placedShulker).getBlock() instanceof ShulkerBoxBlock) {
            transition(Phase.PACKAGE_OPEN, "opening placed shulker");
            return;
        }
        ItemStack held = client.player.getMainHandStack();
        if (!isEmptyShulker(held) || !client.world.getBlockState(placedShulker).isReplaceable()) {
            throw new IllegalStateException("empty shulker or placement target changed before placement");
        }
        client.interactionManager.interactBlock(client.player, Hand.MAIN_HAND, placementHit);
        delay();
    }

    private void packageOpen(MinecraftClient client) {
        if (client.currentScreen instanceof ShulkerBoxScreen && client.player.currentScreenHandler instanceof ShulkerBoxScreenHandler) {
            loadedInOpenShulker = countOpenShulkerItem(client, position.itemId());
            if (countOpenShulkerOtherItems(client, position.itemId()) != 0) throw new IllegalStateException("placed shulker is not empty");
            transition(Phase.PACKAGE_LOAD, "loading exact package quantity");
            return;
        }
        if (!(client.world.getBlockState(placedShulker).getBlock() instanceof ShulkerBoxBlock)) {
            throw new IllegalStateException("placed shulker disappeared before opening");
        }
        BlockHitResult openHit = new BlockHitResult(Vec3d.ofCenter(placedShulker), placementHit.getSide(), placedShulker, false);
        client.interactionManager.interactBlock(client.player, Hand.MAIN_HAND, openHit);
        delay();
    }

    private void packageLoad(MinecraftClient client) {
        if (!(client.player.currentScreenHandler instanceof ShulkerBoxScreenHandler handler)) throw new IllegalStateException("shulker screen closed during packaging");
        int target = listing.itemQuantity();
        int loaded = countOpenShulkerItem(client, position.itemId());
        if (countOpenShulkerOtherItems(client, position.itemId()) != 0 || loaded > target) throw new IllegalStateException("shulker contents changed unexpectedly");
        if (loaded == target) {
            loadedInOpenShulker = loaded;
            transition(Phase.PACKAGE_CLOSE, "closing verified package");
            return;
        }
        int remaining = target - loaded;
        for (Slot slot : handler.slots) {
            if (slot.inventory != client.player.getInventory()) continue;
            ItemStack stack = slot.getStack();
            if (!plainItemMatches(stack, position.itemId())) continue;
            if (stack.getCount() > remaining) throw new IllegalStateException("partial inventory stack cannot be loaded without changing exact quantity");
            clickSlot(client, slot.id, 0, SlotActionType.QUICK_MOVE);
            delay();
            return;
        }
        throw new IllegalStateException("claimed items are missing while packaging");
    }

    private void packageClose(MinecraftClient client) {
        if (countOpenShulkerItem(client, position.itemId()) != listing.itemQuantity()
                || countOpenShulkerOtherItems(client, position.itemId()) != 0) {
            throw new IllegalStateException("shulker contents do not match the exit plan");
        }
        closeScreen(client);
        breakingStarted = false;
        transition(Phase.PACKAGE_BREAK, "recovering the exact filled shulker");
        delay();
    }

    private void packageBreak(MinecraftClient client) {
        if (client.world.getBlockState(placedShulker).isAir()) {
            transition(Phase.PACKAGE_PICKUP, "waiting for filled shulker pickup");
            return;
        }
        if (!(client.world.getBlockState(placedShulker).getBlock() instanceof ShulkerBoxBlock)) {
            throw new IllegalStateException("recorded package block is no longer a shulker");
        }
        if (!breakingStarted) {
            if (!client.interactionManager.attackBlock(placedShulker, placementHit.getSide())) {
                throw new IllegalStateException("client refused to start breaking the packaged shulker");
            }
            breakingStarted = true;
        }
        client.interactionManager.updateBlockBreakingProgress(placedShulker, placementHit.getSide());
    }

    private void packagePickup(MinecraftClient client) {
        if (countFilledShulkers(client.player.getInventory(), position.itemId(), listing.itemQuantity())
                != filledShulkerCountBefore + 1) return;
        int packaged = Math.addExact(position.packagedQuantity(), listing.itemQuantity());
        feed.recordPackaged(position.itemId(), packaged);
        position = feed.orderPosition(position.itemId()).orElseThrow();
        if (selectedHotbarBefore >= 0) client.player.getInventory().setSelectedSlot(selectedHotbarBefore);
        placedShulker = null;
        placementHit = null;
        transition(Phase.LIST_PREPARE, "preparing filled shulker auction listing");
        delay();
    }

    private void listPrepare(MinecraftClient client) {
        if (feed.usedAuctionSlots() >= 18) {
            phase = Phase.WAITING;
            message = "waiting for a free auction slot";
            return;
        }
        closeScreen(client);
        if (assembler == null) {
            int inventoryIndex = listing.shulker()
                    ? findFilledShulker(client.player.getInventory(), position.itemId(), listing.itemQuantity())
                    : findExactPlainItem(client.player.getInventory(), position.itemId(), listing.itemQuantity());
            if (inventoryIndex >= 0) {
                int hotbar = moveInventoryItemToHotbar(client, inventoryIndex);
                client.player.getInventory().setSelectedSlot(hotbar);
                transition(Phase.LIST_COMMAND, "holding exact listing item");
                delay();
                return;
            }
            if (listing.shulker()) throw new IllegalStateException("filled package is missing from inventory");
            assembler = new StackAssembler(position.itemId(), listing.itemQuantity());
        }
        if (assembler.tick(client)) {
            assembler = null;
            transition(Phase.LIST_COMMAND, "assembled exact listing stack");
            delay();
        }
    }

    private void listCommand(MinecraftClient client) {
        ItemStack held = client.player.getMainHandStack();
        if (!listingInventoryStackMatches(held)) throw new IllegalStateException("held item no longer matches exact listing");
        itemCountBefore = listing.shulker() ? countFilledShulkers(client.player.getInventory(), position.itemId(), listing.itemQuantity())
                : countPlainItem(client.player.getInventory(), position.itemId());
        confirmationSent = false;
        feed.recordExitState(position.itemId(), LocalOrderPosition.State.LISTING_PENDING);
        position = feed.orderPosition(position.itemId()).orElseThrow();
        client.getNetworkHandler().sendChatCommand("auction sell " + listing.listPriceDollars());
        transition(Phase.LIST_CONFIRM, "confirming exact auction price $" + listing.listPriceDollars());
        delay();
    }

    private void listConfirm(MinecraftClient client) {
        if (listingInventoryDecreased(client)) {
            client.getNetworkHandler().sendChatCommand("auction");
            transition(Phase.LIST_VERIFY_BOARD, "verifying listing in Your Items");
            delay();
            return;
        }
        if (Duration.between(phaseAt, Instant.now()).compareTo(LISTING_RECEIPT_TIMEOUT) > 0) {
            throw new IllegalStateException("auction listing did not produce a verified inventory receipt");
        }
        if (!(client.currentScreen instanceof DialogScreen<?> dialogScreen) || !(dialogScreen instanceof DialogScreenAccessor accessor)) return;
        Dialog dialog = accessor.donut$getDialog();
        if (!dialogContainsListing(dialog) || !moneyTextMatches(dialogText(dialog), listing.listPriceDollars())) {
            throw new IllegalStateException("auction confirmation does not match exact item, contents, quantity, and price");
        }
        if (!confirmationSent) {
            requireButton(dialogScreen, Set.of("sell", "confirm", "confirm listing", "create auction")).onPress(new MouseInput(0, 0));
            confirmationSent = true;
            phaseAt = Instant.now();
            delay();
        }
    }

    private void listVerifyBoard(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !AUCTION_TITLE.matcher(title(client.currentScreen)).matches()) return;
        ItemStack control = genericHandler(client).getSlot(51).getStack();
        if (!label(control).equals("your items")) throw new IllegalStateException("Your Items control did not match slot 51");
        clickSlot(client, 51, 0, SlotActionType.PICKUP);
        transition(Phase.LIST_VERIFY_ITEMS, "finding exact listing in personal auctions");
        delay();
    }

    private void listVerifyItems(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !OrderPlan.normalizeLabel(title(client.currentScreen)).contains("your items")) return;
        GenericContainerScreenHandler handler = genericHandler(client);
        int matches = 0;
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int slot = 0; slot < limit; slot++) {
            ItemStack row = handler.getSlot(slot).getStack();
            if (listingMarketStackMatches(row) && firstMoney(row).orElse(-1L) == listing.listPriceDollars()) matches++;
        }
        if (matches != 1) throw new IllegalStateException("personal auctions do not contain exactly one matching listing");
        feed.recordListed(position.itemId(), desiredListingCumulative);
        Optional<LocalOrderPosition> updated = feed.orderPosition(position.itemId());
        if (updated.isEmpty()) {
            tell(client, "Completed all " + plan.listings().size() + " verified auction exits for " + position.itemName() + ".");
            clearCurrent();
            phase = Phase.WAITING;
            message = "completed one order exit; waiting for the next filled order";
            closeScreen(client);
            return;
        }
        position = updated.get();
        selectNextListing();
        closeScreen(client);
        transition(Phase.PREFLIGHT, "preparing exit " + listing.sequence() + " of " + plan.listings().size());
        delay();
    }

    private boolean listingInventoryDecreased(MinecraftClient client) {
        int current = listing.shulker() ? countFilledShulkers(client.player.getInventory(), position.itemId(), listing.itemQuantity())
                : countPlainItem(client.player.getInventory(), position.itemId());
        return listing.shulker() ? current == itemCountBefore - 1 : current == itemCountBefore - listing.itemQuantity();
    }

    private boolean listingInventoryStackMatches(ItemStack stack) {
        return listing.shulker() ? filledShulkerMatches(stack, position.itemId(), listing.itemQuantity())
                : plainItemMatches(stack, position.itemId()) && stack.getCount() == listing.itemQuantity();
    }

    private boolean listingMarketStackMatches(ItemStack stack) {
        return listing.shulker() ? filledShulkerMatches(stack, position.itemId(), listing.itemQuantity())
                : itemMatches(stack, position.itemId()) && stack.getCount() == listing.itemQuantity();
    }

    private boolean dialogContainsListing(Dialog dialog) {
        for (DialogBody body : dialog.common().body()) {
            if (body instanceof ItemDialogBody item && listingInventoryStackMatches(item.item())) return true;
        }
        return false;
    }

    private boolean expectsServerScreen() {
        return switch (phase) {
            case BUY_SELECT, BUY_CONFIRM, CLAIM_BOARD, CLAIM_YOUR_ORDERS, CLAIM_MANAGE, CLAIM_COLLECT,
                    LIST_CONFIRM, LIST_VERIFY_BOARD, LIST_VERIFY_ITEMS -> true;
            default -> false;
        };
    }

    private void transition(Phase next, String detail) {
        phase = next;
        phaseAt = Instant.now();
        message = detail;
    }

    private void delay() { nextActionAt = System.nanoTime() + ACTION_DELAY_NANOS; }

    private void abort(MinecraftClient client, String reason) {
        String safeReason = safe(reason);
        LOGGER.warn("Auction exit aborted in {}: {}", phase, safeReason);
        if (position != null) {
            try {
                feed.recordExitState(position.itemId(), LocalOrderPosition.State.HOLD);
            } catch (RuntimeException persistenceFailure) {
                LOGGER.error("Could not persist HOLD after auction-exit abort: {}", safe(persistenceFailure.getMessage()));
            }
        }
        feed.diagnostic("auction_exit", "aborted", Map.of("candidate_state", phase.name(), "route", "ORDER_TO_AUCTION", "reason_code", "workflow_abort"));
        enabled = false;
        phase = Phase.ABORTED;
        message = safeReason;
        assembler = null;
        closeScreen(client);
        String recovery = placedShulker != null
                ? " Check the shulker or dropped package around " + placedShulker.getX() + ", " + placedShulker.getY() + ", " + placedShulker.getZ() + " manually."
                : "";
        tell(client, "Automatic exit stopped safely: " + safeReason + recovery);
    }

    private void clearCurrent() {
        position = null; plan = null; listing = null; assembler = null; placedShulker = null; placementHit = null;
        breakingStarted = false; confirmationSent = false; selectedSupplySlot = -1; selectedSupplyPrice = 0;
        selectedHotbarBefore = -1; loadedInOpenShulker = 0; desiredClaimCumulative = 0;
        desiredPackageCumulative = 0; desiredListingCumulative = 0; shulkerSpendThisWorkflow = 0;
        filledShulkerCountBefore = 0;
    }

    private String serverError(MinecraftClient client) {
        if (client == null || client.getCurrentServerEntry() == null) return "not connected to an approved DonutSMP server";
        String address = client.getCurrentServerEntry().address.strip().toLowerCase(Locale.ROOT);
        String host = address;
        if (host.startsWith("[")) {
            int closing = host.indexOf(']');
            host = closing > 0 ? host.substring(1, closing) : host;
        } else {
            int colon = host.lastIndexOf(':');
            if (colon > 0 && host.indexOf(':') == colon) host = host.substring(0, colon);
        }
        return feed.orderServerHosts().contains(host) ? "" : "connected server is not in order_server_hosts";
    }

    private int remainingShulkersNeeded(MinecraftClient client) {
        if (position.packagedQuantity() >= desiredPackageCumulative) return 0;
        return countEmptyShulkers(client.player.getInventory()) > 0 ? 0 : 1;
    }

    private long conservativeProfitAfterUndercuts() {
        long proceeds = Math.multiplyExact(position.expectedProceedsPerBatch(), position.batches());
        long undercuts = Math.multiplyExact(plan.undercutDollars(), plan.listings().size());
        return Math.subtractExact(Math.subtractExact(proceeds, position.escrowDollars()), undercuts);
    }

    private static GenericContainerScreenHandler genericHandler(MinecraftClient client) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) throw new IllegalStateException("not a generic container");
        return handler;
    }

    private static void clickSlot(MinecraftClient client, int slot, int button, SlotActionType type) {
        client.interactionManager.clickSlot(client.player.currentScreenHandler.syncId, slot, button, type, client.player);
    }

    private static int moveInventoryItemToHotbar(MinecraftClient client, int inventoryIndex) {
        PlayerInventory inventory = client.player.getInventory();
        if (inventoryIndex >= 0 && inventoryIndex < 9) return inventoryIndex;
        int hotbar = -1;
        for (int index = 0; index < 9; index++) if (inventory.getStack(index).isEmpty()) { hotbar = index; break; }
        if (hotbar < 0) hotbar = inventory.getSelectedSlot();
        OptionalInt source = client.player.currentScreenHandler.getSlotIndex(inventory, inventoryIndex);
        if (source.isEmpty()) throw new IllegalStateException("inventory source slot is unavailable");
        clickSlot(client, source.getAsInt(), hotbar, SlotActionType.SWAP);
        return hotbar;
    }

    private static int freeInventorySlots(PlayerInventory inventory) {
        int free = 0;
        for (ItemStack stack : inventory.getMainStacks()) if (stack.isEmpty()) free++;
        return free;
    }

    private static int countPlainItem(PlayerInventory inventory, String itemId) {
        int count = 0;
        for (ItemStack stack : inventory.getMainStacks()) if (plainItemMatches(stack, itemId)) count = Math.addExact(count, stack.getCount());
        return count;
    }

    private static int findExactPlainItem(PlayerInventory inventory, String itemId, int quantity) {
        for (int index = 0; index < inventory.getMainStacks().size(); index++) {
            ItemStack stack = inventory.getStack(index);
            if (plainItemMatches(stack, itemId) && stack.getCount() == quantity) return index;
        }
        return -1;
    }

    private static boolean itemMatches(ItemStack stack, String itemId) {
        if (stack == null || stack.isEmpty()) return false;
        Identifier id = Registries.ITEM.getId(stack.getItem());
        return id != null && id.toString().equals(itemId);
    }

    private static boolean plainItemMatches(ItemStack stack, String itemId) {
        return itemMatches(stack, itemId) && stack.getComponentChanges().isEmpty();
    }

    private static int countEmptyShulkers(PlayerInventory inventory) {
        int count = 0;
        for (ItemStack stack : inventory.getMainStacks()) if (isEmptyShulker(stack)) count += stack.getCount();
        return count;
    }

    private static int findEmptyShulker(PlayerInventory inventory) {
        for (int index = 0; index < inventory.getMainStacks().size(); index++) if (isEmptyShulker(inventory.getStack(index))) return index;
        return -1;
    }

    private static boolean isEmptyShulker(ItemStack stack) {
        if (stack == null || stack.isEmpty() || stack.getCount() != 1
                || !Registries.ITEM.getId(stack.getItem()).toString().equals("minecraft:shulker_box")
                || stack.get(DataComponentTypes.CUSTOM_NAME) != null
                || stack.get(DataComponentTypes.CUSTOM_DATA) != null) return false;
        LoreComponent lore = stack.get(DataComponentTypes.LORE);
        if (lore != null && !lore.lines().isEmpty()) return false;
        ContainerComponent contents = stack.get(DataComponentTypes.CONTAINER);
        return contents == null || contents.streamNonEmpty().findAny().isEmpty();
    }

    private static int findFilledShulker(PlayerInventory inventory, String itemId, int quantity) {
        for (int index = 0; index < inventory.getMainStacks().size(); index++) {
            if (filledShulkerMatches(inventory.getStack(index), itemId, quantity)) return index;
        }
        return -1;
    }

    private static int countFilledShulkers(PlayerInventory inventory, String itemId, int quantity) {
        int count = 0;
        for (ItemStack stack : inventory.getMainStacks()) if (filledShulkerMatches(stack, itemId, quantity)) count += stack.getCount();
        return count;
    }

    private static boolean filledShulkerMatches(ItemStack stack, String itemId, int quantity) {
        if (stack == null || stack.isEmpty() || !stack.isIn(ItemTags.SHULKER_BOXES) || stack.getCount() != 1) return false;
        ContainerComponent contents = stack.get(DataComponentTypes.CONTAINER);
        if (contents == null) return false;
        int total = 0;
        for (ItemStack inside : contents.iterateNonEmpty()) {
            if (!plainItemMatches(inside, itemId)) return false;
            total = Math.addExact(total, inside.getCount());
        }
        return total == quantity;
    }

    private static int countOpenShulkerItem(MinecraftClient client, String itemId) {
        if (!(client.player.currentScreenHandler instanceof ShulkerBoxScreenHandler handler)) return 0;
        int total = 0;
        for (int slot = 0; slot < 27; slot++) {
            ItemStack stack = handler.getSlot(slot).getStack();
            if (plainItemMatches(stack, itemId)) total = Math.addExact(total, stack.getCount());
        }
        return total;
    }

    private static int countOpenShulkerOtherItems(MinecraftClient client, String itemId) {
        if (!(client.player.currentScreenHandler instanceof ShulkerBoxScreenHandler handler)) return 1;
        int count = 0;
        for (int slot = 0; slot < 27; slot++) {
            ItemStack stack = handler.getSlot(slot).getStack();
            if (!stack.isEmpty() && !plainItemMatches(stack, itemId)) count++;
        }
        return count;
    }

    private static int countMenuPlainItem(GenericContainerScreenHandler handler, String itemId) {
        int count = 0;
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int slot = 0; slot < limit; slot++) {
            ItemStack stack = handler.getSlot(slot).getStack();
            if (plainItemMatches(stack, itemId)) count = Math.addExact(count, stack.getCount());
        }
        return count;
    }

    private static Optional<Long> safeEmptyShulkerAuctionRow(ItemStack stack) {
        if (stack == null || stack.isEmpty() || stack.getCount() != 1
                || !Registries.ITEM.getId(stack.getItem()).toString().equals("minecraft:shulker_box")
                || !label(stack).equals("shulker box") || !normalizedText(stack).contains("right-click to preview")
                || !emptyContainer(stack)) return Optional.empty();
        return firstMoney(stack);
    }

    private static boolean emptyContainer(ItemStack stack) {
        ContainerComponent contents = stack.get(DataComponentTypes.CONTAINER);
        return contents == null || contents.streamNonEmpty().findAny().isEmpty();
    }

    private static boolean vanillaSignature(LocalOrderPosition position) {
        String signature = position.signature();
        if (signature.equals(position.itemId())) return true;
        Identifier id = Identifier.tryParse(position.itemId());
        if (id == null || !Registries.ITEM.containsId(id)) return false;
        Item item = Registries.ITEM.get(id);
        String vanillaName = canonicalName(item.getName().getString());
        return signature.equals(position.itemId() + "|name=" + vanillaName);
    }

    private static String canonicalName(String value) {
        return OrderPlan.normalizeLabel(value).replace(' ', '_');
    }

    private static boolean textContainsRequested(String value, int quantity) {
        String compact = value == null ? "" : value.replace(",", "");
        return Pattern.compile("(?i)(?:^|\\D)" + quantity + "\\s+requested(?:\\D|$)").matcher(compact).find();
    }

    private static Optional<Long> firstMoney(ItemStack stack) {
        String text = stackText(stack);
        Matcher matcher = Pattern.compile("(?i)\\$\\s*([0-9][0-9,]*(?:\\.[0-9]+)?)([KMBT]?)").matcher(text);
        if (!matcher.find()) return Optional.empty();
        try {
            java.math.BigDecimal amount = new java.math.BigDecimal(matcher.group(1).replace(",", ""));
            long multiplier = switch (matcher.group(2).toUpperCase(Locale.ROOT)) {
                case "K" -> 1_000L; case "M" -> 1_000_000L; case "B" -> 1_000_000_000L; case "T" -> 1_000_000_000_000L; default -> 1L;
            };
            return Optional.of(amount.multiply(java.math.BigDecimal.valueOf(multiplier)).setScale(0, java.math.RoundingMode.DOWN).longValueExact());
        } catch (ArithmeticException | NumberFormatException error) { return Optional.empty(); }
    }

    private static boolean moneyTextMatches(String text, long dollars) {
        try {
            return OrderPlan.textContainsMoney(text, Math.multiplyExact(dollars, 100));
        } catch (ArithmeticException ignored) {
            return false;
        }
    }

    private static String stackText(ItemStack stack) {
        StringBuilder value = new StringBuilder(stack.getName().getString());
        LoreComponent lore = stack.get(DataComponentTypes.LORE);
        if (lore != null) for (Text line : lore.lines()) value.append(' ').append(line.getString());
        return value.toString();
    }

    private static String normalizedText(ItemStack stack) { return OrderPlan.normalizeLabel(stackText(stack)); }
    private static String label(ItemStack stack) { return OrderPlan.normalizeLabel(stack == null ? "" : stack.getName().getString()); }
    private static String title(Screen screen) { return screen == null ? "" : screen.getTitle().getString().replace("§", "").strip(); }

    private static String dialogText(Dialog dialog) {
        StringBuilder value = new StringBuilder(dialog.common().title().getString());
        for (DialogBody body : dialog.common().body()) {
            if (body instanceof PlainMessageDialogBody plain) value.append(' ').append(plain.contents().getString());
            if (body instanceof ItemDialogBody item) {
                value.append(' ').append(item.item().getName().getString());
                item.description().ifPresent(description -> value.append(' ').append(description.contents().getString()));
            }
        }
        return value.toString();
    }

    private static ButtonWidget requireButton(Screen screen, Set<String> labels) {
        Set<String> normalized = labels.stream().map(OrderPlan::normalizeLabel).collect(java.util.stream.Collectors.toUnmodifiableSet());
        List<ButtonWidget> matches = descendants(screen).stream().filter(ButtonWidget.class::isInstance).map(ButtonWidget.class::cast)
                .filter(button -> button.active && button.visible && normalized.contains(OrderPlan.normalizeLabel(button.getMessage().getString()))).toList();
        if (matches.size() != 1) throw new IllegalStateException("required confirmation button is missing or ambiguous");
        return matches.getFirst();
    }

    private static List<Element> descendants(ParentElement root) {
        List<Element> result = new ArrayList<>();
        Set<Element> visited = Collections.newSetFromMap(new IdentityHashMap<>());
        collectDescendants(root, result, visited, 0);
        return result;
    }

    private static void collectDescendants(ParentElement parent, List<Element> result, Set<Element> visited, int depth) {
        if (depth >= 12) return;
        for (Element child : parent.children()) {
            if (!visited.add(child)) continue;
            result.add(child);
            if (child instanceof ParentElement nested) collectDescendants(nested, result, visited, depth + 1);
        }
    }

    private static long multiplyDivideCeil(long value, long multiplier, long divisor) {
        long product = Math.multiplyExact(value, multiplier);
        return Math.addExact(product, divisor - 1) / divisor;
    }

    private static int ceilingDivide(int value, int divisor) { return (value + divisor - 1) / divisor; }
    private static String safe(String value) {
        String result = value == null ? "unknown failure" : value.replace('\r', ' ').replace('\n', ' ').strip();
        return result.substring(0, Math.min(200, result.length()));
    }
    private static void closeScreen(MinecraftClient client) {
        if (client.currentScreen == null) return;
        if (client.currentScreen instanceof HandledScreen<?> && client.player != null) client.player.closeHandledScreen();
        else client.setScreen(null);
    }
    private static void tell(MinecraftClient client, String value) {
        if (client.player != null) client.player.sendMessage(Text.literal("[DN] " + value), false);
    }

    /** Builds one exact direct-auction stack without touching unrelated items. */
    private static final class StackAssembler {
        private final String itemId;
        private final int targetQuantity;
        private int targetInventory = -1;
        private int sourceInventory = -1;
        private int depositing;
        private boolean returning;

        private StackAssembler(String itemId, int targetQuantity) { this.itemId = itemId; this.targetQuantity = targetQuantity; }

        boolean tick(MinecraftClient client) {
            PlayerInventory inventory = client.player.getInventory();
            ScreenHandler handler = client.player.currentScreenHandler;
            if (client.currentScreen != null || !(handler == client.player.playerScreenHandler)) throw new IllegalStateException("stack assembly requires the closed player inventory");
            if (targetInventory < 0) {
                for (int index = 0; index < 9; index++) if (inventory.getStack(index).isEmpty()) { targetInventory = index; break; }
                if (targetInventory < 0) throw new IllegalStateException("an empty hotbar slot is required to assemble an exact listing stack");
                client.player.getInventory().setSelectedSlot(targetInventory);
            }
            ItemStack target = inventory.getStack(targetInventory);
            if (!target.isEmpty() && !plainItemMatches(target, itemId)) throw new IllegalStateException("listing assembly target changed");
            if (target.getCount() == targetQuantity && handler.getCursorStack().isEmpty()) return true;
            if (target.getCount() > targetQuantity) throw new IllegalStateException("listing assembly exceeded exact quantity");
            if (returning) {
                if (handler.getCursorStack().isEmpty()) { returning = false; sourceInventory = -1; return false; }
                OptionalInt sourceSlot = handler.getSlotIndex(inventory, sourceInventory);
                if (sourceSlot.isEmpty()) throw new IllegalStateException("assembly source slot disappeared");
                clickSlot(client, sourceSlot.getAsInt(), 0, SlotActionType.PICKUP);
                returning = false; sourceInventory = -1;
                return false;
            }
            if (!handler.getCursorStack().isEmpty()) {
                OptionalInt targetSlot = handler.getSlotIndex(inventory, targetInventory);
                if (targetSlot.isEmpty()) throw new IllegalStateException("assembly target slot disappeared");
                clickSlot(client, targetSlot.getAsInt(), 1, SlotActionType.PICKUP);
                depositing--;
                if (depositing <= 0) returning = true;
                return false;
            }
            int needed = targetQuantity - target.getCount();
            sourceInventory = -1;
            for (int index = 0; index < inventory.getMainStacks().size(); index++) {
                if (index == targetInventory) continue;
                ItemStack stack = inventory.getStack(index);
                if (plainItemMatches(stack, itemId)) { sourceInventory = index; break; }
            }
            if (sourceInventory < 0) throw new IllegalStateException("not enough claimed items remain for exact listing");
            ItemStack source = inventory.getStack(sourceInventory);
            depositing = Math.min(needed, source.getCount());
            OptionalInt sourceSlot = handler.getSlotIndex(inventory, sourceInventory);
            if (sourceSlot.isEmpty()) throw new IllegalStateException("assembly source slot is unavailable");
            clickSlot(client, sourceSlot.getAsInt(), 0, SlotActionType.PICKUP);
            return false;
        }
    }
}
