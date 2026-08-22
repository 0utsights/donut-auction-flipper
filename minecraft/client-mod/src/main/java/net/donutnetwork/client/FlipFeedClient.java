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
import java.util.regex.Pattern;

/** The entire normal data path: one conditional HTTP request for backend-ranked flips. */
final class FlipFeedClient implements AutoCloseable {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final int MAX_RESPONSE_BYTES = 1 << 20;
    private static final int MAX_FLIPS = 100;
    private static final int MAX_ALERTS_PER_POLL = 5;
    private static final int MAX_SEEN = 4096;
    private static final Pattern SAFE_COMMAND = Pattern.compile("/ah [\\p{L}\\p{N} _-]{1,48}");

    record Flip(String key, String auctionId, String itemId, String itemName, int quantity, String seller,
                long price, long referenceValue, long profit, int marginBps, int confidenceBps,
                int volume24h, Instant expiresAt, String searchCommand, String modelVersion,
                int expectedSellMinutes) {}

    record Status(String state, Instant lastAttempt, Instant lastSuccess, String message,
                  long version, int flipCount) {}

    record DecodedFeed(long version, String state, List<Flip> flips) {}

    private final ClientConfig.Settings config;
    private final Consumer<Flip> alertSink;
    private final HttpClient http;
    private final ScheduledExecutorService scheduler;
    private final AtomicBoolean alertsEnabled;
    private final AtomicReference<List<Flip>> flips = new AtomicReference<>(List.of());
    private final AtomicReference<Status> status = new AtomicReference<>(
            new Status("waiting", Instant.EPOCH, Instant.EPOCH, "not started", 0, 0));
    private final LinkedHashMap<String, Boolean> seen = new LinkedHashMap<>();
    private String etag = "";

    FlipFeedClient(ClientConfig.Settings config, Consumer<Flip> alertSink) {
        this(config, alertSink, HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build());
    }

    FlipFeedClient(ClientConfig.Settings config, Consumer<Flip> alertSink, HttpClient http) {
        this.config = Objects.requireNonNull(config, "config");
        this.alertSink = Objects.requireNonNull(alertSink, "alertSink");
        this.http = Objects.requireNonNull(http, "http");
        this.alertsEnabled = new AtomicBoolean(config.chatAlerts());
        this.scheduler = Executors.newSingleThreadScheduledExecutor(runnable -> {
            Thread thread = new Thread(runnable, "donut-flip-feed");
            thread.setDaemon(true);
            return thread;
        });
    }

    void start() {
        scheduler.scheduleWithFixedDelay(this::pollSafely, 0, config.pollInterval().toMillis(), TimeUnit.MILLISECONDS);
    }

    Status status() { return status.get(); }
    List<Flip> flips() { return flips.get(); }
    boolean alertsEnabled() { return alertsEnabled.get(); }

    void setAlertsEnabled(boolean enabled) {
        alertsEnabled.set(enabled);
        ClientConfig.saveChatAlerts(enabled);
    }

    void pollNow() throws Exception {
        Instant attempted = Instant.now();
        URI endpoint = config.backend().resolve("/api/v1/flips");
        HttpRequest.Builder builder = HttpRequest.newBuilder(endpoint)
                .timeout(Duration.ofSeconds(10)).header("Accept", "application/json").GET();
        if (!config.token().isEmpty()) {
            builder.header("Authorization", "Bearer " + config.token());
        }
        if (!etag.isEmpty()) {
            builder.header("If-None-Match", etag);
        }
        HttpResponse<InputStream> response = http.send(builder.build(), HttpResponse.BodyHandlers.ofInputStream());
        try (InputStream body = response.body()) {
            if (response.statusCode() == 304) {
                Status previous = status.get();
                status.set(new Status(previous.state(), attempted, Instant.now(), previous.message(),
                        previous.version(), flips.get().size()));
                return;
            }
            byte[] encoded = body.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (encoded.length > MAX_RESPONSE_BYTES) {
                throw new IllegalStateException("flip feed exceeds 1 MiB");
            }
            if (response.statusCode() != 200) {
                throw new IllegalStateException("backend returned HTTP " + response.statusCode());
            }
            DecodedFeed feed = decode(encoded);
            flips.set(feed.flips());
            etag = response.headers().firstValue("ETag").orElse("");
            status.set(new Status(feed.state(), attempted, Instant.now(), "connected",
                    feed.version(), feed.flips().size()));
            if ("ready".equals(feed.state())) {
                emitNew(feed.flips());
            }
        }
    }

    private void emitNew(List<Flip> current) {
        int emitted = 0;
        for (Flip flip : current) {
            boolean fresh = !seen.containsKey(flip.key());
            seen.put(flip.key(), true);
            if (fresh && alertsEnabled.get() && emitted < MAX_ALERTS_PER_POLL) {
                alertSink.accept(flip);
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
                    previous.version(), flips.get().size()));
            LOGGER.warn("Flip feed refresh failed: {}", safeMessage(failure));
        }
    }

    static DecodedFeed decode(byte[] encoded) {
        JsonObject root = JsonParser.parseString(new String(encoded, StandardCharsets.UTF_8)).getAsJsonObject();
        long version = boundedLong(root, "version", 0, Long.MAX_VALUE);
        String state = safeString(root, "status", 32);
        if (!List.of("starting", "collecting", "ready", "error").contains(state)) {
            throw new IllegalArgumentException("invalid backend status");
        }
        JsonArray raw = root.getAsJsonArray("flips");
        if (raw == null || raw.size() > MAX_FLIPS) {
            throw new IllegalArgumentException("invalid flip count");
        }
        List<Flip> decoded = new ArrayList<>(raw.size());
        for (JsonElement element : raw) {
            JsonObject value = element.getAsJsonObject();
            String command = safeString(value, "search_command", 52);
            if (!SAFE_COMMAND.matcher(command).matches()) {
                throw new IllegalArgumentException("unsafe search_command");
            }
            String itemId = safeString(value, "item_id", 128).toLowerCase(Locale.ROOT);
            if (!itemId.matches("[a-z0-9_.-]+:[a-z0-9_./-]+")) {
                throw new IllegalArgumentException("invalid item_id");
            }
            Instant expiresAt = value.has("expires_at") && !value.get("expires_at").getAsString().isBlank()
                    ? Instant.parse(value.get("expires_at").getAsString()) : Instant.EPOCH;
            decoded.add(new Flip(
                    safeString(value, "key", 384), optionalString(value, "auction_id", 128), itemId,
                    safeString(value, "item_name", 128), boundedInt(value, "quantity", 1, 1728),
                    optionalString(value, "seller", 64), boundedLong(value, "price", 1, Long.MAX_VALUE),
                    boundedLong(value, "reference_value", 1, Long.MAX_VALUE),
                    boundedLong(value, "profit", 1, Long.MAX_VALUE), boundedInt(value, "margin_bps", 0, Integer.MAX_VALUE),
                    boundedInt(value, "confidence_bps", 0, 10_000), boundedInt(value, "volume_24h", 0, Integer.MAX_VALUE),
                    expiresAt, command, optionalString(value, "model_version", 64),
                    boundedInt(value, "expected_sell_minutes", 0, Integer.MAX_VALUE)));
        }
        return new DecodedFeed(version, state, List.copyOf(decoded));
    }

    static String commandWithoutSlash(Flip flip) {
        if (!SAFE_COMMAND.matcher(flip.searchCommand()).matches()) {
            throw new IllegalArgumentException("unsafe search command");
        }
        return flip.searchCommand().substring(1);
    }

    private static long boundedLong(JsonObject value, String field, long minimum, long maximum) {
        long number = value.get(field).getAsLong();
        if (number < minimum || number > maximum) {
            throw new IllegalArgumentException(field + " is outside its valid range");
        }
        return number;
    }

    private static int boundedInt(JsonObject value, String field, int minimum, int maximum) {
        long number = boundedLong(value, field, minimum, maximum);
        return (int) number;
    }

    private static String safeString(JsonObject value, String field, int maximumLength) {
        String result = optionalString(value, field, maximumLength);
        if (result.isBlank()) {
            throw new IllegalArgumentException(field + " must not be blank");
        }
        return result;
    }

    private static String optionalString(JsonObject value, String field, int maximumLength) {
        String result = value.has(field) ? value.get(field).getAsString().strip() : "";
        if (result.length() > maximumLength || result.indexOf('\r') >= 0 || result.indexOf('\n') >= 0) {
            throw new IllegalArgumentException(field + " is invalid");
        }
        return result;
    }

    private static String safeMessage(Exception error) {
        String message = error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage();
        message = message.replace('\r', ' ').replace('\n', ' ').strip();
        return message.length() <= 200 ? message : message.substring(0, 200);
    }

    @Override public void close() { scheduler.shutdownNow(); }
}
