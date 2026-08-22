package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;
import java.time.Instant;

import static org.junit.jupiter.api.Assertions.*;

class BackendSnapshotClientTest {
    @Test
    void decodesCompactSnapshot() {
        byte[] json = ("{\"version\":7,\"generated_at\":\"2026-08-20T12:00:00Z\",\"values\":{" +
                "\"minecraft:elytra\":{\"fair_value\":120000000,\"quick_sell_value\":115000000," +
                "\"confidence_bps\":8000,\"volume_24h\":12}}}").getBytes(StandardCharsets.UTF_8);
        PriceSnapshotCache.Snapshot snapshot = BackendSnapshotClient.decode(json);
        assertEquals(7, snapshot.version());
        assertEquals(Instant.parse("2026-08-20T12:00:00Z"), snapshot.generatedAt());
        assertEquals(115_000_000L, snapshot.values().get("minecraft:elytra").quickSellValue());
    }

    @Test
    void rejectsInvalidConfidence() {
        byte[] json = ("{\"version\":7,\"generated_at\":\"2026-08-20T12:00:00Z\",\"values\":{" +
                "\"minecraft:elytra\":{\"fair_value\":1,\"quick_sell_value\":1," +
                "\"confidence_bps\":10001,\"volume_24h\":1}}}").getBytes(StandardCharsets.UTF_8);
        assertThrows(IllegalArgumentException.class, () -> BackendSnapshotClient.decode(json));
    }
}
