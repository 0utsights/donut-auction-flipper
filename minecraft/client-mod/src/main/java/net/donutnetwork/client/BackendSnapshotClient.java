package net.donutnetwork.client;

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
import java.util.HashMap;
import java.util.Map;
import java.util.Objects;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;

/** Polls compact, precomputed backend values. The Donut API key never enters the mod. */
public final class BackendSnapshotClient implements AutoCloseable {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final int MAX_RESPONSE_BYTES = 8 << 20;

    public record Config(URI backend, String token, Duration interval) {
        public Config {
            Objects.requireNonNull(backend, "backend");
            Objects.requireNonNull(token, "token");
            Objects.requireNonNull(interval, "interval");
            if (!("http".equalsIgnoreCase(backend.getScheme()) || "https".equalsIgnoreCase(backend.getScheme()))) {
                throw new IllegalArgumentException("backend URL must use http or https");
            }
            if (backend.getUserInfo() != null || token.indexOf('\r') >= 0 || token.indexOf('\n') >= 0) {
                throw new IllegalArgumentException("invalid backend configuration");
            }
            if (interval.compareTo(Duration.ofSeconds(2)) < 0) {
                throw new IllegalArgumentException("poll interval must be at least two seconds");
            }
        }
    }

    public record Status(String state, Instant lastAttempt, Instant lastSuccess, String message,
                         long snapshotVersion, int values) {}

    private final PriceSnapshotCache cache;
    private final Config config;
    private final HttpClient http;
    private final ScheduledExecutorService scheduler;
    private final AtomicReference<Status> status = new AtomicReference<>(
            new Status("waiting", Instant.EPOCH, Instant.EPOCH, "not started", 0, 0));

    public BackendSnapshotClient(PriceSnapshotCache cache, Config config) {
        this(cache, config, HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build());
    }

    BackendSnapshotClient(PriceSnapshotCache cache, Config config, HttpClient http) {
        this.cache = Objects.requireNonNull(cache, "cache");
        this.config = Objects.requireNonNull(config, "config");
        this.http = Objects.requireNonNull(http, "http");
        this.scheduler = Executors.newSingleThreadScheduledExecutor(runnable -> {
            Thread thread = new Thread(runnable, "donut-network-snapshot");
            thread.setDaemon(true);
            return thread;
        });
    }

    public void start() {
        scheduler.scheduleWithFixedDelay(this::pollSafely, 0, config.interval().toMillis(), TimeUnit.MILLISECONDS);
    }

    public Status status() {
        return status.get();
    }

    void pollNow() throws Exception {
        Instant attempted = Instant.now();
        URI endpoint = config.backend().resolve("/api/v1/client-snapshot");
        HttpRequest request = HttpRequest.newBuilder(endpoint)
                .timeout(Duration.ofSeconds(10))
                .header("Authorization", "Bearer " + config.token())
                .header("Accept", "application/json")
                .header("X-Data-Mode", "live")
                .header("If-None-Match", "\"" + cache.version() + "\"")
                .GET().build();
        HttpResponse<InputStream> response = http.send(request, HttpResponse.BodyHandlers.ofInputStream());
        try (InputStream body = response.body()) {
            if (response.statusCode() == 304) {
                Status previous = status.get();
                status.set(new Status("ready", attempted, Instant.now(), "snapshot unchanged",
                        previous.snapshotVersion(), previous.values()));
                return;
            }
            byte[] encoded = body.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (encoded.length > MAX_RESPONSE_BYTES) {
                throw new IllegalStateException("backend snapshot exceeds 8 MiB");
            }
            if (response.statusCode() != 200) {
                throw new IllegalStateException("backend returned HTTP " + response.statusCode());
            }
            PriceSnapshotCache.Snapshot snapshot = decode(encoded);
            cache.replace(snapshot);
            status.set(new Status("ready", attempted, Instant.now(), "snapshot loaded",
                    snapshot.version(), snapshot.values().size()));
        }
    }

    private void pollSafely() {
        try {
            pollNow();
            Status current = status.get();
            LOGGER.debug("Backend valuation snapshot version={} values={}", current.snapshotVersion(), current.values());
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        } catch (Exception failure) {
            Status previous = status.get();
            status.set(new Status("error", Instant.now(), previous.lastSuccess(), safeMessage(failure),
                    previous.snapshotVersion(), previous.values()));
            LOGGER.warn("Backend valuation refresh failed: {}", safeMessage(failure));
        }
    }

    static PriceSnapshotCache.Snapshot decode(byte[] encoded) {
        JsonObject root = JsonParser.parseString(new String(encoded, StandardCharsets.UTF_8)).getAsJsonObject();
        long version = root.get("version").getAsLong();
        Instant generatedAt = Instant.parse(root.get("generated_at").getAsString());
        JsonObject rawValues = root.getAsJsonObject("values");
        Map<String, PriceSnapshotCache.Value> values = new HashMap<>(Math.max(16, rawValues.size() * 2));
        for (Map.Entry<String, JsonElement> entry : rawValues.entrySet()) {
            JsonObject value = entry.getValue().getAsJsonObject();
            long fair = positiveLong(value, "fair_value");
            long quick = positiveLong(value, "quick_sell_value");
            int confidence = boundedInt(value, "confidence_bps", 0, 10_000);
            int volume = boundedInt(value, "volume_24h", 0, Integer.MAX_VALUE);
            if (fair > 0 && quick > 0) {
                values.put(entry.getKey(), new PriceSnapshotCache.Value(fair, quick, confidence, volume));
            }
        }
        return new PriceSnapshotCache.Snapshot(version, generatedAt, values);
    }

    private static long positiveLong(JsonObject value, String field) {
        long parsed = value.get(field).getAsLong();
        if (parsed < 0) {
            throw new IllegalArgumentException(field + " must not be negative");
        }
        return parsed;
    }

    private static int boundedInt(JsonObject value, String field, int minimum, int maximum) {
        long parsed = value.get(field).getAsLong();
        if (parsed < minimum || parsed > maximum) {
            throw new IllegalArgumentException(field + " is outside its valid range");
        }
        return (int) parsed;
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
