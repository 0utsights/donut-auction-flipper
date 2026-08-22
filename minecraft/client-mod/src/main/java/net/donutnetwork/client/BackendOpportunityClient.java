package net.donutnetwork.client;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

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
import java.util.Objects;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Consumer;

/** Polls the backend-ranked flip feed and emits each listing at most once per game session. */
public final class BackendOpportunityClient implements AutoCloseable {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final int MAX_RESPONSE_BYTES = 2 << 20;
    private static final int MAX_FEED_ITEMS = 25;
    private static final int MAX_ALERTS_PER_POLL = 5;
    private static final int MAX_SEEN = 4096;

    public record Opportunity(String key, String authoritativeId, String fingerprint, String seller,
                              String itemId, int quantity, long price, long referenceValue,
                              long profit, int marginBps, int confidenceBps, int volume24h,
                              Instant expiresAt) {
        public String itemName() {
            String path = itemId.startsWith("minecraft:") ? itemId.substring("minecraft:".length()) : itemId;
            return path.replace('_', ' ');
        }

        public String auctionCommand() {
            String path = itemId.startsWith("minecraft:") ? itemId.substring("minecraft:".length()) : itemId;
            if (!path.matches("[a-z0-9_]{1,80}")) {
                return "ah";
            }
            return "ah " + path.replace('_', ' ');
        }
    }

    public record Status(String state, Instant lastAttempt, Instant lastSuccess, String message,
                         long feedVersion, int opportunities) {}

    private final BackendSnapshotClient.Config config;
    private final Duration interval;
    private final Consumer<Opportunity> alertSink;
    private final HttpClient http;
    private final ScheduledExecutorService scheduler;
    private final AtomicBoolean alertsEnabled;
    private final AtomicReference<List<Opportunity>> opportunities = new AtomicReference<>(List.of());
    private final AtomicReference<Status> status = new AtomicReference<>(
            new Status("waiting", Instant.EPOCH, Instant.EPOCH, "not started", 0, 0));
    private final LinkedHashMap<String, Boolean> seen = new LinkedHashMap<>();
    private String etag = "";

    public BackendOpportunityClient(BackendSnapshotClient.Config config, Duration interval,
                                    boolean alertsEnabled, Consumer<Opportunity> alertSink) {
        this(config, interval, alertsEnabled, alertSink,
                HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build());
    }

    BackendOpportunityClient(BackendSnapshotClient.Config config, Duration interval,
                             boolean alertsEnabled, Consumer<Opportunity> alertSink, HttpClient http) {
        this.config = Objects.requireNonNull(config, "config");
        this.interval = Objects.requireNonNull(interval, "interval");
        this.alertSink = Objects.requireNonNull(alertSink, "alertSink");
        this.http = Objects.requireNonNull(http, "http");
        if (interval.compareTo(Duration.ofSeconds(2)) < 0 || interval.compareTo(Duration.ofMinutes(1)) > 0) {
            throw new IllegalArgumentException("alert interval must be between two seconds and one minute");
        }
        this.alertsEnabled = new AtomicBoolean(alertsEnabled);
        this.scheduler = Executors.newSingleThreadScheduledExecutor(runnable -> {
            Thread thread = new Thread(runnable, "donut-network-opportunities");
            thread.setDaemon(true);
            return thread;
        });
    }

    public void start() {
        scheduler.scheduleWithFixedDelay(this::pollSafely, 0, interval.toMillis(), TimeUnit.MILLISECONDS);
    }

    public Status status() {
        return status.get();
    }

    public List<Opportunity> opportunities() {
        return opportunities.get();
    }

    public boolean alertsEnabled() {
        return alertsEnabled.get();
    }

    public void setAlertsEnabled(boolean enabled) {
        alertsEnabled.set(enabled);
        ClientConfiguration.saveChatAlerts(enabled);
    }

    void pollNow() throws Exception {
        Instant attempted = Instant.now();
        URI endpoint = config.backend().resolve("/api/v1/opportunities");
        HttpRequest.Builder builder = HttpRequest.newBuilder(endpoint)
                .timeout(Duration.ofSeconds(10))
                .header("Authorization", "Bearer " + config.token())
                .header("Accept", "application/json")
                .header("X-Data-Mode", "live")
                .GET();
        if (!etag.isEmpty()) {
            builder.header("If-None-Match", etag);
        }
        HttpResponse<InputStream> response = http.send(builder.build(), HttpResponse.BodyHandlers.ofInputStream());
        try (InputStream body = response.body()) {
            if (response.statusCode() == 304) {
                Status previous = status.get();
                status.set(new Status(previous.state(), attempted, Instant.now(), previous.message(),
                        previous.feedVersion(), opportunities.get().size()));
                return;
            }
            byte[] encoded = body.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (encoded.length > MAX_RESPONSE_BYTES) {
                throw new IllegalStateException("backend opportunity feed exceeds 2 MiB");
            }
            if (response.statusCode() != 200) {
                throw new IllegalStateException("backend returned HTTP " + response.statusCode());
            }
            DecodedFeed feed = decode(encoded);
            opportunities.set(feed.opportunities());
            etag = response.headers().firstValue("ETag").orElse("");
            status.set(new Status(feed.state(), attempted, Instant.now(), feed.message(),
                    feed.version(), feed.opportunities().size()));
            if ("ready".equals(feed.state())) {
                emitNew(feed.opportunities());
            }
        }
    }

    private void emitNew(List<Opportunity> feed) {
        int emitted = 0;
        for (Opportunity opportunity : feed) {
            boolean isNew = !seen.containsKey(opportunity.key());
            seen.put(opportunity.key(), true);
            if (isNew && alertsEnabled.get() && emitted < MAX_ALERTS_PER_POLL) {
                alertSink.accept(opportunity);
                emitted++;
            }
        }
        while (seen.size() > MAX_SEEN) {
            seen.remove(seen.keySet().iterator().next());
        }
    }

    private void pollSafely() {
        try {
            pollNow();
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        } catch (Exception failure) {
            Status previous = status.get();
            status.set(new Status("error", Instant.now(), previous.lastSuccess(), safeMessage(failure),
                    previous.feedVersion(), opportunities.get().size()));
            LOGGER.warn("Backend opportunity refresh failed: {}", safeMessage(failure));
        }
    }

    record DecodedFeed(long version, String state, String message, List<Opportunity> opportunities) {}

    static DecodedFeed decode(byte[] encoded) {
        JsonObject root = JsonParser.parseString(new String(encoded, StandardCharsets.UTF_8)).getAsJsonObject();
        long version = nonNegativeLong(root, "version");
        String state = safeString(root, "state", 32);
        if (!"ready".equals(state) && !"stale".equals(state)) {
            throw new IllegalArgumentException("invalid opportunity feed state");
        }
        String message = optionalString(root, "message", 200);
        JsonArray raw = root.getAsJsonArray("opportunities");
        if (raw == null || raw.size() > MAX_FEED_ITEMS) {
            throw new IllegalArgumentException("invalid opportunity count");
        }
        List<Opportunity> decoded = new ArrayList<>(raw.size());
        for (JsonElement element : raw) {
            JsonObject opportunity = element.getAsJsonObject();
            String key = safeString(opportunity, "key", 384);
            String fingerprint = safeString(opportunity, "fingerprint", 256);
            String authoritativeId = optionalString(opportunity, "authoritative_id", 128);
            String itemId = safeString(opportunity, "item_id", 128).toLowerCase(Locale.ROOT);
            String seller = safeString(opportunity, "seller", 64);
            int quantity = boundedInt(opportunity, "quantity", 1, 64 * 27);
            long price = positiveLong(opportunity, "price");
            long reference = positiveLong(opportunity, "reference_value");
            long profit = positiveLong(opportunity, "profit");
            int margin = boundedInt(opportunity, "margin_bps", 0, Integer.MAX_VALUE);
            int confidence = boundedInt(opportunity, "confidence_bps", 0, 10_000);
            int volume = boundedInt(opportunity, "volume_24h", 0, Integer.MAX_VALUE);
            Instant expiresAt = opportunity.has("expires_at")
                    ? Instant.parse(opportunity.get("expires_at").getAsString()) : Instant.EPOCH;
            decoded.add(new Opportunity(key, authoritativeId, fingerprint, seller, itemId, quantity,
                    price, reference, profit, margin, confidence, volume, expiresAt));
        }
        if ("stale".equals(state) && !decoded.isEmpty()) {
            throw new IllegalArgumentException("stale feed must not contain opportunities");
        }
        return new DecodedFeed(version, state, message, List.copyOf(decoded));
    }

    private static long nonNegativeLong(JsonObject object, String field) {
        long value = object.get(field).getAsLong();
        if (value < 0) {
            throw new IllegalArgumentException(field + " must not be negative");
        }
        return value;
    }

    private static long positiveLong(JsonObject object, String field) {
        long value = object.get(field).getAsLong();
        if (value <= 0) {
            throw new IllegalArgumentException(field + " must be positive");
        }
        return value;
    }

    private static int boundedInt(JsonObject object, String field, int minimum, int maximum) {
        long value = object.get(field).getAsLong();
        if (value < minimum || value > maximum) {
            throw new IllegalArgumentException(field + " is outside its valid range");
        }
        return (int) value;
    }

    private static String safeString(JsonObject object, String field, int maximumLength) {
        String value = optionalString(object, field, maximumLength);
        if (value.isBlank()) {
            throw new IllegalArgumentException(field + " must not be blank");
        }
        return value;
    }

    private static String optionalString(JsonObject object, String field, int maximumLength) {
        String value = object.has(field) ? object.get(field).getAsString().strip() : "";
        if (value.length() > maximumLength || value.indexOf('\r') >= 0 || value.indexOf('\n') >= 0) {
            throw new IllegalArgumentException(field + " is invalid");
        }
        return value;
    }

    private static String safeMessage(Exception error) {
        String message = error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage();
        message = message.replace('\r', ' ').replace('\n', ' ').strip();
        return message.length() <= 200 ? message : message.substring(0, 200);
    }

    @Override
    public void close() {
        scheduler.shutdownNow();
    }
}
