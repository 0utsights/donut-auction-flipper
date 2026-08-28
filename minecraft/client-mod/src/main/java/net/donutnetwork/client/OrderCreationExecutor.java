package net.donutnetwork.client;

import net.donutnetwork.client.mixin.DialogScreenAccessor;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.screen.dialog.DialogScreen;
import net.minecraft.client.gui.screen.ingame.GenericContainerScreen;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.client.gui.widget.TextFieldWidget;
import net.minecraft.client.input.MouseInput;
import net.minecraft.dialog.body.DialogBody;
import net.minecraft.dialog.body.ItemDialogBody;
import net.minecraft.dialog.body.PlainMessageDialogBody;
import net.minecraft.dialog.input.TextInputControl;
import net.minecraft.dialog.type.Dialog;
import net.minecraft.dialog.type.DialogInput;
import net.minecraft.item.ItemStack;
import net.minecraft.registry.Registries;
import net.minecraft.screen.GenericContainerScreenHandler;
import net.minecraft.screen.slot.SlotActionType;
import net.minecraft.text.Text;
import net.minecraft.util.Identifier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.ArrayDeque;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.LinkedHashMap;
import java.util.Optional;
import java.util.Set;
import java.util.regex.Pattern;

/** Executes verified manual or explicitly enabled queued orders through Donut's server-driven 1.21.11 screens. */
final class OrderCreationExecutor {
    enum Phase { IDLE, WAIT_FRESH, ORDER_BOARD, YOUR_ORDERS, ITEM_SEARCH, ITEM_RESULT, AMOUNT, PRICE, REVIEW, PENDING_VERIFICATION, VERIFY_BOARD, VERIFY_YOUR_ORDERS, ABORTED }
    record Status(Phase phase, String message, long sessionSpent, String candidateId) {
        boolean active() { return phase != Phase.IDLE && phase != Phase.ABORTED; }
    }
    record ArmResult(boolean armed, String message) {}

    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final Duration FEED_MAX_AGE = Duration.ofSeconds(3);
    private static final Duration ORDER_MAX_AGE = Duration.ofSeconds(6);
    private static final Duration AUCTION_MAX_AGE = Duration.ofSeconds(15);
    private static final Duration FRESH_WAIT = Duration.ofSeconds(20);
    private static final Duration WORKFLOW_TIMEOUT = Duration.ofSeconds(45);
    private static final Duration SCREEN_TIMEOUT = Duration.ofSeconds(8);
    private static final String FOCUSED_STALE = "focused order observation is not fresh yet";
    private static final long ACTION_DELAY_NANOS = Duration.ofMillis(350).toNanos();
    private static final Pattern ORDERS_TITLE = Pattern.compile("Orders \\(Page [0-9]+\\)");
    private static final Pattern DUPLICATE_ORDER = Pattern.compile("(?i)\\b(?:you already have (?:an? )?(?:active )?order|"
            + "an? order (?:for|of) .{1,80} already exists|(?:cannot|can't) create (?:another|a duplicate) order|"
            + "only (?:have|create) one order per item|duplicate order (?:is )?(?:not allowed|blocked))\\b");
    private static final Set<String> SEARCH_ACTIONS = Set.of("search");
    private static final Set<String> AMOUNT_ACTIONS = Set.of("next", "continue", "set amount");
    private static final Set<String> PRICE_ACTIONS = Set.of("review order");
    private static final Set<String> CREATE_PANE_ITEMS = Set.of(
            "minecraft:black_stained_glass_pane", "minecraft:gray_stained_glass_pane",
            "minecraft:green_stained_glass_pane", "minecraft:lime_stained_glass_pane");

    private final CandidateFeedClient feed;
    private OrderPlan plan;
    private Phase phase = Phase.IDLE;
    private String message = "idle";
    private Instant armedAt = Instant.EPOCH;
    private Instant phaseAt = Instant.EPOCH;
    private long nextActionAt;
    private long sessionSpent;
    private OrderPlan lastSubmittedPlan;
    private boolean autoEnabled;
    private Instant nextAutoAttempt = Instant.EPOCH;
    private final ArrayDeque<String> autoQueue = new ArrayDeque<>();
    private final Map<String, Long> autoEscrowCaps = new LinkedHashMap<>();

    OrderCreationExecutor(CandidateFeedClient feed) {
        this.feed = feed;
    }

    Status status() {
        OrderPlan current = plan != null ? plan : lastSubmittedPlan;
        return new Status(phase, message, sessionSpent, current == null ? "" : current.candidateId());
    }
    boolean autoEnabled() { return autoEnabled; }
    int autoRemaining() { return autoQueue.size(); }

    ArmResult autoReadiness(MinecraftClient client) {
        if (status().active()) return new ArmResult(false, "an order workflow is already active");
        String error = serverError(client);
        if (!error.isEmpty()) return new ArmResult(false, error);
        if (!feed.balanceUsableForOrders()) return new ArmResult(false, "waiting for the live scoreboard balance or a manual override");
        if (feed.allocation().availableOrderSlots() < 1) return new ArmResult(false, "all 20 local order slots are currently marked as used");
        List<PortfolioAllocator.Selection> selections = feed.allocation().selections();
        if (selections.isEmpty()) return new ArmResult(false, "the local portfolio has no eligible orders");
        ArmResult first = canArm(selections.getFirst(), Instant.now());
        if (!first.armed()) return first;
        return new ArmResult(true, selections.size() + " reviewed orders are ready for session consent");
    }

    ArmResult enableAuto(MinecraftClient client) {
        ArmResult readiness = autoReadiness(client);
        if (!readiness.armed()) return readiness;
        List<PortfolioAllocator.Selection> selections = feed.allocation().selections();
        if (phase == Phase.ABORTED) phase = Phase.IDLE;
        autoQueue.clear();
        autoEscrowCaps.clear();
        selections.stream().limit(feed.allocation().availableOrderSlots()).forEach(selection -> {
            autoQueue.addLast(selection.candidate().id());
            autoEscrowCaps.put(selection.candidate().id(), selection.capital());
        });
        if (autoQueue.isEmpty()) return new ArmResult(false, "no free local order slots are available");
        autoEnabled = true;
        message = "automatic order queue enabled for " + autoQueue.size() + " reviewed candidates";
        nextAutoAttempt = Instant.EPOCH;
        feed.diagnostic("order_workflow", "auto_enabled", Map.of("candidate_state", "queue", "route", "ORDER_TO_AUCTION", "reason_code", "explicit_auto_consent"));
        return new ArmResult(true, message);
    }

    void disableAuto(MinecraftClient client, String reason) {
        autoEnabled = false;
        autoQueue.clear();
        autoEscrowCaps.clear();
        if (status().active()) abort(client, reason == null || reason.isBlank() ? "automatic order queue stopped by player" : reason);
        else message = reason == null || reason.isBlank() ? "automatic order queue disabled" : reason;
    }

    ArmResult canArm(PortfolioAllocator.Selection selection, Instant now) {
        if (status().active()) return new ArmResult(false, "another order workflow is active");
        String serverError = serverError(MinecraftClient.getInstance());
        if (!serverError.isEmpty()) return new ArmResult(false, serverError);
        OrderPlan candidatePlan;
        try { candidatePlan = OrderPlan.from(selection); }
        catch (IllegalArgumentException | ArithmeticException error) { return new ArmResult(false, error.getMessage()); }
        String liveError = liveError(candidatePlan, now, true);
        if (!liveError.isEmpty() && !liveError.equals(FOCUSED_STALE)) return new ArmResult(false, liveError);
        if (feed.hasActiveOrder(candidatePlan.itemId())) {
            return new ArmResult(false, "an order for this item is already active or pending verification");
        }
        return new ArmResult(true, liveError.isEmpty() ? "ready to arm" : "will wait for a focused refresh");
    }

    ArmResult arm(PortfolioAllocator.Selection selection) {
        Instant now = Instant.now();
        ArmResult result = canArm(selection, now);
        if (!result.armed()) return result;
        plan = OrderPlan.from(selection);
        armedAt = now;
        transition(Phase.WAIT_FRESH, "waiting for a current focused order sample");
        feed.focus(selection.candidate());
        feed.diagnostic("order_workflow", "armed", Map.of("candidate_state", selection.candidate().state(), "route", selection.candidate().route(), "reason_code", "explicit_local_arm"));
        return new ArmResult(true, "armed one order; waiting for live revalidation");
    }

    void observeServerMessage(MinecraftClient client, String raw) {
        if (phase == Phase.IDLE || phase == Phase.ABORTED) return;
        String text = raw == null ? "" : raw.replaceAll("§[0-9A-FK-ORa-fk-or]", "")
                .replace('\r', ' ').replace('\n', ' ').strip();
        if (!isDuplicateOrderMessage(text)) return;
        OrderPlan affected = plan != null ? plan : lastSubmittedPlan;
        if (affected != null) {
            feed.markActiveOrder(affected.itemId());
        }
        phase = Phase.ABORTED;
        message = "server reported a duplicate order; item was locked until Your Orders is reviewed";
        plan = null;
        autoEnabled = false;
        autoQueue.clear();
        autoEscrowCaps.clear();
        feed.diagnostic("order_workflow", "duplicate_reported", Map.of("candidate_state", "unknown", "route", "ORDER_TO_AUCTION", "reason_code", "server_duplicate_message"));
        if (client.currentScreen != null) client.setScreen(null);
        tell(client, "Duplicate-order response detected. This item is blocked locally; open Your Orders before retrying it.");
    }

    static boolean isDuplicateOrderMessage(String text) {
        return text != null && text.toLowerCase(Locale.ROOT).contains("order") && DUPLICATE_ORDER.matcher(text).find();
    }

    void cancel(MinecraftClient client, String reason) { abort(client, reason == null || reason.isBlank() ? "cancelled by player" : reason); }

    void tick(MinecraftClient client) {
        if (phase == Phase.IDLE) {
            maybeStartAuto(client);
            return;
        }
        if (phase == Phase.ABORTED) return;
        OrderPlan currentPlan = plan != null ? plan : lastSubmittedPlan;
        if (currentPlan == null) { abort(client, "order workflow lost its checked plan"); return; }
        if (client.player == null || client.getNetworkHandler() == null) { abort(client, "disconnected during the armed order workflow"); return; }
        Instant now = Instant.now();
        String serverError = serverError(client);
        if (!serverError.isEmpty()) { abort(client, serverError); return; }
        if (Duration.between(armedAt, now).compareTo(WORKFLOW_TIMEOUT) > 0) { abort(client, "order workflow timed out"); return; }
        if (phase == Phase.WAIT_FRESH) {
            String error = liveError(plan, now, true);
            if (autoEnabled && isRebasablePreTransactionChange(error) && tryRebaseCurrentAuto(now)) {
                error = liveError(plan, now, true);
            }
            if (error.isEmpty()) {
                client.getNetworkHandler().sendChatCommand("orders");
                transition(Phase.ORDER_BOARD, "opening verified order board");
                delay();
            } else if (Duration.between(phaseAt, now).compareTo(FRESH_WAIT) > 0) {
                if (autoEnabled && (error.equals(FOCUSED_STALE) || isSkippablePreTransactionChange(error))) {
                    skipCurrentAuto(client, error);
                }
                else abort(client, error);
            }
            return;
        }
        if (phase == Phase.PENDING_VERIFICATION) {
            if (System.nanoTime() < nextActionAt) return;
            client.getNetworkHandler().sendChatCommand("orders");
            transition(Phase.VERIFY_BOARD, "verifying the submitted order in Your Orders");
            delay();
            return;
        }
        boolean verifying = phase == Phase.VERIFY_BOARD || phase == Phase.VERIFY_YOUR_ORDERS;
        if (!verifying) {
            String error = liveError(currentPlan, now, true);
            if (!error.isEmpty()) { abort(client, error); return; }
        }
        if (System.nanoTime() < nextActionAt) return;
        if (Duration.between(phaseAt, now).compareTo(SCREEN_TIMEOUT) > 0) { abort(client, "server screen did not advance before its deadline"); return; }
        Screen screen = client.currentScreen;
        try {
            switch (phase) {
                case ORDER_BOARD -> handleOrderBoard(client, screen);
                case YOUR_ORDERS -> handleYourOrders(client, screen);
                case ITEM_SEARCH -> handleItemSearch(client, screen);
                case ITEM_RESULT -> handleItemResult(client, screen);
                case AMOUNT -> handleAmount(client, screen);
                case PRICE -> handlePrice(client, screen);
                case REVIEW -> handleReview(client, screen);
                case VERIFY_BOARD -> handleVerifyBoard(client, screen);
                case VERIFY_YOUR_ORDERS -> handleVerifyYourOrders(client, screen);
                default -> { }
            }
        } catch (RuntimeException errorValue) {
            abort(client, "screen verification failed: " + safe(errorValue.getMessage()));
        }
    }

    private void maybeStartAuto(MinecraftClient client) {
        if (!autoEnabled || Instant.now().isBefore(nextAutoAttempt) || client.currentScreen != null) return;
        if (autoQueue.isEmpty()) {
            autoEnabled = false;
            message = "automatic order queue completed";
            tell(client, message);
            return;
        }
        String candidateID = autoQueue.getFirst();
        Optional<PortfolioAllocator.Selection> current = feed.allocation().selections().stream()
                .filter(selection -> selection.candidate().id().equals(candidateID)
                        && !feed.hasActiveOrder(selection.candidate().itemId())).findFirst();
        if (current.isEmpty()) { skipCurrentAuto(client, "reviewed allocation changed or disappeared"); return; }
        long cap = autoEscrowCaps.getOrDefault(candidateID, 0L);
        int authorizedBatches = authorizedBatches(current.get().batches(), current.get().candidate().acquisitionCost(), cap);
        if (authorizedBatches <= 0) { skipCurrentAuto(client, "reviewed escrow no longer covers this item"); return; }
        PortfolioAllocator.Selection next = new PortfolioAllocator.Selection(current.get().candidate(), authorizedBatches);
        ArmResult result = arm(next);
        if (!result.armed()) skipCurrentAuto(client, result.message());
    }

    static int authorizedBatches(int currentBatches, long acquisitionCost, long escrowCap) {
        if (currentBatches <= 0 || acquisitionCost <= 0 || escrowCap <= 0) return 0;
        return (int) Math.min(currentBatches, escrowCap / acquisitionCost);
    }

    /**
     * A focused watch can legitimately replace a candidate's economics while
     * this workflow is waiting. Automatic consent authorizes the market and a
     * maximum escrow, not stale price inputs, so adopt only the current READY
     * allocation for the exact same canonical item and never exceed that cap.
     */
    private boolean tryRebaseCurrentAuto(Instant now) {
        if (!autoEnabled || plan == null || autoQueue.isEmpty()) return false;
        String queuedID = autoQueue.getFirst();
        long escrowCap = autoEscrowCaps.getOrDefault(queuedID, 0L);
        Optional<PortfolioAllocator.Selection> currentValue = feed.allocation().selections().stream()
                .filter(selection -> selection.candidate().itemId().equals(plan.itemId())
                        && selection.candidate().signature().equals(plan.signature())
                        && !feed.hasActiveOrder(selection.candidate().itemId()))
                .findFirst();
        if (currentValue.isEmpty()) return false;
        PortfolioAllocator.Selection current = currentValue.get();
        CandidateFeedClient.Candidate candidate = current.candidate();
        if (candidate.focusedFreshAt().isBefore(armedAt.minusSeconds(1))
                || age(candidate.focusedFreshAt(), now).compareTo(ORDER_MAX_AGE) > 0) return false;
        int batches = authorizedBatches(current.batches(), candidate.acquisitionCost(), escrowCap);
        if (batches <= 0) return false;

        OrderPlan refreshed;
        try {
            refreshed = OrderPlan.from(new PortfolioAllocator.Selection(candidate, batches));
        } catch (IllegalArgumentException | ArithmeticException error) {
            return false;
        }
        if (!liveError(refreshed, now, true).isEmpty()) return false;

        plan = refreshed;
        if (!queuedID.equals(refreshed.candidateId())) {
            autoQueue.removeFirst();
            autoEscrowCaps.remove(queuedID);
            autoQueue.remove(refreshed.candidateId());
            autoEscrowCaps.remove(refreshed.candidateId());
            autoQueue.addFirst(refreshed.candidateId());
            autoEscrowCaps.put(refreshed.candidateId(), escrowCap);
        }
        message = "focused market refresh accepted within the reviewed escrow cap";
        feed.diagnostic("order_workflow", "auto_rebased", Map.of("candidate_state", candidate.state(),
                "route", candidate.route(), "reason_code", "focused_refresh_within_escrow_cap"));
        return true;
    }

    private void handleOrderBoard(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (!(screen instanceof GenericContainerScreen) || !ORDERS_TITLE.matcher(title).matches()) {
            abort(client, "unexpected screen while opening orders: " + title); return;
        }
        ItemStack stack = containerSlot(client, 51);
        if (!labelEquals(stack.getName().getString(), "Your Orders")) { abort(client, "Your Orders control did not match slot 51"); return; }
        clickSlot(client, 51);
        transition(Phase.YOUR_ORDERS, "opening personal order slots"); delay();
    }

    private void handleYourOrders(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (ORDERS_TITLE.matcher(title).matches()) return;
        if (!(screen instanceof GenericContainerScreen) || !title.equals("Orders -> Your Orders")) {
            abort(client, "unexpected personal-orders screen: " + title); return;
        }
        if (personalOrdersContain(client, plan.itemId())) {
            feed.markActiveOrder(plan.itemId());
            abort(client, "an existing personal order for this item was found; duplicate creation was blocked");
            return;
        }
        int createSlot = findCreateOrderSlot(client);
        if (createSlot < 0) {
            LOGGER.warn("Unrecognized or full Your Orders menu: {}", menuFingerprint(client));
            abort(client, "no verified free order slot is available; local log contains the menu fingerprint"); return;
        }
        clickSlot(client, createSlot);
        transition(Phase.ITEM_SEARCH, "entering exact item search"); delay();
    }

    private void handleItemSearch(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (title.equals("Orders -> Your Orders")) return;
        Dialog dialog = requireDialog(screen, "Choose Item");
        if (!title.equals("Choose Item")) { abort(client, "unexpected item-search screen: " + title); return; }
        TextFieldWidget field = requireSingleTextField(screen, dialog, Set.of("search"));
        field.setText(plan.itemPathQuery());
        requireButton(screen, SEARCH_ACTIONS).onPress(new MouseInput(0, 0));
        transition(Phase.ITEM_RESULT, "waiting for one exact item result"); delay();
    }

    private void handleItemResult(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (title.equals("Choose Item")) return;
        requireDialog(screen, "Choose Item");
        if (!title.matches("Choose Item \\(1 results?\\)")) { abort(client, "item search was not uniquely resolved: " + title); return; }
        String registryName = expectedRegistryName();
        List<ButtonWidget> matches = buttons(screen).stream()
                .filter(button -> OrderPlan.equivalentItemLabel(button.getMessage().getString(), registryName, plan.itemName())).toList();
        if (matches.size() != 1) { abort(client, "exact item result was missing or ambiguous"); return; }
        matches.getFirst().onPress(new MouseInput(0, 0));
        transition(Phase.AMOUNT, "entering exact batch quantity"); delay();
    }

    private void handleAmount(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (title.startsWith("Choose Item")) return;
        Dialog dialog = requireDialog(screen, "How many?");
        TextFieldWidget field = requireSingleTextField(screen, dialog, Set.of("amount", "quantity", "how many"));
        field.setText(Integer.toString(plan.quantity()));
        if (!field.getText().equals(Integer.toString(plan.quantity()))) { abort(client, "amount field rejected the exact quantity"); return; }
        requireButton(screen, AMOUNT_ACTIONS).onPress(new MouseInput(0, 0));
        transition(Phase.PRICE, "entering exact unit reward"); delay();
    }

    private void handlePrice(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (title.equals("How many?")) return;
        Dialog dialog = requireDialog(screen, "Price per item?");
        TextFieldWidget field = requireSingleTextField(screen, dialog, Set.of("price", "reward"));
        field.setText(plan.priceInput());
        if (!field.getText().equals(plan.priceInput())) { abort(client, "price field rejected the exact cent value"); return; }
        requireButton(screen, PRICE_ACTIONS).onPress(new MouseInput(0, 0));
        transition(Phase.REVIEW, "performing final server-review validation"); delay();
    }

    private void handleReview(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (title.equals("Price per item?")) return;
        Dialog dialog = requireDialog(screen, "Review Order");
        String corpus = dialogText(dialog) + " " + buttons(screen).stream().map(button -> button.getMessage().getString()).reduce("", (a, b) -> a + " " + b);
        String registryName = expectedRegistryName();
        if (!reviewContainsExactItem(dialog, plan.itemId())) {
            abort(client, "review item does not match the armed item"); return;
        }
        if (!OrderPlan.textContainsQuantity(corpus, plan.quantity())) {
            abort(client, "review amount does not match the armed quantity"); return;
        }
        if (!OrderPlan.textContainsMoney(corpus, plan.unitRewardCents()) || !OrderPlan.textContainsMoney(corpus, plan.totalCents())) {
            abort(client, "review price or total does not match the armed economics"); return;
        }
        CandidateFeedClient.Candidate current = feed.candidate(plan.candidateId()).orElseThrow(() -> new IllegalStateException("candidate disappeared"));
        String liveError = liveError(plan, Instant.now(), true);
        if (!liveError.isEmpty()) { abort(client, liveError); return; }
        requireButton(screen, Set.of("create order")).onPress(new MouseInput(0, 0));
        sessionSpent = Math.addExact(sessionSpent, plan.escrowDollars());
        lastSubmittedPlan = plan;
        feed.recordOrderSubmitted(current, plan);
        transition(Phase.PENDING_VERIFICATION, "Create Order sent once; server outcome is pending local verification");
        delayVerification();
        tell(client, "Create Order sent once for " + plan.quantity() + "× " + registryName + " (" + plan.batches() + " resale stacks) at $" + plan.priceInput() + " each. Verifying it in Your Orders.");
        plan = null;
    }

    private void handleVerifyBoard(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (!(screen instanceof GenericContainerScreen) || !ORDERS_TITLE.matcher(title).matches()) {
            abort(client, "unexpected screen while verifying submitted order: " + title); return;
        }
        ItemStack stack = containerSlot(client, 51);
        if (!labelEquals(stack.getName().getString(), "Your Orders")) { abort(client, "Your Orders verification control did not match slot 51"); return; }
        clickSlot(client, 51);
        transition(Phase.VERIFY_YOUR_ORDERS, "checking that the submitted item exists exactly once");
        delay();
    }

    private void handleVerifyYourOrders(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (ORDERS_TITLE.matcher(title).matches()) return;
        if (!(screen instanceof GenericContainerScreen) || !title.equals("Orders -> Your Orders")) {
            abort(client, "unexpected personal-orders verification screen: " + title); return;
        }
        int matches = personalOrderCount(client, lastSubmittedPlan.itemId());
        if (matches == 1) {
            String itemName = lastSubmittedPlan.itemName();
            String candidateID = lastSubmittedPlan.candidateId();
            feed.markActiveOrder(lastSubmittedPlan.itemId());
            feed.diagnostic("order_workflow", "verified", Map.of("candidate_state", "active", "route", "ORDER_TO_AUCTION", "reason_code", "personal_order_exact_match"));
            phase = Phase.IDLE;
            message = "verified " + itemName + " in Your Orders";
            lastSubmittedPlan = null;
            if (autoEnabled && !autoQueue.isEmpty() && autoQueue.getFirst().equals(candidateID)) {
                autoQueue.removeFirst();
                autoEscrowCaps.remove(candidateID);
            }
            nextAutoAttempt = Instant.now().plusSeconds(2);
            if (client.currentScreen != null) client.setScreen(null);
            tell(client, "Order verified in Your Orders: " + itemName + (autoEnabled
                    ? ". " + autoQueue.size() + " reviewed orders remain." : "."));
            return;
        }
        if (matches > 1) { abort(client, "multiple personal orders matched the submitted item; automatic orders stopped"); return; }
        if (Duration.between(phaseAt, Instant.now()).compareTo(Duration.ofSeconds(1)) < 0) return;
        abort(client, "submitted order was not found in Your Orders; its item remains locked for manual review");
    }

    private String liveError(OrderPlan expected, Instant now, boolean requireAllocation) {
        if (!feed.balanceUsableForOrders()) return "waiting for the live scoreboard balance or a manual override";
        if (!"ready".equals(feed.status().state())) return "backend candidate feed is not ready";
        if (age(feed.generatedAt(), now).compareTo(FEED_MAX_AGE) > 0) return "candidate feed is stale";
        Optional<CandidateFeedClient.Candidate> currentValue = feed.candidate(expected.candidateId());
        if (currentValue.isEmpty() || !expected.matches(currentValue.get())) return "armed candidate changed or disappeared";
        CandidateFeedClient.Candidate current = currentValue.get();
        if (age(current.focusedFreshAt(), now).compareTo(ORDER_MAX_AGE) > 0) return FOCUSED_STALE;
        if (age(current.auctionFreshAt(), now).compareTo(AUCTION_MAX_AGE) > 0) return "auction exit is stale";
        if (requireAllocation && !feed.isAllocated(current.id())) return "candidate no longer belongs to the local portfolio";
        if (requireAllocation && feed.allocatedBatches(current.id()) < expected.batches()) return "allocated stack count was reduced";
        if (feed.hasActiveOrder(expected.itemId())) return "an order for this item is already active or pending";
        PortfolioAllocator.Allocation allocation = feed.allocation();
        if (allocation.availableOrderSlots() < 1) return "local order slots are exhausted";
        if (expected.escrowDollars() > allocation.deployable()) return "order exceeds deployable balance after reserve";
        return "";
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

    private static Duration age(Instant value, Instant now) {
        if (value == null || value.equals(Instant.EPOCH) || value.isAfter(now)) return Duration.ofDays(365_000);
        return Duration.between(value, now);
    }

    private void abort(MinecraftClient client, String reason) {
        if (phase == Phase.IDLE || phase == Phase.ABORTED) return;
        String safeReason = safe(reason);
        LOGGER.warn("Order workflow aborted in {}: {}; screen={}", phase, safeReason, describeScreen(client.currentScreen));
        feed.diagnostic("order_workflow", "aborted", Map.of("candidate_state", phase.name(), "route", "ORDER_TO_AUCTION", "reason_code", "workflow_abort"));
        phase = Phase.ABORTED; message = safeReason; plan = null; autoEnabled = false; autoQueue.clear(); autoEscrowCaps.clear();
        if (client.currentScreen != null) client.setScreen(null);
        tell(client, "Order creation stopped safely: " + safeReason);
    }

    private void skipCurrentAuto(MinecraftClient client, String reason) {
        String skipped = autoQueue.pollFirst();
        if (skipped != null) autoEscrowCaps.remove(skipped);
        plan = null;
        phase = Phase.IDLE;
        nextAutoAttempt = Instant.now().plusSeconds(1);
        if (autoQueue.isEmpty()) {
            autoEnabled = false;
            message = "automatic queue exhausted after skipping changed pre-transaction candidates";
        } else {
            message = "skipped one changed candidate before any server action: " + safe(reason)
                    + "; " + autoQueue.size() + " remain";
        }
        feed.diagnostic("order_workflow", "auto_skipped", Map.of("candidate_state", "pre_transaction",
                "route", "ORDER_TO_AUCTION", "reason_code", "candidate_changed_before_navigation"));
        tell(client, message);
    }

    static boolean isSkippablePreTransactionChange(String error) {
        return error != null && (error.equals("armed candidate changed or disappeared")
                || error.equals("candidate no longer belongs to the local portfolio")
                || error.equals("allocated stack count was reduced")
                || error.equals("an order for this item is already active or pending")
                || error.equals("order exceeds deployable balance after reserve")
                || error.equals("auction exit is stale"));
    }

    static boolean isRebasablePreTransactionChange(String error) {
        return error != null && (error.equals("armed candidate changed or disappeared")
                || error.equals("candidate no longer belongs to the local portfolio")
                || error.equals("allocated stack count was reduced")
                || error.equals("order exceeds deployable balance after reserve"));
    }

    private void transition(Phase next, String detail) { phase = next; phaseAt = Instant.now(); message = detail; }
    private void delay() { nextActionAt = System.nanoTime() + ACTION_DELAY_NANOS; }
    private void delayVerification() { nextActionAt = System.nanoTime() + Duration.ofSeconds(1).toNanos(); }

    private static ItemStack containerSlot(MinecraftClient client, int index) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) throw new IllegalStateException("not a generic container");
        if (index < 0 || index >= handler.slots.size()) throw new IllegalStateException("required slot is absent");
        ItemStack stack = handler.getSlot(index).getStack();
        if (stack.isEmpty()) throw new IllegalStateException("required slot is empty");
        return stack;
    }

    private static void clickSlot(MinecraftClient client, int slot) {
        if (client.interactionManager == null || client.player == null) throw new IllegalStateException("interaction manager is unavailable");
        client.interactionManager.clickSlot(client.player.currentScreenHandler.syncId, slot, 0, SlotActionType.PICKUP, client.player);
    }

    private static int findCreateOrderSlot(MinecraftClient client) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return -1;
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (stack.isEmpty()) continue;
            Identifier itemId = Registries.ITEM.getId(stack.getItem());
            String label = OrderPlan.normalizeLabel(stack.getName().getString());
            if (isCreateOrderControl(itemId.toString(), label)) return index;
        }
        return -1;
    }

    static boolean isCreateOrderControl(String itemId, String label) {
        if (!CREATE_PANE_ITEMS.contains(itemId)) return false;
        String normalized = OrderPlan.normalizeLabel(label);
        return normalized.contains("order") && (normalized.contains("create") || normalized.contains("new")
                || normalized.contains("empty") || normalized.contains("available"));
    }

    private static String menuFingerprint(MinecraftClient client) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return "not-a-container";
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        StringBuilder value = new StringBuilder("slots=").append(limit);
        for (int index = 0; index < limit && value.length() < 1800; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (stack.isEmpty()) continue;
            Identifier id = Registries.ITEM.getId(stack.getItem());
            value.append(" | ").append(index).append('=').append(id).append('[')
                    .append(safe(stack.getName().getString())).append(']');
        }
        return value.toString();
    }

    private static boolean personalOrdersContain(MinecraftClient client, String expectedItemId) {
        return personalOrderCount(client, expectedItemId) > 0;
    }

    private static int personalOrderCount(MinecraftClient client, String expectedItemId) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return 0;
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        int matches = 0;
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (stack.isEmpty()) continue;
            Identifier id = Registries.ITEM.getId(stack.getItem());
            if (id != null && id.toString().equals(expectedItemId)) matches++;
        }
        return matches;
    }

    private static Dialog requireDialog(Screen screen, String exactTitle) {
        if (!(screen instanceof DialogScreen<?> dialogScreen) || !(screen instanceof DialogScreenAccessor accessor)) {
            throw new IllegalStateException("expected a server dialog");
        }
        if (!title(dialogScreen).equals(exactTitle) && !title(dialogScreen).startsWith(exactTitle + " (")) {
            throw new IllegalStateException("unexpected dialog title " + title(dialogScreen));
        }
        return accessor.donut$getDialog();
    }

    private static TextFieldWidget requireSingleTextField(Screen screen, Dialog dialog, Set<String> expectedLabels) {
        List<TextFieldWidget> fields = screen.children().stream().filter(TextFieldWidget.class::isInstance).map(TextFieldWidget.class::cast).toList();
        if (fields.size() != 1) throw new IllegalStateException("expected exactly one text field");
        List<String> labels = new ArrayList<>();
        for (DialogInput input : dialog.common().inputs()) {
            labels.add(OrderPlan.normalizeLabel(input.key()));
            if (input.control() instanceof TextInputControl text) labels.add(OrderPlan.normalizeLabel(text.label().getString()));
        }
        boolean recognized = labels.stream().anyMatch(label -> expectedLabels.stream().anyMatch(label::contains));
        if (!recognized) throw new IllegalStateException("dialog input label is not recognized");
        return fields.getFirst();
    }

    private static ButtonWidget requireButton(Screen screen, Set<String> exactLabels) {
        List<ButtonWidget> matches = buttons(screen).stream().filter(button -> button.active && button.visible)
                .filter(button -> exactLabels.contains(OrderPlan.normalizeLabel(button.getMessage().getString()))).toList();
        if (matches.size() != 1) throw new IllegalStateException("required action button is missing or ambiguous");
        return matches.getFirst();
    }

    private static List<ButtonWidget> buttons(Screen screen) {
        return screen.children().stream().filter(ButtonWidget.class::isInstance).map(ButtonWidget.class::cast).toList();
    }

    private String expectedRegistryName() {
        Identifier id = Identifier.tryParse(plan.itemId());
        if (id == null || !Registries.ITEM.containsId(id)) throw new IllegalStateException("unknown Minecraft item id");
        return new ItemStack(Registries.ITEM.get(id)).getName().getString();
    }

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

    private static boolean reviewContainsExactItem(Dialog dialog, String expectedId) {
        for (DialogBody body : dialog.common().body()) {
            if (body instanceof ItemDialogBody item) {
                Identifier actual = Registries.ITEM.getId(item.item().getItem());
                if (actual != null && actual.toString().equals(expectedId)) return true;
            }
        }
        return false;
    }

    private static String describeScreen(Screen screen) {
        if (screen == null) return "none";
        StringBuilder value = new StringBuilder(screen.getClass().getSimpleName()).append(':').append(title(screen));
        for (ButtonWidget button : buttons(screen)) value.append("|button=").append(safe(button.getMessage().getString()));
        if (screen instanceof DialogScreenAccessor accessor) {
            for (DialogInput input : accessor.donut$getDialog().common().inputs()) value.append("|input=").append(safe(input.key()));
        }
        return safe(value.toString());
    }

    private static String title(Screen screen) { return screen.getTitle().getString().replace("§", "").strip(); }
    private static boolean labelEquals(String actual, String expected) { return OrderPlan.normalizeLabel(actual).equals(OrderPlan.normalizeLabel(expected)); }
    private static String safe(String value) {
        String result = value == null ? "unknown failure" : value.replace('\r', ' ').replace('\n', ' ').strip();
        return result.substring(0, Math.min(160, result.length()));
    }
    private static void tell(MinecraftClient client, String value) {
        if (client.player != null) client.player.sendMessage(Text.literal("[DN] " + value), false);
    }
}
