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
    private static final Pattern BALANCE = Pattern.compile("(?i)\\b(?:balance|money|cash)\\b[^$0-9]{0,20}\\$?([0-9][0-9,]*)");
    private static final String MOD_VERSION = FabricLoader.getInstance().getModContainer("donut-network-client")
            .map(container -> container.getMetadata().getVersion().getFriendlyString()).orElse("unknown");

    record Candidate(String id, String route, String state, String reason, String signature, String itemId,
                     String itemName, int quantity, int maxStackSize, long acquisitionCost, long expectedProceeds,
                     long orderUnitRewardCents, long targetListPrice,
                     long conservativeProfit, int marginBps, int completionBps, int expectedCycleMinutes,
                     long riskAdjustedProfitDay, int executableBatches, int queuePosition, int orderSlots, int auctionSlots,
                     int inventorySlots, long profitInventorySlot, int confidenceBps, String orderTier,
                     Instant orderFreshAt, Instant focusedFreshAt, Instant auctionFreshAt, String orderCommand, String auctionCommand) {}
    record Status(String state, Instant lastSuccess, String message, long version, int candidateCount) {}
    record DecodedFeed(long version, Instant generatedAt, List<Candidate> candidates) {}

    private final ClientConfig.Settings config;
    private final Consumer<PortfolioAllocator.Selection> alertSink;
    private final HttpClient http;
    private final ScheduledExecutorService scheduler;
    private final AtomicReference<List<Candidate>> candidates = new AtomicReference<>(List.of());
    private final AtomicReference<PortfolioAllocator.Allocation> allocation;
    private final AtomicReference<Status> status = new AtomicReference<>(new Status("waiting", Instant.EPOCH, "not started", 0, 0));
    private final AtomicReference<Instant> generatedAt = new AtomicReference<>(Instant.EPOCH);
    private final AtomicLong balance;
    private final AtomicReference<String> balanceSource = new AtomicReference<>("saved/manual");
    private final AtomicLong usedSlots;
    private final Set<String> activeOrderItems = ConcurrentHashMap.newKeySet();
    private final AtomicBoolean diagnostics;
    private final ConcurrentLinkedQueue<JsonObject> diagnosticQueue = new ConcurrentLinkedQueue<>();
    private final LinkedHashMap<String, Boolean> seen = new LinkedHashMap<>();
    private final PortfolioAllocator allocator = new PortfolioAllocator();
    private String etag = "";

    CandidateFeedClient(ClientConfig.Settings config, Consumer<PortfolioAllocator.Selection> alertSink) {
        this(config, alertSink, HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build());
    }

    CandidateFeedClient(ClientConfig.Settings config, Consumer<PortfolioAllocator.Selection> alertSink, HttpClient http) {
        this.config = Objects.requireNonNull(config); this.alertSink = Objects.requireNonNull(alertSink); this.http = Objects.requireNonNull(http);
        balance = new AtomicLong(config.balance());
        activeOrderItems.addAll(config.activeOrderItems());
        usedSlots = new AtomicLong(packSlots(Math.max(config.usedOrderSlots(), activeOrderItems.size()), config.usedAuctionSlots()));
        diagnostics = new AtomicBoolean(config.diagnostics());
        allocation = new AtomicReference<>(allocator.allocate(List.of(), config.balance(),
                Math.max(config.usedOrderSlots(), activeOrderItems.size()), config.usedAuctionSlots(), activeOrderItems));
        scheduler = Executors.newSingleThreadScheduledExecutor(runnable -> { Thread thread = new Thread(runnable, "donut-candidate-feed"); thread.setDaemon(true); return thread; });
    }

    void start() {
        enqueueDiagnostic("startup", "", 0, Map.of("state", "started"));
        scheduler.scheduleWithFixedDelay(this::pollSafely, 0, config.pollInterval().toMillis(), TimeUnit.MILLISECONDS);
        scheduler.scheduleWithFixedDelay(this::flushDiagnosticsSafely, 5, 5, TimeUnit.SECONDS);
    }

    Status status() { return status.get(); }
    List<Candidate> candidates() { return candidates.get(); }
    PortfolioAllocator.Allocation allocation() { return allocation.get(); }
    long balance() { return balance.get(); }
    String balanceSource() { return balanceSource.get(); }
    int usedOrderSlots() { return unpackOrder(usedSlots.get()); }
    int usedAuctionSlots() { return unpackAuction(usedSlots.get()); }
    boolean diagnosticsEnabled() { return diagnostics.get(); }
    Instant generatedAt() { return generatedAt.get(); }
    long orderSessionBudget() { return config.orderSessionBudget(); }
    Set<String> orderServerHosts() { return config.orderServerHosts(); }

    Optional<Candidate> candidate(String id) {
        return candidates.get().stream().filter(value -> value.id().equals(id)).findFirst();
    }

    boolean isAllocated(String id) {
        return allocation.get().selections().stream().anyMatch(selection -> selection.candidate().id().equals(id));
    }

    int allocatedBatches(String id) {
        return allocation.get().selections().stream().filter(selection -> selection.candidate().id().equals(id))
                .mapToInt(PortfolioAllocator.Selection::batches).findFirst().orElse(0);
    }

    boolean hasActiveOrder(String itemId) { return activeOrderItems.contains(itemId); }
    int activeOrderCount() { return activeOrderItems.size(); }

    void markActiveOrder(String itemId) {
        if (itemId != null && ITEM_ID.matcher(itemId).matches() && activeOrderItems.add(itemId)) {
            usedSlots.updateAndGet(value -> packSlots(Math.max(unpackOrder(value), activeOrderItems.size()), unpackAuction(value)));
            persistAndAllocate();
        }
    }

    void recheckTrackedOrders() {
        // Clearing only re-admits candidates to the arm screen. Every arm still
        // opens the verified personal-order menu and blocks an exact item match
        // before any transactional control is used.
        activeOrderItems.clear();
        persistAndAllocate();
        // Previously selected items receive an immediate backend focused watch.
        // The server recognizes proven market profiles and uses its shorter
        // revalidation lane; the exact Your Orders duplicate check still runs
        // before any creation action.
        for (PortfolioAllocator.Selection selection : allocation.get().selections()) focus(selection.candidate());
    }

    void adjustBalance(long delta) {
        balance.updateAndGet(value -> delta > 0 && value > Long.MAX_VALUE - delta ? Long.MAX_VALUE : Math.max(0, value + delta));
        balanceSource.set("manual");
        persistAndAllocate();
    }

    void observeBalance(String message) {
        Matcher matcher = BALANCE.matcher(message == null ? "" : message.replace("§", ""));
        if (!matcher.find()) return;
        try {
            long observed = Long.parseLong(matcher.group(1).replace(",", ""));
            balance.set(observed); balanceSource.set("labeled chat"); persistAndAllocate();
        } catch (NumberFormatException ignored) { }
    }

    void adjustUsedSlots(int orderDelta, int auctionDelta) {
        usedSlots.updateAndGet(value -> packSlots(Math.max(0, Math.min(20, unpackOrder(value) + orderDelta)),
                Math.max(0, Math.min(18, unpackAuction(value) + auctionDelta))));
        persistAndAllocate();
    }

    void setDiagnostics(boolean enabled) { diagnostics.set(enabled); ClientConfig.saveDiagnostics(enabled); }

    void diagnostic(String event, String code, Map<String, String> fields) {
        enqueueDiagnostic(event, code, 0, fields);
    }

    void recordOrderSubmitted(Candidate candidate, OrderPlan plan) {
        balance.updateAndGet(value -> Math.max(0, value - plan.escrowDollars()));
        usedSlots.updateAndGet(value -> packSlots(Math.min(20, unpackOrder(value) + 1), unpackAuction(value)));
        activeOrderItems.add(candidate.itemId());
        balanceSource.set("local pending order");
        persistAndAllocate();
        enqueueDiagnostic("order_workflow", "submitted", 0, Map.of("candidate_state", candidate.state(),
                "route", candidate.route(), "reason_code", "explicit_local_arm"));
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
            if (response.statusCode() == 304) { status.updateAndGet(previous -> new Status(previous.state(), Instant.now(), "connected", previous.version(), previous.candidateCount())); return; }
            byte[] encoded = body.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (encoded.length > MAX_RESPONSE_BYTES) throw new IllegalStateException("candidate feed exceeds 1 MiB");
            if (response.statusCode() != 200) throw new IllegalStateException("backend returned HTTP " + response.statusCode());
            DecodedFeed feed = decode(encoded); candidates.set(feed.candidates()); generatedAt.set(feed.generatedAt()); etag = response.headers().firstValue("ETag").orElse("");
            PortfolioAllocator.Allocation next = allocator.allocate(feed.candidates(), balance(), usedOrderSlots(), usedAuctionSlots(), activeOrderItems);
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
            if (!seen.containsKey(key)) { seen.put(key, true); focus(candidate); alertSink.accept(selection); }
        }
        while (seen.size() > 4096) seen.remove(seen.keySet().iterator().next());
    }

    private void pollSafely() {
        try { pollNow(); }
        catch (InterruptedException error) { Thread.currentThread().interrupt(); }
        catch (Exception error) {
            status.set(new Status("error", status.get().lastSuccess(), safeMessage(error), status.get().version(), candidates.get().size()));
            enqueueDiagnostic("error", "candidate_poll", 0, Map.of("exception_class", error.getClass().getSimpleName(), "endpoint", "/api/v1/candidates"));
            LOGGER.warn("Candidate feed refresh failed: {}", safeMessage(error));
        }
    }

    private void persistAndAllocate() {
        ClientConfig.saveLocalState(balance(), usedOrderSlots(), usedAuctionSlots(), Set.copyOf(activeOrderItems));
        allocation.set(allocator.allocate(candidates(), balance(), usedOrderSlots(), usedAuctionSlots(), activeOrderItems));
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
            if (quantity != maxStackSize) throw new IllegalArgumentException("candidate is not one exact maximum-stack exit");
            if ((route.equals("ORDER_TO_AUCTION") && (orderSlots != 1 || auctionSlots != 1))
                    || (route.equals("AUCTION_TO_ORDER") && (orderSlots != 0 || auctionSlots != 0))) {
                throw new IllegalArgumentException("candidate has invalid route slot semantics");
            }
            result.add(new Candidate(required(value, "id", 128), route,
                    oneOf(value, "state", "READY", "RESEARCH", "CAPTURED", "HOLD", "STALE", "REJECTED"), optional(value, "reason", 200),
                    required(value, "signature", 2048), itemId, required(value, "item_name", 128), quantity,
                    maxStackSize, boundedLong(value, "acquisition_cost", 1, Long.MAX_VALUE),
                    boundedLong(value, "expected_proceeds", 0, Long.MAX_VALUE), boundedLong(value, "order_unit_reward_cents", 0, Long.MAX_VALUE),
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
