package net.donutnetwork.client;

import net.donutnetwork.client.mixin.DialogScreenAccessor;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.gui.Element;
import net.minecraft.client.gui.ParentElement;
import net.minecraft.client.gui.screen.Screen;
import net.minecraft.client.gui.screen.dialog.DialogScreen;
import net.minecraft.client.gui.screen.ingame.GenericContainerScreen;
import net.minecraft.client.gui.screen.ingame.HandledScreen;
import net.minecraft.client.gui.widget.ButtonWidget;
import net.minecraft.client.gui.widget.EditBoxWidget;
import net.minecraft.client.gui.widget.TextFieldWidget;
import net.minecraft.client.input.MouseInput;
import net.minecraft.component.DataComponentTypes;
import net.minecraft.component.type.LoreComponent;
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
import java.util.Collections;
import java.util.HashSet;
import java.util.IdentityHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.LinkedHashMap;
import java.util.Optional;
import java.util.OptionalLong;
import java.util.Set;
import java.util.regex.Pattern;

/** Executes verified manual or explicitly enabled queued orders through Donut's server-driven 1.21.11 screens. */
final class OrderCreationExecutor {
    enum Phase { IDLE, WAIT_FRESH, ORDER_BOARD, YOUR_ORDERS, ITEM_SEARCH, ITEM_RESULT, AMOUNT, PRICE, REVIEW,
        PENDING_VERIFICATION, VERIFY_BOARD, VERIFY_YOUR_ORDERS, RANK_BOARD, RANK_SEARCH,
        CANCEL_BOARD, CANCEL_YOUR_ORDERS, CANCEL_MANAGE, CANCEL_CONFIRM,
        CANCEL_VERIFY_BOARD, CANCEL_VERIFY_YOUR_ORDERS, ABORTED }
    record Status(Phase phase, String message, long sessionSpent, String candidateId) {
        boolean active() { return phase != Phase.IDLE && phase != Phase.ABORTED; }
    }
    record ArmResult(boolean armed, String message) {}
    private record PublicRank(int rank, int exactRows) {}
    private record DialogTextInput(TextFieldWidget singleLine, EditBoxWidget multiline) {
        static DialogTextInput of(Object widget) {
            if (widget instanceof TextFieldWidget field) return new DialogTextInput(field, null);
            if (widget instanceof EditBoxWidget field) return new DialogTextInput(null, field);
            return null;
        }
        void setText(String value) {
            if (singleLine != null) singleLine.setText(value);
            else multiline.setText(value);
        }
        String getText() { return singleLine != null ? singleLine.getText() : multiline.getText(); }
    }

    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final Duration FEED_MAX_AGE = Duration.ofSeconds(3);
    private static final Duration ORDER_MAX_AGE = Duration.ofSeconds(6);
    private static final Duration AUCTION_MAX_AGE = Duration.ofSeconds(15);
    private static final Duration FRESH_WAIT = Duration.ofSeconds(20);
    private static final Duration WORKFLOW_TIMEOUT = Duration.ofMinutes(3);
    private static final Duration SCREEN_TIMEOUT = Duration.ofSeconds(8);
    private static final String FOCUSED_STALE = "focused order observation is not fresh yet";
    private static final long ACTION_DELAY_NANOS = Duration.ofMillis(350).toNanos();
    private static final Pattern ORDERS_TITLE = Pattern.compile("Orders \\(Page [0-9]+\\)");
    private static final Pattern DUPLICATE_ORDER = Pattern.compile("(?i)\\b(?:you already have (?:an? )?(?:active )?order|"
            + "an? order (?:for|of) .{1,80} already exists|(?:cannot|can't) create (?:another|a duplicate) order|"
            + "only (?:have|create) one order per item|duplicate order (?:is )?(?:not allowed|blocked))\\b");
    private static final Set<String> ITEM_DIALOG_TITLES = Set.of("Choose Item", "Choisir un objet");
    private static final Set<String> AMOUNT_DIALOG_TITLES = Set.of("How many?", "Combien ?", "Combien?");
    private static final Set<String> PRICE_DIALOG_TITLES = Set.of(
            "Price per item?", "Prix par objet ?", "Prix par objet?", "Prix par article ?", "Prix unitaire ?");
    private static final Set<String> REVIEW_DIALOG_TITLES = Set.of(
            "Review Order", "Vérifier la commande", "Réviser la commande", "Récapitulatif de la commande");
    private static final Set<String> SEARCH_ACTIONS = Set.of("search", "rechercher");
    private static final Set<String> AMOUNT_ACTIONS = Set.of(
            "next", "continue", "set amount", "suivant", "continuer", "définir la quantité");
    private static final Set<String> PRICE_ACTIONS = Set.of(
            "review order", "vérifier la commande", "réviser la commande", "récapitulatif de la commande");
    private static final Set<String> CREATE_ACTIONS = Set.of("create order", "créer la commande", "créer une commande");
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
    private final Map<String, String> autoItemIds = new LinkedHashMap<>();
    private final Set<String> autoSessionRejectedItems = new LinkedHashSet<>();
    private long currentEscrowCap;
    private long minimumProfitDollars;
    private long pendingRepriceUnitCents;
    private int rankSortAttempts;
    private boolean retrying;
    private boolean cancelConfirmationSent;

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
        return new ArmResult(true, selections.isEmpty()
                ? "no reviewed orders now; the session will wait for future READY allocations"
                : selections.size() + " reviewed orders are ready now; later READY allocations will also be queued");
    }

    ArmResult enableAuto(MinecraftClient client) {
        ArmResult readiness = autoReadiness(client);
        if (!readiness.armed()) return readiness;
        if (phase == Phase.ABORTED) phase = Phase.IDLE;
        clearAutoQueueState();
        sessionSpent = 0;
        lastSubmittedPlan = null;
        autoEnabled = true;
        refreshAutoQueue();
        message = autoQueue.isEmpty()
                ? "automatic order session active; waiting for reviewed candidates"
                : "automatic order session active with " + autoQueue.size() + " reviewed candidates queued";
        nextAutoAttempt = Instant.EPOCH;
        feed.diagnostic("order_workflow", "auto_enabled", Map.of("candidate_state", autoQueue.isEmpty() ? "waiting" : "queue",
                "route", "ORDER_TO_AUCTION", "reason_code", "explicit_continuous_session_consent"));
        return new ArmResult(true, message);
    }

    void disableAuto(MinecraftClient client, String reason) {
        autoEnabled = false;
        clearAutoQueueState();
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
        currentEscrowCap = autoEnabled
                ? autoEscrowCaps.getOrDefault(selection.candidate().id(), selection.capital())
                : selection.capital();
        minimumProfitDollars = Math.max(1, selection.conservativeProfit());
        pendingRepriceUnitCents = 0;
        rankSortAttempts = 0;
        retrying = false;
        armedAt = now;
        transition(Phase.WAIT_FRESH, "waiting for a current focused order sample");
        feed.focus(selection.candidate());
        feed.diagnostic("order_workflow", "armed", Map.of("candidate_state", selection.candidate().state(), "route", selection.candidate().route(), "reason_code", "explicit_local_arm"));
        return new ArmResult(true, "armed one order; waiting for live revalidation");
    }

    void observeServerMessage(MinecraftClient client, String raw) {
        String text = raw == null ? "" : raw.replaceAll("§[0-9A-FK-ORa-fk-or]", "")
                .replace('\r', ' ').replace('\n', ' ').strip();
        OrderPlan affected = plan != null ? plan : lastSubmittedPlan;
        if (affected != null && CandidateFeedClient.isCompleteOrderMessage(text, affected.itemName())
                && feed.orderPosition(affected.itemId())
                .filter(position -> position.state() == LocalOrderPosition.State.CLAIM_READY).isPresent()) {
            finishCompletedOrder(client, affected);
            return;
        }
        if (phase == Phase.IDLE || phase == Phase.ABORTED) return;
        if (!isDuplicateOrderMessage(text)) return;
        if (affected != null) {
            feed.markActiveOrder(affected.itemId());
        }
        if (autoEnabled) {
            if (lastSubmittedPlan != null) {
                sessionSpent = Math.max(0, sessionSpent - lastSubmittedPlan.escrowDollars());
            }
            feed.diagnostic("order_workflow", "duplicate_reported", Map.of("candidate_state", phase.name(),
                    "route", "ORDER_TO_AUCTION", "reason_code", "server_duplicate_message_item_quarantined"));
            quarantineCurrentAuto(client, "server reported an existing order for this item");
            return;
        }
        phase = Phase.ABORTED;
        message = "server reported a duplicate order; item was locked until Your Orders is reviewed";
        plan = null;
        autoEnabled = false;
        clearAutoQueueState();
        feed.diagnostic("order_workflow", "duplicate_reported", Map.of("candidate_state", "unknown", "route", "ORDER_TO_AUCTION", "reason_code", "server_duplicate_message"));
        closeScreen(client);
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
        if (client.player == null || client.getNetworkHandler() == null) {
            if (autoEnabled) pauseAutoForConnection(client, "disconnected; automatic orders will resume after reconnect");
            else abort(client, "disconnected during the armed order workflow");
            return;
        }
        Instant now = Instant.now();
        String serverError = serverError(client);
        if (!serverError.isEmpty()) {
            if (autoEnabled) pauseAutoForConnection(client, serverError);
            else abort(client, serverError);
            return;
        }
        if (Duration.between(armedAt, now).compareTo(WORKFLOW_TIMEOUT) > 0) {
            failCandidate(client, "order workflow timed out");
            return;
        }
        if (phase == Phase.WAIT_FRESH) {
            String error = liveError(plan, now, true);
            if (autoEnabled && !retrying && isRebasablePreTransactionChange(error) && tryRebaseCurrentAuto(now)) {
                error = liveError(plan, now, true);
            }
            if (error.isEmpty()) {
                client.getNetworkHandler().sendChatCommand("orders");
                transition(Phase.ORDER_BOARD, "opening verified order board");
                delay();
            } else if (Duration.between(phaseAt, now).compareTo(FRESH_WAIT) > 0) {
                if (autoEnabled && isTransientWaitError(error)) {
                    feed.candidate(plan.candidateId()).ifPresent(feed::focus);
                    phaseAt = now;
                    message = error + "; automatic session is still waiting";
                } else if (autoEnabled && isSkippablePreTransactionChange(error)) {
                    quarantineCurrentAuto(client, error);
                } else abort(client, error);
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
        boolean verifying = switch (phase) {
            case VERIFY_BOARD, VERIFY_YOUR_ORDERS, RANK_BOARD, RANK_SEARCH,
                    CANCEL_BOARD, CANCEL_YOUR_ORDERS, CANCEL_MANAGE, CANCEL_CONFIRM,
                    CANCEL_VERIFY_BOARD, CANCEL_VERIFY_YOUR_ORDERS -> true;
            default -> false;
        };
        if (!verifying) {
            String error = liveError(currentPlan, now, true);
            if (!error.isEmpty()) {
                if (autoEnabled && (error.equals(FOCUSED_STALE) || isSkippablePreTransactionChange(error))) {
                    quarantineCurrentAuto(client, error);
                } else {
                    abort(client, error);
                }
                return;
            }
        }
        if (System.nanoTime() < nextActionAt) return;
        if (Duration.between(phaseAt, now).compareTo(SCREEN_TIMEOUT) > 0) {
            failCandidate(client, "server screen did not advance before its deadline");
            return;
        }
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
                case RANK_BOARD -> handleRankBoard(client, screen);
                case RANK_SEARCH -> handleRankSearch(client, screen);
                case CANCEL_BOARD -> handleCancelBoard(client, screen);
                case CANCEL_YOUR_ORDERS -> handleCancelYourOrders(client, screen);
                case CANCEL_MANAGE -> handleCancelManage(client, screen);
                case CANCEL_CONFIRM -> handleCancelConfirm(client, screen);
                case CANCEL_VERIFY_BOARD -> handleCancelVerifyBoard(client, screen);
                case CANCEL_VERIFY_YOUR_ORDERS -> handleCancelVerifyYourOrders(client, screen);
                default -> { }
            }
        } catch (RuntimeException errorValue) {
            failCandidate(client, "screen verification failed: " + safe(errorValue.getMessage()));
        }
    }

    private void maybeStartAuto(MinecraftClient client) {
        Instant now = Instant.now();
        if (!autoEnabled || now.isBefore(nextAutoAttempt) || client.currentScreen != null) return;
        String connectionError = serverError(client);
        if (!connectionError.isEmpty()) {
            message = connectionError + "; automatic session is waiting";
            nextAutoAttempt = now.plusSeconds(2);
            return;
        }
        boolean wasEmpty = autoQueue.isEmpty();
        int added = refreshAutoQueue();
        if (autoQueue.isEmpty()) {
            message = autoSessionRejectedItems.isEmpty()
                    ? "automatic order session active; waiting for reviewed candidates"
                    : "automatic order session active; " + autoSessionRejectedItems.size()
                            + " item" + (autoSessionRejectedItems.size() == 1 ? " is" : "s are")
                            + " ignored until this session ends";
            nextAutoAttempt = now.plusSeconds(1);
            return;
        }
        if (wasEmpty && added > 0) {
            tell(client, "Automatic order session queued " + added + " newly reviewed candidate" + (added == 1 ? "." : "s."));
        }
        String candidateID = autoQueue.getFirst();
        Optional<PortfolioAllocator.Selection> current = feed.allocation().selections().stream()
                .filter(selection -> selection.candidate().id().equals(candidateID)
                        && !feed.hasActiveOrder(selection.candidate().itemId())).findFirst();
        if (current.isEmpty()) { quarantineCurrentAuto(client, "reviewed allocation changed or disappeared"); return; }
        long cap = autoEscrowCaps.getOrDefault(candidateID, 0L);
        int authorizedBatches = authorizedBatches(current.get().batches(), current.get().candidate().acquisitionCost(), cap);
        if (authorizedBatches <= 0) { quarantineCurrentAuto(client, "reviewed escrow no longer covers this item"); return; }
        PortfolioAllocator.Selection next = new PortfolioAllocator.Selection(current.get().candidate(), authorizedBatches);
        ArmResult result = arm(next);
        if (!result.armed()) quarantineCurrentAuto(client, result.message());
    }

    private int refreshAutoQueue() {
        List<PortfolioAllocator.Selection> eligible = feed.allocation().selections().stream()
                .filter(selection -> !feed.hasActiveOrder(selection.candidate().itemId())).toList();
        List<PortfolioAllocator.Selection> additions = autoQueueAdditions(eligible,
                feed.allocation().availableOrderSlots(), Set.copyOf(autoQueue), new HashSet<>(autoItemIds.values()),
                autoSessionRejectedItems);
        for (PortfolioAllocator.Selection selection : additions) {
            String candidateID = selection.candidate().id();
            autoQueue.addLast(candidateID);
            autoEscrowCaps.put(candidateID, selection.capital());
            autoItemIds.put(candidateID, selection.candidate().itemId());
        }
        return additions.size();
    }

    static List<PortfolioAllocator.Selection> autoQueueAdditions(List<PortfolioAllocator.Selection> selections,
                                                                  int availableOrderSlots,
                                                                   Set<String> queuedCandidateIds,
                                                                   Set<String> queuedItemIds,
                                                                   Set<String> rejectedItemIds) {
        int capacity = Math.max(0, availableOrderSlots - queuedCandidateIds.size());
        if (capacity == 0 || selections == null || selections.isEmpty()) return List.of();
        List<PortfolioAllocator.Selection> result = new ArrayList<>();
        Set<String> candidateIds = new HashSet<>(queuedCandidateIds);
        Set<String> itemIds = new HashSet<>(queuedItemIds);
        for (PortfolioAllocator.Selection selection : selections) {
            if (result.size() >= capacity) break;
            if (selection == null || selection.candidate() == null) continue;
            String candidateID = selection.candidate().id();
            String itemID = selection.candidate().itemId();
            if (candidateID == null || itemID == null || candidateIds.contains(candidateID) || itemIds.contains(itemID)
                    || rejectedItemIds.contains(itemID)) continue;
            result.add(selection);
            candidateIds.add(candidateID);
            itemIds.add(itemID);
        }
        return List.copyOf(result);
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
        minimumProfitDollars = Math.max(1, current.conservativeProfit());
        if (!queuedID.equals(refreshed.candidateId())) {
            autoQueue.removeFirst();
            autoEscrowCaps.remove(queuedID);
            autoItemIds.remove(queuedID);
            autoQueue.remove(refreshed.candidateId());
            autoEscrowCaps.remove(refreshed.candidateId());
            autoItemIds.remove(refreshed.candidateId());
            autoQueue.addFirst(refreshed.candidateId());
            autoEscrowCaps.put(refreshed.candidateId(), escrowCap);
            autoItemIds.put(refreshed.candidateId(), refreshed.itemId());
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
            failKnownCandidate(client, "an existing personal order for this item was found; duplicate creation was blocked");
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
        Dialog dialog = requireDialog(screen, ITEM_DIALOG_TITLES, false);
        DialogTextInput field = requireSingleTextInput(screen, dialog, Set.of("search", "recherche", "item", "objet"));
        field.setText(plan.itemPathQuery());
        requireButton(screen, SEARCH_ACTIONS).onPress(new MouseInput(0, 0));
        transition(Phase.ITEM_RESULT, "waiting for the exact item among search results"); delay();
    }

    private void handleItemResult(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (localizedTitleEquals(title, ITEM_DIALOG_TITLES)) return;
        requireDialog(screen, ITEM_DIALOG_TITLES, true);
        if (!isItemResultTitle(title)) { failCandidate(client, "unexpected item-result dialog: " + title); return; }
        String registryName = expectedRegistryName();
        List<ButtonWidget> matches = buttons(screen).stream()
                .filter(button -> button.active && button.visible)
                .filter(button -> OrderPlan.exactItemResultLabel(button.getMessage().getString(), plan.itemId(), registryName, plan.itemName())).toList();
        if (matches.size() != 1) {
            failCandidate(client, "expected one exact canonical item result, found " + matches.size() + " in " + title); return;
        }
        matches.getFirst().onPress(new MouseInput(0, 0));
        transition(Phase.AMOUNT, "entering exact batch quantity"); delay();
    }

    private void handleAmount(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (localizedTitleStartsWith(title, ITEM_DIALOG_TITLES)) return;
        Dialog dialog = requireDialog(screen, AMOUNT_DIALOG_TITLES, false);
        DialogTextInput field = requireSingleTextInput(screen, dialog, Set.of("amount", "quantity", "how many", "quantité", "combien"));
        field.setText(Integer.toString(plan.quantity()));
        if (!field.getText().equals(Integer.toString(plan.quantity()))) { failCandidate(client, "amount field rejected the exact quantity"); return; }
        requireButton(screen, AMOUNT_ACTIONS).onPress(new MouseInput(0, 0));
        transition(Phase.PRICE, "entering exact unit reward"); delay();
    }

    private void handlePrice(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (localizedTitleEquals(title, AMOUNT_DIALOG_TITLES)) return;
        Dialog dialog = requireDialog(screen, PRICE_DIALOG_TITLES, false);
        DialogTextInput field = requireSingleTextInput(screen, dialog, Set.of("price", "reward", "prix", "récompense"));
        field.setText(plan.priceInput());
        if (!field.getText().equals(plan.priceInput())) { failCandidate(client, "price field rejected the exact cent value"); return; }
        requireButton(screen, PRICE_ACTIONS).onPress(new MouseInput(0, 0));
        transition(Phase.REVIEW, "performing final server-review validation"); delay();
    }

    private void handleReview(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (localizedTitleEquals(title, PRICE_DIALOG_TITLES)) return;
        Dialog dialog = requireDialog(screen, REVIEW_DIALOG_TITLES, false);
        String corpus = dialogText(dialog) + " " + buttons(screen).stream().map(button -> button.getMessage().getString()).reduce("", (a, b) -> a + " " + b);
        String registryName = expectedRegistryName();
        if (!reviewContainsExactItem(dialog, plan.itemId())) {
            failCandidate(client, "review item does not match the armed item"); return;
        }
        if (!OrderPlan.textContainsQuantity(corpus, plan.quantity())) {
            failCandidate(client, "review amount does not match the armed quantity"); return;
        }
        if (!OrderPlan.textContainsMoney(corpus, plan.unitRewardCents()) || !OrderPlan.textContainsMoney(corpus, plan.reviewTotalCents())) {
            LOGGER.warn("Order review economics mismatch: item={}; expected_unit_cents={}; expected_escrow_cents={}; review={}",
                    plan.itemId(), plan.unitRewardCents(), plan.reviewTotalCents(), safe(corpus));
            failCandidate(client, "review price or total does not match the armed economics"); return;
        }
        CandidateFeedClient.Candidate current = feed.candidate(plan.candidateId()).orElseThrow(() -> new IllegalStateException("candidate disappeared"));
        String liveError = liveError(plan, Instant.now(), true);
        if (!liveError.isEmpty()) {
            if (autoEnabled && (liveError.equals(FOCUSED_STALE) || isSkippablePreTransactionChange(liveError))) {
                quarantineCurrentAuto(client, liveError);
            } else {
                abort(client, liveError);
            }
            return;
        }
        requireButton(screen, CREATE_ACTIONS).onPress(new MouseInput(0, 0));
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
        List<Integer> exactMatches = personalOrderSlotsExact(client, lastSubmittedPlan, false);
        if (exactMatches.size() == 1) {
            OrderPlan.OrderProgress progress = OrderPlan.firstOrderProgress(stackText(containerSlot(client, exactMatches.getFirst())))
                    .orElseThrow(() -> new IllegalStateException("exact personal order has no parseable progress"));
            feed.recordOrderVerified(lastSubmittedPlan, progress.delivered());
            if (progress.delivered() > 0) {
                finishPartiallyFilledOrder(client, progress);
                return;
            }
            closeScreen(client);
            client.getNetworkHandler().sendChatCommand("orders");
            rankSortAttempts = 0;
            transition(Phase.RANK_BOARD, "verifying Most Per Item before checking public rank");
            delay();
            return;
        }
        if (exactMatches.size() > 1) { abort(client, "multiple exact personal orders matched the submitted values; automatic orders stopped"); return; }
        if (personalOrderCount(client, lastSubmittedPlan.itemId()) > 0) {
            abort(client, "personal order item exists but its price or quantity is ambiguous; automatic orders stopped"); return;
        }
        if (Duration.between(phaseAt, Instant.now()).compareTo(Duration.ofSeconds(1)) < 0) return;
        abort(client, "submitted order was not found in Your Orders; its item remains locked for manual review");
    }

    private void handleRankBoard(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (!(screen instanceof GenericContainerScreen) || !title.equals("Orders (Page 1)")) {
            abort(client, "unexpected screen while verifying order sort: " + title); return;
        }
        if (!orderPageDescending(client, 10)) {
            if (rankSortAttempts++ >= 3 || !isFilterControl(containerSlot(client, 47))) {
                LOGGER.warn("Could not prove Most Per Item before rank check: {}", menuFingerprint(client));
                abort(client, "Most Per Item ordering could not be proven before the rank check"); return;
            }
            clickSlot(client, 47);
            transition(Phase.RANK_BOARD, "cycling the order filter to prove Most Per Item");
            delay();
            return;
        }
        closeScreen(client);
        client.getNetworkHandler().sendChatCommand("order " + lastSubmittedPlan.itemPathQuery());
        transition(Phase.RANK_SEARCH, "searching the exact item to locate the new order");
        delay();
    }

    private void handleRankSearch(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (!(screen instanceof GenericContainerScreen) || !title.equals("Orders (Page 1)")) {
            abort(client, "unexpected screen while checking public order rank: " + title); return;
        }
        PublicRank result = publicRank(client, lastSubmittedPlan);
        if (result.rank() == 1) {
            finishRankedOrder(client, result.exactRows());
            return;
        }
        OptionalLong next = lastSubmittedPlan.nextUnitReward(currentEscrowCap, minimumProfitDollars);
        pendingRepriceUnitCents = next.orElse(0);
        feed.diagnostic("order_workflow", "ranked", Map.of("candidate_state", "active", "route", "ORDER_TO_AUCTION",
                "reason_code", pendingRepriceUnitCents > 0 ? "not_first_reprice" : "not_first_drop"));
        tell(client, lastSubmittedPlan.itemName() + " placed #" + result.rank() + ". Cancelling it "
                + (pendingRepriceUnitCents > 0 ? "before the bounded higher bid." : "because the next bid fails the reviewed profit/escrow limit."));
        closeScreen(client);
        client.getNetworkHandler().sendChatCommand("orders");
        transition(Phase.CANCEL_BOARD, "opening Your Orders for a verified rank replacement");
        delay();
    }

    private void handleCancelBoard(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (!(screen instanceof GenericContainerScreen) || !ORDERS_TITLE.matcher(title).matches()) {
            abort(client, "unexpected screen before rank replacement: " + title); return;
        }
        ItemStack stack = containerSlot(client, 51);
        if (!labelEquals(stack.getName().getString(), "Your Orders")) {
            abort(client, "Your Orders cancellation control did not match slot 51"); return;
        }
        clickSlot(client, 51);
        transition(Phase.CANCEL_YOUR_ORDERS, "locating the exact unfilled personal order");
        delay();
    }

    private void handleCancelYourOrders(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (ORDERS_TITLE.matcher(title).matches()) return;
        if (!(screen instanceof GenericContainerScreen) || !title.equals("Orders -> Your Orders")) {
            abort(client, "unexpected personal-orders cancellation screen: " + title); return;
        }
        List<Integer> matches = personalOrderSlotsExact(client, lastSubmittedPlan, true);
        if (matches.size() != 1) {
            abort(client, matches.isEmpty()
                    ? "the exact unfilled personal order could not be proven before cancellation"
                    : "the personal order to cancel was ambiguous");
            return;
        }
        clickSlot(client, matches.getFirst());
        transition(Phase.CANCEL_MANAGE, "opening the exact personal order controls");
        delay();
    }

    private void handleCancelManage(MinecraftClient client, Screen screen) {
        if (screen == null || title(screen).equals("Orders -> Your Orders")) return;
        if (!(screen instanceof GenericContainerScreen) || !title(screen).equals("Orders -> Edit Order")) {
            abort(client, "unexpected order-management screen: " + title(screen)); return;
        }
        List<Integer> controls = cancelOrderControlSlots(client);
        if (controls.size() != 1) {
            LOGGER.warn("Cancellation control not proven: {}", menuFingerprint(client));
            abort(client, "exactly one verified cancellation control was not present; no cancellation was attempted"); return;
        }
        clickSlot(client, controls.getFirst());
        cancelConfirmationSent = false;
        transition(Phase.CANCEL_CONFIRM, "waiting for the server cancellation outcome");
        delayVerification();
    }

    private void handleCancelConfirm(MinecraftClient client, Screen screen) {
        if (screen instanceof DialogScreen<?> && !cancelConfirmationSent) {
            String normalizedTitle = OrderPlan.normalizeLabel(title(screen));
            if (!normalizedTitle.contains("cancel") || !normalizedTitle.contains("order")) {
                abort(client, "unexpected dialog after Cancel Order: " + title(screen)); return;
            }
            requireButton(screen, Set.of("confirm", "confirm cancellation", "cancel order")).onPress(new MouseInput(0, 0));
            cancelConfirmationSent = true;
            delayVerification();
            return;
        }
        if (screen instanceof GenericContainerScreen && !cancelConfirmationSent) {
            String normalizedTitle = OrderPlan.normalizeLabel(title(screen));
            if (normalizedTitle.contains("cancel") && normalizedTitle.contains("order")) {
                List<Integer> controls = controlSlots(client, Set.of("confirm", "confirm cancellation", "confirm cancel order"));
                if (controls.size() != 1) {
                    abort(client, "generic cancellation confirmation was missing or ambiguous"); return;
                }
                clickSlot(client, controls.getFirst());
                cancelConfirmationSent = true;
                delayVerification();
                return;
            }
        }
        if (screen instanceof DialogScreen<?> && cancelConfirmationSent) return;
        closeScreen(client);
        client.getNetworkHandler().sendChatCommand("orders");
        transition(Phase.CANCEL_VERIFY_BOARD, "verifying that the cancelled order is absent");
        delay();
    }

    private void handleCancelVerifyBoard(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (!(screen instanceof GenericContainerScreen) || !ORDERS_TITLE.matcher(title).matches()) {
            abort(client, "unexpected cancellation-verification screen: " + title); return;
        }
        ItemStack stack = containerSlot(client, 51);
        if (!labelEquals(stack.getName().getString(), "Your Orders")) {
            abort(client, "Your Orders post-cancellation control did not match slot 51"); return;
        }
        clickSlot(client, 51);
        transition(Phase.CANCEL_VERIFY_YOUR_ORDERS, "proving the cancelled order is absent");
        delay();
    }

    private void handleCancelVerifyYourOrders(MinecraftClient client, Screen screen) {
        if (screen == null) return;
        String title = title(screen);
        if (ORDERS_TITLE.matcher(title).matches()) return;
        if (!(screen instanceof GenericContainerScreen) || !title.equals("Orders -> Your Orders")) {
            abort(client, "unexpected personal-orders post-cancellation screen: " + title); return;
        }
        if (personalOrderCount(client, lastSubmittedPlan.itemId()) > 0) {
            if (Duration.between(phaseAt, Instant.now()).compareTo(Duration.ofSeconds(1)) < 0) return;
            abort(client, "cancelled order is still present; no replacement was created");
            return;
        }
        OrderPlan cancelled = lastSubmittedPlan;
        feed.recordOrderCancelled(cancelled.itemId());
        sessionSpent = Math.max(0, sessionSpent - cancelled.escrowDollars());
        lastSubmittedPlan = null;
        if (pendingRepriceUnitCents <= 0) {
            finishDroppedOrder(client, cancelled, "the next rank bid was outside the reviewed profitable escrow");
            return;
        }
        plan = cancelled.withUnitReward(pendingRepriceUnitCents);
        pendingRepriceUnitCents = 0;
        retrying = true;
        armedAt = Instant.now();
        feed.candidate(plan.candidateId()).ifPresent(feed::focus);
        closeScreen(client);
        transition(Phase.WAIT_FRESH, "waiting for refund, fresh auction exit, and the bounded replacement bid");
        tell(client, "Cancellation verified. Rechecking before recreating " + plan.itemName() + " at $" + plan.priceInput()
                + " each; projected conservative profit is $" + FlipNotifier.format(plan.conservativeProfitDollars()) + ".");
    }

    private void finishRankedOrder(MinecraftClient client, int exactRows) {
        String itemName = lastSubmittedPlan.itemName();
        String candidateID = lastSubmittedPlan.candidateId();
        feed.markActiveOrder(lastSubmittedPlan.itemId());
        feed.diagnostic("order_workflow", "verified", Map.of("candidate_state", "active", "route", "ORDER_TO_AUCTION",
                "reason_code", "public_rank_one"));
        phase = Phase.IDLE;
        message = "verified " + itemName + " at public rank #1 among " + exactRows + " exact orders";
        lastSubmittedPlan = null;
        retrying = false;
        pendingRepriceUnitCents = 0;
        completeQueueItem(candidateID);
        nextAutoAttempt = Instant.now().plusSeconds(2);
        closeScreen(client);
        tell(client, "Order verified at public rank #1: " + itemName + (autoEnabled
                ? ". " + autoQueue.size() + " reviewed orders remain." : "."));
    }

    private void finishPartiallyFilledOrder(MinecraftClient client, OrderPlan.OrderProgress progress) {
        OrderPlan accepted = lastSubmittedPlan;
        String candidateID = accepted.candidateId();
        feed.diagnostic("order_workflow", "verified", Map.of("candidate_state", "active", "route", "ORDER_TO_AUCTION",
                "reason_code", "partial_fill_before_rank_check"));
        phase = Phase.IDLE;
        message = "verified " + accepted.itemName() + " after an immediate fill of " + progress.delivered()
                + "/" + progress.total() + "; repricing was disabled";
        lastSubmittedPlan = null;
        retrying = false;
        pendingRepriceUnitCents = 0;
        completeQueueItem(candidateID);
        nextAutoAttempt = Instant.now().plusSeconds(2);
        closeScreen(client);
        tell(client, "Order accepted and already filling: " + accepted.itemName() + " (" + progress.delivered()
                + "/" + progress.total() + "). It was kept safely; partial orders are never cancelled for repricing.");
    }

    private void finishCompletedOrder(MinecraftClient client, OrderPlan completed) {
        String candidateID = completed.candidateId();
        feed.diagnostic("order_workflow", "complete", Map.of("candidate_state", "claim_ready", "route", "ORDER_TO_AUCTION",
                "reason_code", "filled_before_rank_check"));
        phase = Phase.IDLE;
        message = "order filled completely and is ready for the claim/exit workflow: " + completed.itemName();
        plan = null;
        lastSubmittedPlan = null;
        retrying = false;
        pendingRepriceUnitCents = 0;
        completeQueueItem(candidateID);
        nextAutoAttempt = Instant.now().plusSeconds(2);
        closeScreen(client);
        tell(client, "Order filled completely: " + completed.itemName() + ". It is now queued for the verified claim and auction exit workflow.");
    }

    private void finishDroppedOrder(MinecraftClient client, OrderPlan dropped, String reason) {
        if (autoEnabled) autoSessionRejectedItems.add(dropped.itemId());
        completeQueueItem(dropped.candidateId());
        phase = Phase.IDLE;
        retrying = false;
        message = "dropped " + dropped.itemName() + ": " + reason;
        nextAutoAttempt = Instant.now().plusSeconds(1);
        closeScreen(client);
        tell(client, message + (autoEnabled ? ". " + autoQueue.size() + " reviewed orders remain." : "."));
    }

    private void completeQueueItem(String candidateID) {
        autoQueue.remove(candidateID);
        autoEscrowCaps.remove(candidateID);
        autoItemIds.remove(candidateID);
    }

    private void clearAutoQueueState() {
        autoQueue.clear();
        autoEscrowCaps.clear();
        autoItemIds.clear();
        autoSessionRejectedItems.clear();
    }

    private String liveError(OrderPlan expected, Instant now, boolean requireAllocation) {
        if (!feed.balanceUsableForOrders()) return "waiting for the live scoreboard balance or a manual override";
        if (!"ready".equals(feed.status().state())) return "backend candidate feed is not ready";
        // A successful conditional poll can legitimately return 304 while the
        // candidate payload's generated_at remains unchanged. Transport
        // freshness comes from lastSuccess; evidence freshness is checked
        // independently below for the focused order and auction observations.
        if (age(feed.status().lastSuccess(), now).compareTo(FEED_MAX_AGE) > 0) return "candidate feed is stale";
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
        if (autoEnabled) {
            feed.diagnostic("order_workflow", "held_continue", Map.of("candidate_state", phase.name(),
                    "route", "ORDER_TO_AUCTION", "reason_code", "item_failure_session_continues"));
            quarantineCurrentAuto(client, safeReason);
            return;
        }
        feed.diagnostic("order_workflow", "aborted", Map.of("candidate_state", phase.name(), "route", "ORDER_TO_AUCTION", "reason_code", "workflow_abort"));
        phase = Phase.ABORTED; message = safeReason; plan = null; autoEnabled = false; clearAutoQueueState();
        closeScreen(client);
        tell(client, "Order creation stopped safely: " + safeReason);
    }

    private void pauseAutoForConnection(MinecraftClient client, String reason) {
        // Once Create Order has been sent, retrying the same item could create a
        // duplicate. Keep its durable lock and move on after reconnect. Before
        // submission, retain the queue entry and simply restart its wizard.
        if (lastSubmittedPlan != null) {
            quarantineCurrentAuto(client, reason + "; submitted item remains locked for reconciliation");
            return;
        }
        plan = null;
        retrying = false;
        pendingRepriceUnitCents = 0;
        cancelConfirmationSent = false;
        phase = Phase.IDLE;
        nextAutoAttempt = Instant.now().plusSeconds(3);
        closeScreen(client);
        message = safe(reason);
    }

    private void quarantineCurrentAuto(MinecraftClient client, String reason) {
        Phase failedPhase = phase;
        String skipped = autoQueue.peekFirst();
        String itemID = plan != null ? plan.itemId() : autoItemIds.get(skipped);
        if (itemID != null && !itemID.isBlank()) autoSessionRejectedItems.add(itemID);
        List<String> removed = autoItemIds.entrySet().stream()
                .filter(entry -> itemID != null && itemID.equals(entry.getValue()))
                .map(Map.Entry::getKey).toList();
        if (removed.isEmpty() && skipped != null) removed = List.of(skipped);
        for (String candidateID : removed) {
            autoQueue.remove(candidateID);
            autoEscrowCaps.remove(candidateID);
            autoItemIds.remove(candidateID);
        }
        plan = null;
        lastSubmittedPlan = null;
        retrying = false;
        pendingRepriceUnitCents = 0;
        cancelConfirmationSent = false;
        phase = Phase.IDLE;
        nextAutoAttempt = Instant.now().plusSeconds(1);
        closeScreen(client);
        String ignored = itemID == null || itemID.isBlank() ? "one item" : itemID;
        if (autoQueue.isEmpty()) {
            message = "ignored " + ignored + " for this session: " + safe(reason)
                    + "; waiting for other reviewed candidates";
        } else {
            message = "ignored " + ignored + " for this session: " + safe(reason)
                    + "; " + autoQueue.size() + " remain";
        }
        feed.diagnostic("order_workflow", "auto_item_quarantined", Map.of("candidate_state", failedPhase.name(),
                "route", "ORDER_TO_AUCTION", "reason_code", "item_failed_for_session"));
        tell(client, message);
    }

    private void failCandidate(MinecraftClient client, String reason) {
        if (autoEnabled && isPreSubmitItemPhase(phase)) quarantineCurrentAuto(client, reason);
        else abort(client, reason);
    }

    private void failKnownCandidate(MinecraftClient client, String reason) {
        if (autoEnabled) quarantineCurrentAuto(client, reason);
        else abort(client, reason);
    }

    static boolean isPreSubmitItemPhase(Phase value) {
        return switch (value) {
            case ITEM_SEARCH, ITEM_RESULT, AMOUNT, PRICE, REVIEW -> true;
            default -> false;
        };
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

    static boolean isTransientWaitError(String error) {
        return error != null && (error.equals(FOCUSED_STALE)
                || error.equals("backend candidate feed is not ready")
                || error.equals("candidate feed is stale")
                || error.equals("auction exit is stale")
                || error.equals("waiting for the live scoreboard balance or a manual override"));
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

    private static int personalOrderCountExact(MinecraftClient client, OrderPlan expected, boolean requireUnfilled) {
        return personalOrderSlotsExact(client, expected, requireUnfilled).size();
    }

    private static List<Integer> personalOrderSlotsExact(MinecraftClient client, OrderPlan expected, boolean requireUnfilled) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return List.of();
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        List<Integer> result = new ArrayList<>();
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (!itemMatches(stack, expected.itemId())) continue;
            String text = stackText(stack);
            if (OrderPlan.textContainsUnitReward(text, expected.unitRewardCents())
                    && OrderPlan.textContainsOrderProgress(text, expected.quantity(), requireUnfilled)) result.add(index);
        }
        return List.copyOf(result);
    }

    private static PublicRank publicRank(MinecraftClient client, OrderPlan expected) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) {
            throw new IllegalStateException("public rank screen is not a generic container");
        }
        int limit = Math.min(45, Math.min(handler.getInventory().size(), handler.slots.size()));
        int exactRows = 0;
        int matchingRows = 0;
        int rank = 0;
        long previous = Long.MAX_VALUE;
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (!itemMatches(stack, expected.itemId())) continue;
            String text = stackText(stack);
            OptionalLong displayed = OrderPlan.firstUnitRewardCents(text);
            if (displayed.isEmpty()) throw new IllegalStateException("exact item row has no parseable unit reward");
            if (displayed.getAsLong() > previous) throw new IllegalStateException("exact search results are not Most Per Item");
            previous = displayed.getAsLong();
            exactRows++;
            if (OrderPlan.textContainsUnitReward(text, expected.unitRewardCents())
                    && OrderPlan.textContainsOrderProgress(text, expected.quantity(), true)) {
                matchingRows++;
                rank = exactRows;
            }
        }
        if (exactRows == 0) throw new IllegalStateException("exact item search returned no canonical rows");
        if (matchingRows != 1) throw new IllegalStateException("public order identity is missing or ambiguous");
        return new PublicRank(rank, exactRows);
    }

    private static boolean orderPageDescending(MinecraftClient client, int minimumRows) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return false;
        int limit = Math.min(45, Math.min(handler.getInventory().size(), handler.slots.size()));
        long previous = Long.MAX_VALUE;
        int rows = 0;
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (stack.isEmpty()) continue;
            OptionalLong displayed = OrderPlan.firstUnitRewardCents(stackText(stack));
            if (displayed.isEmpty() || displayed.getAsLong() > previous) return false;
            previous = displayed.getAsLong();
            rows++;
        }
        return rows >= minimumRows;
    }

    private static boolean isFilterControl(ItemStack stack) {
        Identifier id = Registries.ITEM.getId(stack.getItem());
        return id != null && id.toString().equals("minecraft:hopper")
                && OrderPlan.normalizeLabel(stack.getName().getString()).equals("filter");
    }

    private static List<Integer> controlSlots(MinecraftClient client, String exactLabel) {
        return controlSlots(client, Set.of(exactLabel));
    }

    private static List<Integer> cancelOrderControlSlots(MinecraftClient client) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return List.of();
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        List<Integer> result = new ArrayList<>();
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (stack.isEmpty()) continue;
            Identifier id = Registries.ITEM.getId(stack.getItem());
            if (id != null && isCancelOrderControl(id.toString(), stack.getName().getString())) result.add(index);
        }
        return List.copyOf(result);
    }

    static boolean isCancelOrderControl(String itemId, String label) {
        if (!"minecraft:red_terracotta".equals(itemId)) return false;
        String normalized = OrderPlan.normalizeLabel(label);
        return normalized.equals("cancel") || normalized.equals("cancel order");
    }

    private static List<Integer> controlSlots(MinecraftClient client, Set<String> exactLabels) {
        if (!(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return List.of();
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        Set<String> expected = exactLabels.stream().map(OrderPlan::normalizeLabel).collect(java.util.stream.Collectors.toUnmodifiableSet());
        List<Integer> result = new ArrayList<>();
        for (int index = 0; index < limit; index++) {
            ItemStack stack = handler.getSlot(index).getStack();
            if (!stack.isEmpty() && expected.contains(OrderPlan.normalizeLabel(stack.getName().getString()))) result.add(index);
        }
        return List.copyOf(result);
    }

    private static boolean itemMatches(ItemStack stack, String expectedItemId) {
        if (stack == null || stack.isEmpty()) return false;
        Identifier id = Registries.ITEM.getId(stack.getItem());
        return id != null && id.toString().equals(expectedItemId);
    }

    private static String stackText(ItemStack stack) {
        StringBuilder value = new StringBuilder(stack.getName().getString());
        LoreComponent lore = stack.get(DataComponentTypes.LORE);
        if (lore != null) for (Text line : lore.lines()) value.append(' ').append(line.getString());
        return value.toString();
    }

    private static Dialog requireDialog(Screen screen, Set<String> titles, boolean allowSuffix) {
        if (!(screen instanceof DialogScreen<?> dialogScreen) || !(screen instanceof DialogScreenAccessor accessor)) {
            throw new IllegalStateException("expected a server dialog");
        }
        String actual = title(dialogScreen);
        boolean matches = allowSuffix ? localizedTitleStartsWith(actual, titles) : localizedTitleEquals(actual, titles);
        if (!matches) {
            throw new IllegalStateException("unexpected dialog title " + title(dialogScreen));
        }
        return accessor.donut$getDialog();
    }

    static boolean localizedTitleEquals(String actual, Set<String> expected) {
        String normalized = OrderPlan.normalizeLabel(actual);
        return expected != null && expected.stream().map(OrderPlan::normalizeLabel).anyMatch(normalized::equals);
    }

    static boolean localizedTitleStartsWith(String actual, Set<String> expected) {
        String normalized = OrderPlan.normalizeLabel(actual);
        return expected != null && expected.stream().map(OrderPlan::normalizeLabel)
                .anyMatch(root -> normalized.equals(root) || normalized.startsWith(root + " "));
    }

    static boolean isItemResultTitle(String actual) {
        String normalized = OrderPlan.normalizeLabel(actual);
        for (String title : ITEM_DIALOG_TITLES) {
            String root = OrderPlan.normalizeLabel(title);
            if (normalized.matches(Pattern.quote(root) + " (?:0|[1-9][0-9]{0,2}) (?:result|results|resultat|resultats)")) return true;
        }
        return false;
    }

    private static DialogTextInput requireSingleTextInput(Screen screen, Dialog dialog, Set<String> expectedLabels) {
        List<DialogTextInput> fields = descendants(screen).stream().map(DialogTextInput::of)
                .filter(java.util.Objects::nonNull).toList();
        if (fields.size() != 1) {
            throw new IllegalStateException("expected exactly one supported text input, found " + fields.size());
        }
        List<DialogInput> declared = dialog.common().inputs().stream()
                .filter(input -> input.control() instanceof TextInputControl).toList();
        if (declared.size() != 1 || !recognizedTextInput(declared.getFirst(), expectedLabels)) {
            throw new IllegalStateException("dialog input label is missing or ambiguous");
        }
        return fields.getFirst();
    }

    static boolean recognizedTextInput(DialogInput input, Set<String> expectedLabels) {
        if (input == null || !(input.control() instanceof TextInputControl text) || expectedLabels == null) return false;
        return recognizedTextInputDescriptor(input.key(), text.label().getString(), expectedLabels);
    }

    static boolean recognizedTextInputDescriptor(String inputKey, String inputLabel, Set<String> expectedLabels) {
        if (expectedLabels == null) return false;
        String key = OrderPlan.normalizeLabel(inputKey);
        String label = OrderPlan.normalizeLabel(inputLabel);
        return expectedLabels.stream().map(OrderPlan::normalizeLabel)
                .anyMatch(expected -> !expected.isEmpty() && (key.contains(expected) || label.contains(expected)));
    }

    private static ButtonWidget requireButton(Screen screen, Set<String> exactLabels) {
        Set<String> expected = exactLabels.stream().map(OrderPlan::normalizeLabel)
                .collect(java.util.stream.Collectors.toUnmodifiableSet());
        List<ButtonWidget> matches = buttons(screen).stream().filter(button -> button.active && button.visible)
                .filter(button -> expected.contains(OrderPlan.normalizeLabel(button.getMessage().getString()))).toList();
        if (matches.size() != 1) throw new IllegalStateException("required action button is missing or ambiguous");
        return matches.getFirst();
    }

    private static List<ButtonWidget> buttons(Screen screen) {
        return descendants(screen).stream().filter(ButtonWidget.class::isInstance).map(ButtonWidget.class::cast).toList();
    }

    /**
     * Server dialog inputs can be nested inside ScrollableLayoutWidget's
     * ContainerWidget rather than registered as direct Screen children.
     * Traverse only the public ParentElement tree, with identity and depth
     * bounds so malformed or cyclic UI trees remain fail-closed.
     */
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
    private static void closeScreen(MinecraftClient client) {
        if (client.currentScreen == null) return;
        if (client.currentScreen instanceof HandledScreen<?> && client.player != null) client.player.closeHandledScreen();
        else client.setScreen(null);
    }
    private static void tell(MinecraftClient client, String value) {
        if (client.player != null) client.player.sendMessage(Text.literal("[DN] " + value), false);
    }
}
