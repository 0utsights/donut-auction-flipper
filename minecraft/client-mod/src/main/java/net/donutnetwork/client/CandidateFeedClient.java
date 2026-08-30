package net.donutnetwork.client;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import net.fabricmc.loader.api.FabricLoader;

import java.io.InputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.math.BigDecimal;
import java.math.RoundingMode;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.OptionalLong;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Consumer;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

final class CandidateFeedClient implements AutoCloseable {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final int MAX_RESPONSE_BYTES = 1 << 20;
    private static final int MAX_CANDIDATES = 100;
    private static final Pattern ITEM_ID = Pattern.compile("[a-z0-9_.-]+:[a-z0-9_./-]+");
    private static final Pattern AUCTION_COMMAND = Pattern.compile("/ah(?: [a-z0-9_-]{1,64})?");
    private static final Pattern LABELED_BALANCE = Pattern.compile("(?i)\\b(?:balance|money|cash)\\b[^$0-9]{0,20}\\$?\\s*([0-9][0-9,]*(?:\\.[0-9]+)?)([KMBT]?)\\b");
    private static final Pattern SIDEBAR_BALANCE = Pattern.compile("(?i)^\\s*\\$\\s*([0-9][0-9,]*(?:\\.[0-9]+)?)([KMBT]?)\\s*$");
    private static final Pattern COMPLETE_ORDER = Pattern.compile("(?i)^Your (.{1,128}?) order is complete!{0,3}$");
    private static final Pattern ITEM_DELIVERY = Pattern.compile("(?i)^.{1,64}? delivered you ([0-9][0-9,]*) (.{1,128})$");
    private static final String MOD_VERSION = FabricLoader.getInstance().getModContainer("donut-network-client")
            .map(container -> container.getMetadata().getVersion().getFriendlyString()).orElse("unknown");

    record Candidate(String id, String route, String state, String reason, String signature, String itemId,
                     String itemName, int quantity, int maxStackSize, long acquisitionCost, long expectedProceeds,
                     long observedOrderUnitRewardCents, long orderUnitRewardCents, long targetListPrice,
                     long conservativeProfit, int marginBps, int completionBps, int expectedCycleMinutes,
                     long riskAdjustedProfitDay, int executableBatches, int queuePosition, int orderSlots, int auctionSlots,
                     int inventorySlots, long profitInventorySlot, int confidenceBps, String orderTier,
                     Instant orderFreshAt, Instant focusedFreshAt, Instant auctionFreshAt, String orderCommand, String auctionCommand) {}
    record Status(String state, Instant lastSuccess, String message, long version, int candidateCount) {}
    record DecodedFeed(long version, Instant generatedAt, List<Candidate> candidates) {}
    record ShulkerSupply(String auctionId, String seller, String itemId, long price, Instant lastSeen, Instant expiresAt) {}

    private final ClientConfig.Settings config;
    private final Consumer<PortfolioAllocator.Selection> alertSink;
    private final HttpClient http;
    private final ScheduledExecutorService scheduler;
    private final AtomicReference<List<Candidate>> candidates = new AtomicReference<>(List.of());
    private final AtomicReference<PortfolioAllocator.Allocation> allocation;
    private final AtomicReference<Status> status = new AtomicReference<>(new Status("waiting", Instant.EPOCH, "not started", 0, 0));
    private final AtomicReference<Instant> generatedAt = new AtomicReference<>(Instant.EPOCH);
    private final AtomicReference<List<ShulkerSupply>> shulkerSupplies = new AtomicReference<>(List.of());
    private final AtomicReference<Instant> shulkerSupplySuccess = new AtomicReference<>(Instant.EPOCH);
    private final AtomicLong balance;
    private final AtomicReference<String> balanceSource = new AtomicReference<>("saved fallback");
    private final AtomicLong pendingBalanceCeiling = new AtomicLong(Long.MAX_VALUE);
    private final AtomicLong pendingBalanceUntilMillis = new AtomicLong();
    private final AtomicLong pendingRefundFloor = new AtomicLong(-1);
    /**
     * Exact pre-submit local balances for orders created by this running client.
     * Donut rounds the sidebar to compact units (for example $126M), so a
     * legitimate cancellation refund can be smaller than the visible rounding
     * bucket.  The verified disappearance of that exact, still-unfilled order
     * restores this already-known local value; a later scoreboard sample remains
     * authoritative.
     */
    private final ConcurrentHashMap<String, Long> preSubmitBalances = new ConcurrentHashMap<>();
    private final AtomicLong usedSlots;
    private final Set<String> activeOrderItems = ConcurrentHashMap.newKeySet();
    private final ConcurrentHashMap<String, LocalOrderPosition> orderPositions = new ConcurrentHashMap<>();
    private final OrderPositionStore positionStore;
    private final AtomicBoolean diagnostics;
    private final ConcurrentLinkedQueue<JsonObject> diagnosticQueue = new ConcurrentLinkedQueue<>();
    private final LinkedHashMap<String, Boolean> seen = new LinkedHashMap<>();
    private final PortfolioAllocator allocator = new PortfolioAllocator();
    private final RepeatedFailureLimiter pollFailureLimiter = new RepeatedFailureLimiter(Duration.ofSeconds(30));
    private String etag = "";

    CandidateFeedClient(ClientConfig.Settings config, Consumer<PortfolioAllocator.Selection> alertSink) {
        this(config, alertSink, HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build());
    }

    CandidateFeedClient(ClientConfig.Settings config, Consumer<PortfolioAllocator.Selection> alertSink, HttpClient http) {
        this(config, alertSink, http, OrderPositionStore.inConfigDirectory());
    }

    CandidateFeedClient(ClientConfig.Settings config, Consumer<PortfolioAllocator.Selection> alertSink, HttpClient http,
                        OrderPositionStore positionStore) {
        this.config = Objects.requireNonNull(config); this.alertSink = Objects.requireNonNull(alertSink); this.http = Objects.requireNonNull(http);
        this.positionStore = Objects.requireNonNull(positionStore);
        balance = new AtomicLong(config.balance());
        orderPositions.putAll(positionStore.load());
        // Older builds persisted durable position IDs into active_order_items.
        // Positions already provide their own duplicate lock, so remove those
        // migrated IDs from the separate live/legacy server-order lock set.
        activeOrderItems.addAll(liveOrderLocks(config.activeOrderItems(), orderPositions.keySet()));
        usedSlots = new AtomicLong(packSlots(Math.max(config.usedOrderSlots(), activeOrderItems.size()), config.usedAuctionSlots()));
        diagnostics = new AtomicBoolean(config.diagnostics());
        allocation = new AtomicReference<>(allocator.allocate(List.of(), config.balance(),
                Math.max(config.usedOrderSlots(), activeOrderItems.size()), config.usedAuctionSlots(), duplicateLockedItemIds()));
        scheduler = Executors.newSingleThreadScheduledExecutor(runnable -> { Thread thread = new Thread(runnable, "donut-candidate-feed"); thread.setDaemon(true); return thread; });
    }

    void start() {
        enqueueDiagnostic("startup", "", 0, Map.of("state", "started"));
        scheduler.scheduleWithFixedDelay(this::pollSafely, 0, config.pollInterval().toMillis(), TimeUnit.MILLISECONDS);
        scheduler.scheduleWithFixedDelay(this::pollShulkerSuppliesSafely, 0, 2, TimeUnit.SECONDS);
        scheduler.scheduleWithFixedDelay(this::flushDiagnosticsSafely, 5, 5, TimeUnit.SECONDS);
    }

    Status status() { return status.get(); }
    List<Candidate> candidates() { return candidates.get(); }
    PortfolioAllocator.Allocation allocation() { return allocation.get(); }
    long balance() { return balance.get(); }
    String balanceSource() { return balanceSource.get(); }
    boolean balanceUsableForOrders() {
        String source = balanceSource.get();
        return !source.equals("saved fallback") && !source.startsWith("waiting for cancellation");
    }
    int usedOrderSlots() { return unpackOrder(usedSlots.get()); }
    int usedAuctionSlots() { return unpackAuction(usedSlots.get()); }
    boolean diagnosticsEnabled() { return diagnostics.get(); }
    Instant generatedAt() { return generatedAt.get(); }
    Set<String> orderServerHosts() { return config.orderServerHosts(); }
    List<ShulkerSupply> shulkerSupplies() { return shulkerSupplies.get(); }
    Instant shulkerSupplySuccess() { return shulkerSupplySuccess.get(); }

    Optional<Candidate> candidate(String id) {
        return candidates.get().stream().filter(value -> value.id().equals(id)).findFirst();
    }

    Optional<Candidate> currentExitCandidate(LocalOrderPosition position, Instant now, Duration maximumAuctionAge) {
        if (position == null || now == null || maximumAuctionAge == null || maximumAuctionAge.isNegative()) {
            return Optional.empty();
        }
        return candidates.get().stream()
                .filter(value -> value.route().equals("ORDER_TO_AUCTION"))
                .filter(value -> value.itemId().equals(position.itemId())
                        && value.signature().equals(position.signature())
                        && value.quantity() == position.batchQuantity())
                .filter(value -> !value.auctionFreshAt().equals(Instant.EPOCH)
                        && !value.auctionFreshAt().isAfter(now)
                        && Duration.between(value.auctionFreshAt(), now).compareTo(maximumAuctionAge) <= 0)
                .filter(value -> value.targetListPrice() > 0 && value.expectedProceeds() > 0)
                .max(java.util.Comparator.comparing(Candidate::auctionFreshAt));
    }

    boolean isAllocated(String id) {
        return allocation.get().selections().stream().anyMatch(selection -> selection.candidate().id().equals(id));
    }

    int allocatedBatches(String id) {
        return allocation.get().selections().stream().filter(selection -> selection.candidate().id().equals(id))
                .mapToInt(PortfolioAllocator.Selection::batches).findFirst().orElse(0);
    }

    boolean hasActiveOrder(String itemId) { return activeOrderItems.contains(itemId) || orderPositions.containsKey(itemId); }
    int activeOrderCount() { return (int) java.util.stream.Stream.concat(activeOrderItems.stream(), orderPositions.keySet().stream()).distinct().count(); }
    List<LocalOrderPosition> orderPositions() {
        return orderPositions.values().stream().sorted(java.util.Comparator.comparing(LocalOrderPosition::createdAt)).toList();
    }
    Optional<LocalOrderPosition> orderPosition(String itemId) { return Optional.ofNullable(orderPositions.get(itemId)); }
    List<AuctionExitPlan> readyExitPlans() {
        return orderPositions().stream().filter(position -> position.deliveredQuantity() == position.totalQuantity()
                        && position.state() != LocalOrderPosition.State.HOLD && position.state() != LocalOrderPosition.State.EXITED)
                .map(position -> {
                    try { return AuctionExitPlan.from(position); }
                    catch (IllegalArgumentException | ArithmeticException ignored) { return null; }
                }).filter(Objects::nonNull).toList();
    }

    int exitReadyCount() {
        return (int) orderPositions.values().stream().filter(position -> position.state() == LocalOrderPosition.State.CLAIM_READY
				|| position.state() == LocalOrderPosition.State.SUPPLY_PENDING
				|| position.state() == LocalOrderPosition.State.CLAIM_PENDING || position.state() == LocalOrderPosition.State.CLAIMING
                || position.state() == LocalOrderPosition.State.CLAIMED || position.state() == LocalOrderPosition.State.PACKAGE_PENDING
                || position.state() == LocalOrderPosition.State.PACKAGING || position.state() == LocalOrderPosition.State.LISTING_PENDING
                || position.state() == LocalOrderPosition.State.LISTING).count();
    }

    void recordExitState(String itemId, LocalOrderPosition.State state) {
        if (itemId == null || state == null) return;
        orderPositions.computeIfPresent(itemId, (ignored, position) -> position.withState(state, Instant.now()));
        requirePositionPersistence();
    }

    int rearmSafePreClaimHolds() {
        Map<String, LocalOrderPosition> before = Map.copyOf(orderPositions);
        AtomicLong rearmed = new AtomicLong();
        Instant now = Instant.now();
        orderPositions.replaceAll((ignored, position) -> {
            if (!position.safePreClaimHold()) return position;
            rearmed.incrementAndGet();
            return position.withState(LocalOrderPosition.State.CLAIM_READY, now);
        });
        if (rearmed.get() > 0 && !persistAndAllocate()) {
            orderPositions.clear();
            orderPositions.putAll(before);
            persistAndAllocate();
            throw new IllegalStateException("safe held-order rechecks could not be persisted");
        }
        return Math.toIntExact(rearmed.get());
    }

    void recordClaimed(String itemId, int totalClaimed, boolean direct) {
        if (itemId == null) return;
        AtomicBoolean freedOrderSlot = new AtomicBoolean();
        orderPositions.computeIfPresent(itemId, (ignored, position) -> {
            LocalOrderPosition next = position.claimed(totalClaimed, direct, Instant.now());
            if (position.claimedQuantity() < position.totalQuantity() && next.claimedQuantity() == next.totalQuantity()) {
                freedOrderSlot.set(true);
            }
            return next;
        });
        if (freedOrderSlot.get()) {
            activeOrderItems.remove(itemId);
            usedSlots.updateAndGet(value -> packSlots(Math.max(0, unpackOrder(value) - 1), unpackAuction(value)));
        }
        requirePositionPersistence();
    }

    void recordPackaged(String itemId, int totalPackaged) {
        if (itemId == null) return;
        orderPositions.computeIfPresent(itemId, (ignored, position) -> position.packaged(totalPackaged, Instant.now()));
        requirePositionPersistence();
    }

    void recordListed(String itemId, int totalListed) {
        if (itemId == null) return;
        AtomicBoolean exited = new AtomicBoolean();
        AtomicBoolean advanced = new AtomicBoolean();
        orderPositions.computeIfPresent(itemId, (ignored, position) -> {
            LocalOrderPosition next = position.listed(totalListed, Instant.now());
            advanced.set(next.listedQuantity() > position.listedQuantity());
            exited.set(next.state() == LocalOrderPosition.State.EXITED);
            return next;
        });
        if (advanced.get()) {
            usedSlots.updateAndGet(value -> packSlots(unpackOrder(value), Math.min(18, unpackAuction(value) + 1)));
        }
        // Persist EXITED before removing the row. If cleanup persistence fails,
        // the durable state is still terminal and a restart cannot relist it.
        requirePositionPersistence();
        if (exited.get()) {
            orderPositions.remove(itemId);
            activeOrderItems.remove(itemId);
            requirePositionPersistence();
        }
    }

    void markActiveOrder(String itemId) {
        if (itemId != null && ITEM_ID.matcher(itemId).matches() && activeOrderItems.add(itemId)) {
            usedSlots.updateAndGet(value -> packSlots(Math.max(unpackOrder(value), activeOrderItems.size()), unpackAuction(value)));
            persistAndAllocate();
        }
    }

    void recordOrderCancelled(String itemId) {
        if (itemId == null || !ITEM_ID.matcher(itemId).matches()) return;
        Long preSubmit = preSubmitBalances.remove(itemId);
        activeOrderItems.remove(itemId);
        orderPositions.remove(itemId);
        usedSlots.updateAndGet(value -> packSlots(Math.max(activeOrderItems.size(), unpackOrder(value) - 1), unpackAuction(value)));
        // The server menu has proven the exact unfilled order absent. Restore
        // the exact pre-submit local value when this process created it. This
        // avoids waiting forever when the compact sidebar hides the refund in
        // the same K/M/B display bucket. A subsequent scoreboard value still
        // replaces this local reconciliation normally.
        if (preSubmit != null) balance.updateAndGet(current -> reconciledCancellationBalance(current, preSubmit));
        pendingBalanceCeiling.set(Long.MAX_VALUE);
        pendingBalanceUntilMillis.set(0);
        pendingRefundFloor.set(-1);
        balanceSource.set(preSubmit == null ? "waiting for cancellation scoreboard" : "verified cancellation refund");
        persistAndAllocate();
        enqueueDiagnostic("order_workflow", "cancelled", 0, Map.of("item_id", itemId,
                "route", "ORDER_TO_AUCTION", "reason_code", "verified_absent_after_cancel"));
    }

    void recheckTrackedOrders() {
        // Clearing only re-admits candidates to the arm screen. Every arm still
        // opens the verified personal-order menu and blocks an exact item match
        // before any transactional control is used.
        activeOrderItems.clear();
        persistAndAllocate();
        // Recheck only the strongest newly available item. Enqueuing all twenty
        // as manual watches would starve market discovery; opening or arming any
        // other row starts its own focused check on demand.
        allocation.get().selections().stream().findFirst().ifPresent(selection -> focus(selection.candidate()));
    }

    void adjustBalance(long delta) {
        balance.updateAndGet(value -> delta > 0 && value > Long.MAX_VALUE - delta ? Long.MAX_VALUE : Math.max(0, value + delta));
        pendingBalanceCeiling.set(Long.MAX_VALUE);
        pendingBalanceUntilMillis.set(0);
        pendingRefundFloor.set(-1);
        balanceSource.set("manual");
        persistAndAllocate();
    }

    void observeBalance(String message) {
        parseBalance(message, LABELED_BALANCE).ifPresent(value -> updateBalance(value, "labeled chat"));
    }

    boolean observeSidebarBalance(Iterable<String> lines) {
        if (lines == null) return false;
        for (String line : lines) {
            OptionalLong parsed = parseBalance(line, SIDEBAR_BALANCE);
            if (parsed.isPresent()) {
                updateBalance(parsed.getAsLong(), "scoreboard");
                return true;
            }
        }
        return false;
    }

    static OptionalLong parseSidebarBalance(String line) {
        return parseBalance(line, SIDEBAR_BALANCE);
    }

    private static OptionalLong parseBalance(String text, Pattern pattern) {
        String clean = text == null ? "" : text.replaceAll("§[0-9A-FK-ORa-fk-or]", "")
                .replace("§", "")
                .replaceAll("[\\p{Cc}\\p{Cf}]", "").strip()
                // Donut's 1.21.11 scoreboard can expose the formatting-code payload
                // without its section-sign prefix, or with a duplicated section sign
                // (for example, `f$ 143M` or `§§f$ 143M`). Remove only a bounded
                // legacy-format prefix immediately before the anchored currency row.
                .replaceFirst("(?i)^(?:§|[0-9A-FK-OR]){1,4}(?=\\s*\\$)", "");
        Matcher matcher = pattern.matcher(clean);
        if (!matcher.find()) return OptionalLong.empty();
        try {
            BigDecimal amount = new BigDecimal(matcher.group(1).replace(",", ""));
            long multiplier = switch (matcher.group(2).toUpperCase(Locale.ROOT)) {
                case "K" -> 1_000L;
                case "M" -> 1_000_000L;
                case "B" -> 1_000_000_000L;
                case "T" -> 1_000_000_000_000L;
                default -> 1L;
            };
            BigDecimal dollars = amount.multiply(BigDecimal.valueOf(multiplier)).setScale(0, RoundingMode.DOWN);
            if (dollars.signum() < 0 || dollars.compareTo(BigDecimal.valueOf(Long.MAX_VALUE)) > 0) return OptionalLong.empty();
            return OptionalLong.of(dollars.longValueExact());
        } catch (ArithmeticException | NumberFormatException error) {
            return OptionalLong.empty();
        }
    }

    private void updateBalance(long observed, String source) {
        long effective = observed;
        String effectiveSource = source;
        if (source.equals("scoreboard")) {
            long ceiling = pendingBalanceCeiling.get();
            if (System.currentTimeMillis() < pendingBalanceUntilMillis.get() && observed > ceiling) {
                effective = ceiling;
                effectiveSource = "scoreboard (pending order)";
            } else {
                pendingBalanceCeiling.set(Long.MAX_VALUE);
                pendingBalanceUntilMillis.set(0);
            }
        }
        if (balanceSource.get().startsWith("waiting for cancellation")) {
            long floor = pendingRefundFloor.get();
            if (floor >= 0 && effective <= floor) return;
            pendingRefundFloor.set(-1);
        }
        long previous = balance.getAndSet(effective);
        String previousSource = balanceSource.getAndSet(effectiveSource);
        if (previous != effective || !previousSource.equals(effectiveSource)) persistAndAllocate();
    }

    void adjustUsedSlots(int orderDelta, int auctionDelta) {
        usedSlots.updateAndGet(value -> packSlots(Math.max(0, Math.min(20, unpackOrder(value) + orderDelta)),
                Math.max(0, Math.min(18, unpackAuction(value) + auctionDelta))));
        persistAndAllocate();
    }

    void reconcileUsedAuctionSlots(int observed) {
        if (observed < 0 || observed > 18) throw new IllegalArgumentException("observed auction slots must be between 0 and 18");
        usedSlots.updateAndGet(value -> packSlots(unpackOrder(value), observed));
        persistAndAllocate();
    }

    void reconcilePersonalOrders(Set<String> observedItemIds, int occupiedSlots, Map<String, Integer> progressUpdates) {
        Set<String> observed = validatedPersonalOrderSnapshot(observedItemIds, occupiedSlots);
        if (progressUpdates == null || !observed.containsAll(progressUpdates.keySet())
                || progressUpdates.entrySet().stream().anyMatch(entry -> entry.getKey() == null
                || !ITEM_ID.matcher(entry.getKey()).matches() || entry.getValue() == null || entry.getValue() < 0)) {
            throw new IllegalArgumentException("invalid tracked-order progress reconciliation");
        }
        Instant now = Instant.now();
        progressUpdates.forEach((itemId, delivered) -> orderPositions.computeIfPresent(itemId,
                (ignored, position) -> position.verified(delivered, now)));
        activeOrderItems.clear();
        activeOrderItems.addAll(liveOrderLocks(observed, orderPositions.keySet()));
        usedSlots.updateAndGet(value -> packSlots(occupiedSlots, unpackAuction(value)));
        persistAndAllocate();
    }

    void setDiagnostics(boolean enabled) { diagnostics.set(enabled); ClientConfig.saveDiagnostics(enabled); }

    void diagnostic(String event, String code, Map<String, String> fields) {
        enqueueDiagnostic(event, code, 0, fields);
    }

    void recordOrderSubmitted(Candidate candidate, OrderPlan plan) {
        LocalOrderPosition position = LocalOrderPosition.submitted(candidate, plan, Instant.now());
        orderPositions.put(position.itemId(), position);
        long beforeEscrow = balance.getAndUpdate(value -> Math.max(0, value - plan.escrowDollars()));
        preSubmitBalances.put(position.itemId(), beforeEscrow);
        long afterEscrow = balance.get();
        pendingBalanceCeiling.set(afterEscrow);
        pendingBalanceUntilMillis.set(System.currentTimeMillis() + 10_000);
        pendingRefundFloor.set(-1);
        usedSlots.updateAndGet(value -> packSlots(Math.min(20, unpackOrder(value) + 1), unpackAuction(value)));
        balanceSource.set("local pending order");
        requirePositionPersistence();
        enqueueDiagnostic("order_workflow", "submitted", 0, Map.of("candidate_state", candidate.state(),
                "route", candidate.route(), "reason_code", "explicit_local_arm"));
    }

    void recordOrderVerified(OrderPlan plan, int deliveredQuantity) {
        if (plan == null) return;
        orderPositions.computeIfPresent(plan.itemId(), (ignored, position) -> position.verified(deliveredQuantity, Instant.now()));
        requirePositionPersistence();
    }

    void observeOrderMessage(String raw) {
        String text = raw == null ? "" : raw.replaceAll("§[0-9A-FK-ORa-fk-or]", "")
                .replace('\r', ' ').replace('\n', ' ').strip();
        Matcher complete = COMPLETE_ORDER.matcher(text);
        if (complete.matches()) {
            matchingPosition(complete.group(1)).ifPresent(position -> {
                orderPositions.computeIfPresent(position.itemId(), (ignored, current) -> current.completed(Instant.now()));
                persistAndAllocate();
                enqueueDiagnostic("order_position", "complete", 0, Map.of("item_id", position.itemId(),
                        "route", "ORDER_TO_AUCTION", "reason_code", "server_completion_message"));
            });
            return;
        }
        Matcher delivery = ITEM_DELIVERY.matcher(text);
        if (!delivery.matches()) return;
        try {
            int quantity = Integer.parseInt(delivery.group(1).replace(",", ""));
            if (quantity <= 0) return;
            matchingPosition(delivery.group(2)).ifPresent(position -> {
                int delivered = Math.min(position.totalQuantity(), Math.addExact(position.deliveredQuantity(), quantity));
                orderPositions.computeIfPresent(position.itemId(), (ignored, current) -> current.verified(delivered, Instant.now()));
                persistAndAllocate();
            });
        } catch (ArithmeticException | NumberFormatException ignored) { }
    }

    static boolean isCompleteOrderMessage(String text, String expectedItemName) {
        Matcher matcher = COMPLETE_ORDER.matcher(text == null ? "" : text.strip());
        return matcher.matches() && OrderPlan.equivalentItemLabel(matcher.group(1), expectedItemName, expectedItemName);
    }

    private Optional<LocalOrderPosition> matchingPosition(String itemLabel) {
        List<LocalOrderPosition> matches = orderPositions.values().stream()
                .filter(position -> OrderPlan.equivalentItemLabel(itemLabel, position.itemName(), position.itemName())).toList();
        return matches.size() == 1 ? Optional.of(matches.getFirst()) : Optional.empty();
    }

    void focus(Candidate candidate) {
        scheduler.execute(() -> {
            try {
                JsonObject body = new JsonObject(); body.addProperty("signature", candidate.signature());
                sendJson("/api/v1/watches", "POST", body.toString());
                enqueueDiagnostic("decision", "focus", 0, Map.of("candidate_state", candidate.state(), "route", candidate.route(), "reason_code", "user_focus"));
            } catch (Exception error) { LOGGER.warn("Could not start focused watch: {}", safeMessage(error)); }
        });
    }

    String primaryCommand(Candidate candidate) { return candidate.route().equals("AUCTION_TO_ORDER") ? candidate.auctionCommand() : candidate.orderCommand(); }

    void pollNow() throws Exception {
        HttpRequest.Builder builder = request(config.backend().resolve("/api/v1/candidates")).GET();
        if (!etag.isEmpty()) builder.header("If-None-Match", etag);
        HttpResponse<InputStream> response = http.send(builder.build(), HttpResponse.BodyHandlers.ofInputStream());
        try (InputStream body = response.body()) {
            if (response.statusCode() == 304) {
                status.updateAndGet(previous -> connectedNotModified(previous, Instant.now()));
                return;
            }
            byte[] encoded = body.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (encoded.length > MAX_RESPONSE_BYTES) throw new IllegalStateException("candidate feed exceeds 1 MiB");
            if (response.statusCode() != 200) throw new IllegalStateException("backend returned HTTP " + response.statusCode());
            DecodedFeed feed = decode(encoded); candidates.set(feed.candidates()); generatedAt.set(feed.generatedAt()); etag = response.headers().firstValue("ETag").orElse("");
            PortfolioAllocator.Allocation next = allocator.allocate(feed.candidates(), balance(), usedOrderSlots(),
                    usedAuctionSlots(), duplicateLockedItemIds());
            allocation.set(next); status.set(new Status("ready", Instant.now(), "connected", feed.version(), feed.candidates().size()));
            emitNew(next);
        }
    }

    private void emitNew(PortfolioAllocator.Allocation value) {
        for (PortfolioAllocator.Selection selection : value.selections()) {
            // Alert once when an item enters the READY portfolio. Quantity is
            // shown from this allocation, but later one-stack fluctuations do
            // not spam chat; the live screen continues updating them.
            Candidate candidate = selection.candidate(); String key = candidate.id() + ":" + candidate.state();
            // READY portfolio updates may contain all 20 slots at once. Alerting
            // does not create 20 long-lived manual watches: a focused task starts
            // only when the player opens or arms that candidate. This preserves
            // discovery breadth and avoids reconnect pressure on the sole observer.
            if (!seen.containsKey(key)) { seen.put(key, true); alertSink.accept(selection); }
        }
        while (seen.size() > 4096) seen.remove(seen.keySet().iterator().next());
    }

    private void pollSafely() {
        try {
            pollNow();
            RepeatedFailureLimiter.Recovery recovery = pollFailureLimiter.recover();
            if (recovery.recovered()) LOGGER.info("Candidate feed recovered after {} suppressed repeat failures", recovery.suppressed());
        }
        catch (InterruptedException error) { Thread.currentThread().interrupt(); }
        catch (Exception error) {
            String message = safeMessage(error);
            status.set(new Status("error", status.get().lastSuccess(), message, status.get().version(), candidates.get().size()));
            RepeatedFailureLimiter.Decision decision = pollFailureLimiter.record(message, System.nanoTime());
            if (decision.emit()) {
                enqueueDiagnostic("error", "candidate_poll", 0, Map.of("exception_class", error.getClass().getSimpleName(), "endpoint", "/api/v1/candidates"));
                if (decision.suppressed() > 0) LOGGER.warn("Candidate feed refresh failed: {} ({} identical failures suppressed)", message, decision.suppressed());
                else LOGGER.warn("Candidate feed refresh failed: {}", message);
            }
        }
    }

    void pollShulkerSuppliesNow() throws Exception {
        HttpResponse<InputStream> response = http.send(request(config.backend().resolve("/api/v1/supplies/shulker-boxes")).GET().build(),
                HttpResponse.BodyHandlers.ofInputStream());
        try (InputStream body = response.body()) {
            byte[] encoded = body.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (encoded.length > MAX_RESPONSE_BYTES) throw new IllegalStateException("shulker supply feed exceeds 1 MiB");
            if (response.statusCode() != 200) throw new IllegalStateException("backend returned HTTP " + response.statusCode());
            shulkerSupplies.set(decodeShulkerSupplies(encoded, Instant.now()));
            shulkerSupplySuccess.set(Instant.now());
        }
    }

    static List<ShulkerSupply> decodeShulkerSupplies(byte[] encoded, Instant now) {
        JsonObject root = JsonParser.parseString(new String(encoded, StandardCharsets.UTF_8)).getAsJsonObject();
        Instant generated = instant(root, "generated_at");
        if (generated.isAfter(now.plusSeconds(5)) || generated.isBefore(now.minusSeconds(20))) {
            throw new IllegalArgumentException("stale shulker supply response");
        }
        JsonArray raw = root.getAsJsonArray("supplies");
        if (raw == null || raw.size() > 20) throw new IllegalArgumentException("invalid shulker supply count");
        List<ShulkerSupply> decoded = new ArrayList<>(raw.size());
        Set<String> identities = new java.util.HashSet<>();
        long previousPrice = -1;
        for (JsonElement element : raw) {
            JsonObject value = element.getAsJsonObject();
            String itemId = required(value, "item_id", 128).toLowerCase(Locale.ROOT);
            String seller = required(value, "seller", 16);
            String auctionId = optional(value, "auction_id", 128);
            long price = boundedLong(value, "price", 1, Long.MAX_VALUE);
            Instant lastSeen = instant(value, "last_seen");
            Instant expires = value.has("expires_at") && !value.get("expires_at").getAsString().isBlank()
                    ? instant(value, "expires_at") : Instant.EPOCH;
            String identity = auctionId.isBlank() ? seller + ':' + price + ':' + lastSeen : auctionId;
            if (!itemId.equals("minecraft:shulker_box") || !seller.matches("[A-Za-z0-9_]{1,16}")
                    || (previousPrice >= 0 && price < previousPrice) || !identities.add(identity)
                    || lastSeen.isAfter(now.plusSeconds(5)) || lastSeen.isBefore(now.minusSeconds(20))
                    || (!expires.equals(Instant.EPOCH) && !expires.isAfter(now))) {
                throw new IllegalArgumentException("unsafe, stale, duplicated, or unsorted shulker supply");
            }
            previousPrice = price;
            decoded.add(new ShulkerSupply(auctionId, seller, itemId, price, lastSeen, expires));
        }
        return List.copyOf(decoded);
    }

    private void pollShulkerSuppliesSafely() {
        try {
            pollShulkerSuppliesNow();
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
        } catch (Exception error) {
            LOGGER.debug("Shulker supply refresh failed: {}", safeMessage(error));
        }
    }

    private boolean persistAndAllocate() {
        Set<String> liveOrders = Set.copyOf(activeOrderItems);
        boolean saved = positionStore.save(orderPositions.values());
        ClientConfig.saveLocalState(balance(), usedOrderSlots(), usedAuctionSlots(), liveOrders);
        allocation.set(allocator.allocate(candidates(), balance(), usedOrderSlots(), usedAuctionSlots(), duplicateLockedItemIds()));
        return saved;
    }

    private Set<String> duplicateLockedItemIds() {
        java.util.LinkedHashSet<String> locked = new java.util.LinkedHashSet<>(activeOrderItems);
        locked.addAll(orderPositions.keySet());
        return Set.copyOf(locked);
    }

    static Set<String> liveOrderLocks(Set<String> configuredItems, Set<String> durablePositionItems) {
        if (configuredItems == null || durablePositionItems == null) {
            throw new IllegalArgumentException("order lock sets are required");
        }
        java.util.LinkedHashSet<String> live = new java.util.LinkedHashSet<>(configuredItems);
        live.removeAll(durablePositionItems);
        return Set.copyOf(live);
    }

    static Set<String> validatedPersonalOrderSnapshot(Set<String> observedItemIds, int occupiedSlots) {
        if (observedItemIds == null || occupiedSlots < 0 || occupiedSlots > 20
                || observedItemIds.size() > occupiedSlots
                || observedItemIds.stream().anyMatch(value -> value == null || !ITEM_ID.matcher(value).matches())) {
            throw new IllegalArgumentException("invalid personal-order reconciliation");
        }
        return Set.copyOf(observedItemIds);
    }

    private void requirePositionPersistence() {
        if (!persistAndAllocate()) throw new IllegalStateException("local order position could not be persisted");
    }

    private void enqueueDiagnostic(String event, String code, long duration, Map<String, String> fields) {
        if (!diagnostics.get() || diagnosticQueue.size() >= 100) return;
        JsonObject value = new JsonObject(); value.addProperty("install_id", config.installId()); value.addProperty("version", MOD_VERSION);
        value.addProperty("event", event); if (!code.isBlank()) value.addProperty("code", code); if (duration > 0) value.addProperty("duration_ms", duration);
        JsonObject encodedFields = new JsonObject(); fields.forEach(encodedFields::addProperty); value.add("fields", encodedFields); value.addProperty("created_at", Instant.now().toString());
        diagnosticQueue.add(value);
    }

    private void flushDiagnosticsSafely() {
        if (!diagnostics.get() || diagnosticQueue.isEmpty()) return;
        JsonArray batch = new JsonArray(); List<JsonObject> removed = new ArrayList<>();
        while (batch.size() < 50) { JsonObject value = diagnosticQueue.poll(); if (value == null) break; batch.add(value); removed.add(value); }
        try { sendJson("/api/v1/client/diagnostics", "POST", batch.toString()); }
        catch (Exception error) { for (JsonObject value : removed) if (diagnosticQueue.size() < 100) diagnosticQueue.add(value); }
    }

    private void sendJson(String path, String method, String body) throws Exception {
        HttpRequest request = request(config.backend().resolve(path)).header("Content-Type", "application/json")
                .method(method, HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8)).build();
        HttpResponse<InputStream> response = http.send(request, HttpResponse.BodyHandlers.ofInputStream());
        try (InputStream ignored = response.body()) { if (response.statusCode() < 200 || response.statusCode() >= 300) throw new IllegalStateException("backend returned HTTP " + response.statusCode()); }
    }

    private HttpRequest.Builder request(URI endpoint) {
        HttpRequest.Builder builder = HttpRequest.newBuilder(endpoint).timeout(Duration.ofSeconds(25)).header("Accept", "application/json");
        if (!config.token().isEmpty()) builder.header("Authorization", "Bearer " + config.token());
        return builder;
    }

    static DecodedFeed decode(byte[] encoded) {
        JsonObject root = JsonParser.parseString(new String(encoded, StandardCharsets.UTF_8)).getAsJsonObject();
        long version = boundedLong(root, "version", 0, Long.MAX_VALUE); Instant generatedAt = Instant.parse(required(root, "generated_at", 64));
        JsonArray raw = root.getAsJsonArray("candidates"); if (raw == null || raw.size() > MAX_CANDIDATES) throw new IllegalArgumentException("invalid candidate count");
        List<Candidate> result = new ArrayList<>();
        for (JsonElement element : raw) {
            JsonObject value = element.getAsJsonObject(); String itemId = required(value, "item_id", 128).toLowerCase(Locale.ROOT);
            if (!ITEM_ID.matcher(itemId).matches()) throw new IllegalArgumentException("invalid item id");
            String orderCommand = required(value, "order_command", 64); String auctionCommand = required(value, "auction_command", 68);
            if (!orderCommand.equals("/orders") || !AUCTION_COMMAND.matcher(auctionCommand).matches()) throw new IllegalArgumentException("unsafe candidate command");
            String route = oneOf(value, "route", "ORDER_TO_AUCTION", "AUCTION_TO_ORDER");
            int quantity = boundedInt(value, "quantity", 1, 64), maxStackSize = boundedInt(value, "max_stack_size", 1, 64);
            int orderSlots = boundedInt(value, "order_slots", 0, 20), auctionSlots = boundedInt(value, "auction_slots", 0, 18);
            long acquisitionCost = boundedLong(value, "acquisition_cost", 1, Long.MAX_VALUE);
            long observedOrderUnitRewardCents = boundedLong(value, "observed_order_unit_reward_cents", 0, Long.MAX_VALUE);
            long orderUnitRewardCents = boundedLong(value, "order_unit_reward_cents", 0, Long.MAX_VALUE);
            if (quantity > maxStackSize) throw new IllegalArgumentException("candidate exit quantity exceeds its maximum stack size");
            if ((route.equals("ORDER_TO_AUCTION") && (orderSlots != 1 || auctionSlots != 1))
                    || (route.equals("AUCTION_TO_ORDER") && (orderSlots != 0 || auctionSlots != 0))) {
                throw new IllegalArgumentException("candidate has invalid route slot semantics");
            }
            if (route.equals("ORDER_TO_AUCTION") && acquisitionCost != orderEscrow(orderUnitRewardCents, quantity)) {
                throw new IllegalArgumentException("candidate acquisition cost does not match its exact order quantity");
            }
            if (route.equals("ORDER_TO_AUCTION") && (observedOrderUnitRewardCents <= 0
                    || observedOrderUnitRewardCents >= orderUnitRewardCents)) {
                throw new IllegalArgumentException("candidate order price bucket is invalid");
            }
            result.add(new Candidate(required(value, "id", 128), route,
                    oneOf(value, "state", "READY", "RESEARCH", "CAPTURED", "HOLD", "STALE", "REJECTED"), optional(value, "reason", 200),
                    required(value, "signature", 2048), itemId, required(value, "item_name", 128), quantity,
                    maxStackSize, acquisitionCost,
                    boundedLong(value, "expected_proceeds", 0, Long.MAX_VALUE), observedOrderUnitRewardCents, orderUnitRewardCents,
                    boundedLong(value, "target_list_price", 0, Long.MAX_VALUE), boundedLong(value, "conservative_profit", Long.MIN_VALUE, Long.MAX_VALUE),
                    boundedInt(value, "margin_bps", 0, Integer.MAX_VALUE), boundedInt(value, "completion_bps", 0, 10_000),
                    boundedInt(value, "expected_cycle_minutes", 1, 1_000_000), boundedLong(value, "risk_adjusted_profit_day", 0, Long.MAX_VALUE),
                    boundedInt(value, "executable_batches", 0, 1_000_000), boundedInt(value, "queue_position", 0, 1_000_000), orderSlots, auctionSlots,
                    boundedInt(value, "inventory_slots", 1, 1728), boundedLong(value, "profit_per_inventory_slot", Long.MIN_VALUE, Long.MAX_VALUE),
                    boundedInt(value, "confidence_bps", 0, 10_000), required(value, "order_tier", 32), instant(value, "order_fresh_at"),
                    instant(value, "focused_fresh_at"), instant(value, "auction_fresh_at"), orderCommand, auctionCommand));
        }
        return new DecodedFeed(version, generatedAt, List.copyOf(result));
    }

    static Status connectedNotModified(Status previous, Instant now) {
        return new Status("ready", now, "connected", previous.version(), previous.candidateCount());
    }

    static long reconciledCancellationBalance(long current, long preSubmit) {
        if (current < 0 || preSubmit < 0) throw new IllegalArgumentException("balance cannot be negative");
        return Math.max(current, preSubmit);
    }

    private static long orderEscrow(long unitRewardCents, int quantity) {
        try {
            long wholeDollars = Math.multiplyExact(unitRewardCents / 100, quantity);
            long remainderCents = Math.multiplyExact(unitRewardCents % 100, quantity);
            return Math.addExact(wholeDollars, (remainderCents + 99) / 100);
        } catch (ArithmeticException error) {
            throw new IllegalArgumentException("candidate order escrow overflows", error);
        }
    }

    private static String oneOf(JsonObject value, String field, String... allowed) { String result = required(value, field, 32); for (String candidate : allowed) if (candidate.equals(result)) return result; throw new IllegalArgumentException("invalid " + field); }
    private static Instant instant(JsonObject value, String field) { return Instant.parse(required(value, field, 64)); }
    private static String required(JsonObject value, String field, int limit) { String result = optional(value, field, limit); if (result.isBlank()) throw new IllegalArgumentException(field + " is blank"); return result; }
    private static String optional(JsonObject value, String field, int limit) { String result = value.has(field) ? value.get(field).getAsString().strip() : ""; if (result.length() > limit || result.indexOf('\r') >= 0 || result.indexOf('\n') >= 0) throw new IllegalArgumentException("invalid " + field); return result; }
    private static long boundedLong(JsonObject value, String field, long minimum, long maximum) { long result = value.get(field).getAsLong(); if (result < minimum || result > maximum) throw new IllegalArgumentException("invalid " + field); return result; }
    private static int boundedInt(JsonObject value, String field, int minimum, int maximum) { return (int) boundedLong(value, field, minimum, maximum); }
    private static String safeMessage(Exception error) { String value = error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage(); value = value.replace('\r', ' ').replace('\n', ' ').strip(); return value.substring(0, Math.min(200, value.length())); }
    private static long packSlots(int orders, int auctions) { return ((long) orders << 32) | (auctions & 0xffffffffL); }
    private static int unpackOrder(long value) { return (int) (value >>> 32); }
    private static int unpackAuction(long value) { return (int) value; }

    @Override public void close() { enqueueDiagnostic("shutdown", "", 0, Map.of("state", "stopped")); flushDiagnosticsSafely(); scheduler.shutdownNow(); }
}
